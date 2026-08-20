// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type View struct {
	Exam                model.Exam
	Draft               model.ExamDraft
	OwnerUserID         model.UserID
	ManagerCount        int
	ResourceCount       int
	HasStarterWorkspace bool
}

type CreateCommand struct {
	AcademicUnitID       model.AcademicUnitID
	Title                string
	InstructionsMarkdown string
	Idempotency          *store.CommandIdempotency
}

type EditDraftTextCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Title                 *string
	InstructionsMarkdown  *string
	Idempotency           *store.CommandIdempotency
}

type ConfigureDraftFocusLossCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	FocusLoss             model.FocusLossPolicy
	Idempotency           *store.CommandIdempotency
}

// Call is immutable security and safe audit context owned by this child
// package. It prevents the child from importing the parent app package.
type Call struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

func NewCall(principal model.Principal, metadata model.RequestMetadata) Call {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Call{principal: principal, metadata: metadata}
}

func (c Call) Principal() model.Principal {
	principal := c.principal
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return principal
}

func (c Call) RequestMetadata() model.RequestMetadata { return c.metadata }

type Fault struct {
	Code       string
	SafeFields map[string]any
	Cause      error
}

func (f *Fault) Error() string {
	if f == nil {
		return "exam fault"
	}
	return f.Code
}

func (f *Fault) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

type Authorizer interface {
	Authorize(context.Context, Call, model.Action, model.Resource) error
	AuthorizeList(context.Context, Call, model.AcademicUnitID) (store.ExamListVisibility, error)
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

type Effects interface {
	Created(context.Context, model.ExamID) error
	DraftUpdated(context.Context, model.ExamID, int64) error
	Archived(context.Context, model.ExamID, int64, time.Time) error
	ManagerChanged(context.Context, model.ExamID, model.UserID, bool, int64, time.Time) error
	OwnerTransferred(context.Context, model.ExamID, model.UserID, int64, time.Time) error
}
type EffectFailures interface {
	Report(context.Context, string, error)
}

type memberships interface {
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
}

type users interface {
	Get(context.Context, string) (*model.User, error)
}

type ManagerMailRelationship string

const (
	ManagerMailRelationshipManager         ManagerMailRelationship = "manager"
	ManagerMailRelationshipOwner           ManagerMailRelationship = "owner"
	ManagerMailRelationshipNoLongerManager ManagerMailRelationship = "no_longer_manager"
)

type ManagerMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	ExamTitle    string
	Relationship ManagerMailRelationship
	ActionAt     time.Time
}

type ManagerMailPreparer interface {
	PrepareManagerMail(ManagerMailPreparation) (*store.ExamManagerMail, error)
}

type Authoring struct {
	persistence store.ExamAuthoringStore
	memberships memberships
	users       users
	mail        ManagerMailPreparer
	authorizer  Authorizer
	auditor     Auditor
	effects     Effects
	failures    EffectFailures
	now         func() time.Time
	newID       func() model.ExamID
}

func NewAuthoring(persistence store.ExamAuthoringStore, memberships memberships, users users, mail ManagerMailPreparer, authorizer Authorizer, auditor Auditor, effects Effects, failures EffectFailures, now func() time.Time, newID func() model.ExamID) (*Authoring, error) {
	if persistence == nil || memberships == nil || users == nil || mail == nil || authorizer == nil || auditor == nil || effects == nil || failures == nil || now == nil || newID == nil {
		return nil, errors.New("exam authoring dependencies are required")
	}
	return &Authoring{persistence: persistence, memberships: memberships, users: users, mail: mail, authorizer: authorizer, auditor: auditor, effects: effects, failures: failures, now: now, newID: newID}, nil
}

func (a *Authoring) Create(ctx context.Context, call Call, command CreateCommand) (View, error) {
	principal := call.Principal()
	if principal.Validate() != nil {
		return View{}, invalid("principal")
	}
	if !command.AcademicUnitID.IsValid() {
		return View{}, invalid("academic_unit_id")
	}
	if command.Idempotency == nil {
		return View{}, &Fault{Code: "idempotency.key_required"}
	}
	at := model.TimeUTC(a.now())
	examID := a.newID()
	if !examID.IsValid() {
		return View{}, invalid("exam_id")
	}
	exam, err := model.NewExam(examID, command.AcademicUnitID, principal.UserID, at)
	if err != nil {
		return View{}, invalidCause("exam", err)
	}
	draft, err := model.NewExamDraft(examID, command.Title, command.InstructionsMarkdown, model.DefaultExamPolicySet(), at)
	if err != nil {
		return View{}, invalidCause("draft", err)
	}
	manager, err := model.NewExamManager(examID, principal.UserID, principal.UserID, at)
	if err != nil {
		return View{}, invalidCause("manager", err)
	}
	ordinary, err := a.hasCurrentMembership(ctx, principal.UserID, command.AcademicUnitID, at)
	if err != nil {
		return View{}, unavailable(err)
	}
	action := model.ActionExamCreateOverride
	if ordinary {
		action = model.ActionExamCreate
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: command.AcademicUnitID.String()}
	if err := a.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return View{}, err
	}
	auditID, err := a.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, command.AcademicUnitID.String(), "create", map[string]any{
		"exam_id": examID.String(), "academic_unit_id": command.AcademicUnitID.String(),
		"creator_user_id": principal.UserID.String(),
	}, nil)
	if err != nil {
		return View{}, err
	}
	creation := &store.ExamAuthoringCreation{Exam: exam, Draft: draft, Manager: manager, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}
	result, err := a.persistence.Create(ctx, creation, command.Idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		var fault *Fault
		if !errors.As(mapped, &fault) {
			fault = &Fault{Code: "exam.unavailable", Cause: mapped}
		}
		if auditErr := a.auditor.Fail(ctx, auditID, fault.Code); auditErr != nil {
			return View{}, auditErr
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil {
		return View{}, unavailable(errors.New("exam store returned no creation result"))
	}
	if !result.Replayed {
		if effectErr := a.effects.Created(ctx, result.Value.Exam.ID); effectErr != nil {
			a.failures.Report(ctx, "exam_created", effectErr)
		}
	}
	return project(result.Value), nil
}

func (a *Authoring) Get(ctx context.Context, call Call, examID model.ExamID) (View, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return View{}, invalid("exam_id")
	}
	access, err := a.persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, model.TimeUTC(a.now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return View{}, err
	}
	if err := a.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return View{}, err
	}
	snapshot, err := a.persistence.Get(ctx, examID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, unavailable(errors.New("exam store returned an incomplete snapshot"))
	}
	return project(snapshot), nil
}

// AuthorizeView performs the same current relationship and role decision used
// by Get without loading authored Draft content. Realtime subscriptions use it
// so a scoped role alone cannot reveal Exam events to a non-manager.
func (a *Authoring) AuthorizeView(ctx context.Context, call Call, examID model.ExamID) error {
	principal := call.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return invalid("exam_id")
	}
	access, err := a.persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, model.TimeUTC(a.now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return err
	}
	return a.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: examID.String()})
}

func (a *Authoring) EditDraftText(ctx context.Context, call Call, command EditDraftTextCommand) (View, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, invalid("draft_revision")
	}
	if command.Idempotency == nil {
		return View{}, &Fault{Code: "idempotency.key_required"}
	}
	if command.Title == nil && command.InstructionsMarkdown == nil {
		return View{}, invalid("fields")
	}
	title := cloneStringPointer(command.Title)
	instructions := cloneStringPointer(command.InstructionsMarkdown)
	at := model.TimeUTC(a.now())

	access, err := a.persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err := a.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, unavailable(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyTextPatch(title, instructions, at)
	if err != nil {
		return View{}, invalidCause("draft", err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, &Fault{Code: "exam.draft.no_changes"}
	}
	if title != nil {
		title = &candidate.Title
	}
	if instructions != nil {
		instructions = &candidate.InstructionsMarkdown
	}
	auditID, err := a.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "edit_draft_text", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.persistence.UpdateDraftText(ctx, &store.ExamDraftTextUpdate{
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision,
		Title:            title, InstructionsMarkdown: instructions, UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		var fault *Fault
		if !errors.As(mapped, &fault) {
			fault = &Fault{Code: "exam.unavailable", Cause: mapped}
		}
		if auditErr := a.auditor.Fail(ctx, auditID, fault.Code); auditErr != nil {
			return View{}, auditErr
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, unavailable(errors.New("exam store returned no Draft update result"))
	}
	if !result.Replayed {
		if effectErr := a.effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.failures.Report(ctx, "exam_draft_updated", effectErr)
		}
	}
	return project(result.Value), nil
}

// ConfigureDraftFocusLoss replaces only the supported Focus Loss rule. The
// complete policy is reconstructed from authoritative Draft state so callers
// cannot supply or weaken Connection Loss.
func (a *Authoring) ConfigureDraftFocusLoss(ctx context.Context, call Call, command ConfigureDraftFocusLossCommand) (View, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, invalid("draft_revision")
	}
	if command.Idempotency == nil {
		return View{}, &Fault{Code: "idempotency.key_required"}
	}
	policy := model.DefaultExamPolicySet()
	policy.FocusLoss = command.FocusLoss
	if err := policy.Validate(); err != nil {
		return View{}, invalidCause("focus_loss", err)
	}
	at := model.TimeUTC(a.now())
	access, err := a.persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, unavailable(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err := a.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, mapStoreError(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, unavailable(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyFocusLossPolicy(command.FocusLoss, at)
	if err != nil {
		return View{}, invalidCause("focus_loss", err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, &Fault{Code: "exam.draft.no_changes"}
	}
	auditID, err := a.auditor.Begin(ctx, call, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "configure_draft_focus_loss", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.persistence.UpdateDraftFocusLoss(ctx, &store.ExamDraftFocusLossUpdate{
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision, FocusLoss: command.FocusLoss, UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		var fault *Fault
		if !errors.As(mapped, &fault) {
			fault = &Fault{Code: "exam.unavailable", Cause: mapped}
		}
		if auditErr := a.auditor.Fail(ctx, auditID, fault.Code); auditErr != nil {
			return View{}, auditErr
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, unavailable(errors.New("exam store returned no Focus Loss update result"))
	}
	if !result.Replayed {
		if effectErr := a.effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.failures.Report(ctx, "exam_draft_updated", effectErr)
		}
	}
	return project(result.Value), nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (a *Authoring) actionForAccess(ctx context.Context, userID model.UserID, access *store.ExamAccessSnapshot, at time.Time, ordinaryAction, overrideAction model.Action) (model.Action, error) {
	return actionForAccess(ctx, a.memberships, userID, access, at, ordinaryAction, overrideAction)
}

func actionForAccess(ctx context.Context, memberships memberships, userID model.UserID, access *store.ExamAccessSnapshot, at time.Time, ordinaryAction, overrideAction model.Action) (model.Action, error) {
	if access.ActorIsManager {
		ordinary, err := hasCurrentMembership(ctx, memberships, userID, access.Exam.AcademicUnitID, at)
		if err != nil {
			return "", unavailable(err)
		}
		if ordinary {
			return ordinaryAction, nil
		}
	}
	return overrideAction, nil
}

func (a *Authoring) hasCurrentMembership(ctx context.Context, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	return hasCurrentMembership(ctx, a.memberships, userID, unitID, at)
}

func hasCurrentMembership(ctx context.Context, memberships memberships, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	items, err := memberships.ListActiveByUser(ctx, userID.String(), model.MillisFromTime(at))
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item != nil && item.AcademicUnitID == unitID {
			return true, nil
		}
	}
	return false, nil
}

func project(snapshot *store.ExamAuthoringSnapshot) View {
	return View{Exam: *snapshot.Exam, Draft: *snapshot.Draft, OwnerUserID: snapshot.OwnerUserID,
		ManagerCount: snapshot.ManagerCount, ResourceCount: snapshot.ResourceCount, HasStarterWorkspace: snapshot.HasStarterWorkspace}
}

func invalid(field string) error {
	return &Fault{Code: "exam.invalid", SafeFields: map[string]any{"field": field}}
}
func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}
func unavailable(cause error) error { return &Fault{Code: "exam.unavailable", Cause: cause} }

func mapStoreError(err error) error {
	var idempotencyConflict *store.ErrIdempotencyConflict
	var idempotencyInProgress *store.ErrIdempotencyInProgress
	var invalidInput *store.ErrInvalidInput
	var conflict *store.ErrConflict
	switch {
	case errors.As(err, &idempotencyConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idempotencyInProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.not_found", Cause: err}
	case errors.As(err, &conflict):
		switch conflict.Constraint {
		case "exam_archived":
			return &Fault{Code: "exam.archived", Cause: err}
		case "exam_draft_revision":
			return &Fault{Code: "exam.draft.revision_conflict", Cause: err}
		case "exam_revision":
			return &Fault{Code: "exam.revision_conflict", Cause: err}
		case "exam_draft_no_changes":
			return &Fault{Code: "exam.draft.no_changes", Cause: err}
		case "exam_revision_no_changes":
			return &Fault{Code: "exam.revision.no_changes", Cause: err}
		case "exam_manager_exists":
			return &Fault{Code: "exam.manager.exists", Cause: err}
		case "exam_manager_missing":
			return &Fault{Code: "exam.manager.not_found", Cause: err}
		case "exam_manager_ineligible":
			return &Fault{Code: "exam.manager.ineligible", Cause: err}
		case "exam_owner_manager":
			return &Fault{Code: "exam.manager.owner_protected", Cause: err}
		case "exam_owner_no_changes":
			return &Fault{Code: "exam.owner.no_changes", Cause: err}
		default:
			return &Fault{Code: "exam.conflict", Cause: err}
		}
	default:
		if errors.As(err, &invalidInput) {
			return &Fault{Code: "exam.invalid", Cause: err}
		}
		return unavailable(err)
	}
}
