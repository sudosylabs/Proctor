// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type View struct {
	Exam                model.Exam
	Draft               model.ExamDraft
	OwnerUserID         model.UserID
	ManagerCount        int
	Capacity            model.ExamCapacityPolicy
	ResourceCount       int
	HasStarterWorkspace bool
}

type CreateCommand struct {
	AcademicUnitID       model.AcademicUnitID
	Title                string
	InstructionsMarkdown string
	IdempotencyKey       string
}

type EditDraftTextCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Title                 *string
	InstructionsMarkdown  *string
	IdempotencyKey        string
}

type ConfigureDraftFocusLossCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	FocusLoss             model.FocusLossPolicy
	IdempotencyKey        string
}

type ConfigureDraftBrowserPolicyCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Policy                model.BrowserPolicy
	IdempotencyKey        string
}

type Authorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	AuthorizeList(context.Context, invocation.Invocation, model.AcademicUnitID) (store.ExamListVisibility, error)
}

type Auditor interface {
	Begin(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
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

type CommandOutcomes interface {
	Has(context.Context, *store.CommandIdempotency) (bool, error)
}

type users interface {
	Get(context.Context, model.UserID) (*model.User, error)
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

type Authoring struct{ deps AuthoringDependencies }

// AuthoringDependencies are the collaborators of Authoring; NewAuthoring validates them once and the
// service retains them unchanged.
type AuthoringDependencies struct {
	Persistence       store.ExamAuthoringStore
	Memberships       manageraccess.Memberships
	Users             users
	Mail              ManagerMailPreparer
	Authorizer        Authorizer
	Auditor           Auditor
	Outcomes          CommandOutcomes
	Effects           Effects
	EffectFailures    EffectFailures
	Now               func() time.Time
	NewID             func() model.ExamID
	InstitutionOrigin string
}

func NewAuthoring(deps AuthoringDependencies) (*Authoring, error) {
	if deps.Persistence == nil || deps.Memberships == nil || deps.Users == nil || deps.Mail == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Outcomes == nil || deps.Effects == nil || deps.EffectFailures == nil || deps.Now == nil || deps.NewID == nil {
		return nil, errors.New("exam authoring dependencies are required")
	}
	return &Authoring{deps: deps}, nil
}

func (a *Authoring) Create(ctx context.Context, invocation invocation.Invocation, command CreateCommand) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return View{}, apperror.Invalid("exam.invalid", "principal")
	}
	if !command.AcademicUnitID.IsValid() {
		return View{}, apperror.Invalid("exam.invalid", "academic_unit_id")
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationCreate, command.IdempotencyKey, struct {
		AcademicUnitID       string `json:"academic_unit_id"`
		Title                string `json:"title"`
		InstructionsMarkdown string `json:"instructions_markdown"`
	}{command.AcademicUnitID.String(), command.Title, command.InstructionsMarkdown})
	if err != nil {
		return View{}, err
	}
	at := model.TimeUTC(a.deps.Now())
	examID := a.deps.NewID()
	if !examID.IsValid() {
		return View{}, apperror.Invalid("exam.invalid", "exam_id")
	}
	exam, err := model.NewExam(examID, command.AcademicUnitID, principal.UserID, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "exam").Wrap(err)
	}
	draft, err := model.NewExamDraft(examID, command.Title, command.InstructionsMarkdown, model.DefaultExamPolicySet(), at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "draft").Wrap(err)
	}
	manager, err := model.NewExamManager(examID, principal.UserID, principal.UserID, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "manager").Wrap(err)
	}
	ordinary, err := manageraccess.HasCurrentMembership(ctx, a.deps.Memberships, principal.UserID, command.AcademicUnitID, at)
	if err != nil {
		return View{}, apperror.New("exam.unavailable").Wrap(err)
	}
	action := model.ActionExamCreateOverride
	if ordinary {
		action = model.ActionExamCreate
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: command.AcademicUnitID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return View{}, err
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, command.AcademicUnitID.String(), "create", map[string]any{
		"exam_id": examID.String(), "academic_unit_id": command.AcademicUnitID.String(),
		"creator_user_id": principal.UserID.String(),
	}, nil)
	if err != nil {
		return View{}, err
	}
	creation := &store.ExamAuthoringCreation{Exam: exam, Draft: draft, Manager: manager, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}
	result, err := a.deps.Persistence.Create(ctx, creation, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return View{}, errors.Join(mapped, auditErr)
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no creation result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.Created(ctx, result.Value.Exam.ID); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_created", effectErr)
		}
	}
	return project(result.Value), nil
}

func (a *Authoring) Get(ctx context.Context, invocation invocation.Invocation, examID model.ExamID) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return View{}, apperror.Invalid("exam.invalid", "exam_id")
	}
	access, err := a.deps.Persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, model.TimeUTC(a.deps.Now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return View{}, err
	}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return View{}, err
	}
	snapshot, err := a.deps.Persistence.Get(ctx, examID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned an incomplete snapshot"))
	}
	return project(snapshot), nil
}

// AuthorizeView performs the same current relationship and role decision used
// by Get without loading authored Draft content. Realtime subscriptions use it
// so a scoped role alone cannot reveal Exam events to a non-manager.
func (a *Authoring) AuthorizeView(ctx context.Context, invocation invocation.Invocation, examID model.ExamID) error {
	principal := invocation.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return apperror.Invalid("exam.invalid", "exam_id")
	}
	access, err := a.deps.Persistence.Access(ctx, examID, principal.UserID)
	if err != nil {
		return examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, model.TimeUTC(a.deps.Now()), model.ActionExamView, model.ActionExamViewOverride)
	if err != nil {
		return err
	}
	return a.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: examID.String()})
}

func (a *Authoring) EditDraftText(ctx context.Context, invocation invocation.Invocation, command EditDraftTextCommand) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, apperror.Invalid("exam.invalid", "draft_revision")
	}
	if command.Title == nil && command.InstructionsMarkdown == nil {
		return View{}, apperror.Invalid("exam.invalid", "fields")
	}
	title := cloneStringPointer(command.Title)
	if title != nil {
		*title = strings.TrimSpace(*title)
	}
	instructions := cloneStringPointer(command.InstructionsMarkdown)
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationEditDraftText, command.IdempotencyKey, struct {
		ExamID                string  `json:"exam_id"`
		ExpectedDraftRevision int64   `json:"expected_draft_revision"`
		Title                 *string `json:"title"`
		InstructionsMarkdown  *string `json:"instructions_markdown"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, title, instructions})
	if err != nil {
		return View{}, err
	}
	at := model.TimeUTC(a.deps.Now())

	access, err := a.deps.Persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.deps.Persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyTextPatch(title, instructions, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "draft").Wrap(err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, apperror.New("exam.draft.no_changes")
	}
	if title != nil {
		title = &candidate.Title
	}
	if instructions != nil {
		instructions = &candidate.InstructionsMarkdown
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "edit_draft_text", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.deps.Persistence.UpdateDraftText(ctx, &store.ExamDraftTextUpdate{
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision,
		Title:            title, InstructionsMarkdown: instructions, UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return View{}, errors.Join(mapped, auditErr)
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no Draft update result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_draft_updated", effectErr)
		}
	}
	return project(result.Value), nil
}

// ConfigureDraftFocusLoss replaces only the supported Focus Loss rule. The
// complete policy is reconstructed from authoritative Draft state so callers
// cannot supply or weaken Connection Loss.
func (a *Authoring) ConfigureDraftFocusLoss(ctx context.Context, invocation invocation.Invocation, command ConfigureDraftFocusLossCommand) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, apperror.Invalid("exam.invalid", "draft_revision")
	}
	command.FocusLoss.MinimumDuration = time.Duration(command.FocusLoss.MinimumDuration.Milliseconds()) * time.Millisecond
	command.FocusLoss.Window = time.Duration(command.FocusLoss.Window.Milliseconds()) * time.Millisecond
	policy := model.DefaultExamPolicySet()
	policy.FocusLoss = command.FocusLoss
	if err := policy.Validate(); err != nil {
		return View{}, apperror.Invalid("exam.invalid", "focus_loss").Wrap(err)
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationConfigureDraftFocusLoss, command.IdempotencyKey, struct {
		ExamID                      string                          `json:"exam_id"`
		ExpectedDraftRevision       int64                           `json:"expected_draft_revision"`
		Enabled                     bool                            `json:"enabled"`
		MinimumDurationMilliseconds int64                           `json:"minimum_duration_milliseconds"`
		IncidentCount               int                             `json:"incident_count"`
		WindowMilliseconds          int64                           `json:"window_milliseconds"`
		Outcome                     model.IntegrityThresholdOutcome `json:"outcome"`
	}{
		ExamID: command.ExamID.String(), ExpectedDraftRevision: command.ExpectedDraftRevision,
		Enabled: command.FocusLoss.Enabled, MinimumDurationMilliseconds: command.FocusLoss.MinimumDuration.Milliseconds(),
		IncidentCount: command.FocusLoss.IncidentCount, WindowMilliseconds: command.FocusLoss.Window.Milliseconds(), Outcome: command.FocusLoss.Outcome,
	})
	if err != nil {
		return View{}, err
	}
	at := model.TimeUTC(a.deps.Now())
	access, err := a.deps.Persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.deps.Persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyFocusLossPolicy(command.FocusLoss, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "focus_loss").Wrap(err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, apperror.New("exam.draft.no_changes")
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "configure_draft_focus_loss", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.deps.Persistence.UpdateDraftFocusLoss(ctx, &store.ExamDraftFocusLossUpdate{
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision, FocusLoss: command.FocusLoss, UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return View{}, errors.Join(mapped, auditErr)
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no Focus Loss update result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_draft_updated", effectErr)
		}
	}
	return project(result.Value), nil
}

// ConfigureDraftBrowserPolicy replaces the complete canonical enforcement
// value. Publication, rather than this mutable Draft, controls open Sittings.
func (a *Authoring) ConfigureDraftBrowserPolicy(ctx context.Context, invocation invocation.Invocation, command ConfigureDraftBrowserPolicyCommand) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, apperror.Invalid("exam.invalid", "draft_revision")
	}
	policy, err := model.NewBrowserPolicy(command.Policy.Enabled, command.Policy.StartRuleID, command.Policy.Rules)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "browser_policy").Wrap(err)
	}
	if err = policy.ValidateInstitutionOrigin(a.deps.InstitutionOrigin); err != nil {
		return View{}, apperror.Invalid("exam.invalid", "browser_policy").Wrap(err)
	}
	canonical, err := model.EncodeBrowserPolicy(policy)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "browser_policy").Wrap(err)
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationConfigureBrowserPolicy, command.IdempotencyKey, struct {
		ExamID                string          `json:"exam_id"`
		ExpectedDraftRevision int64           `json:"expected_draft_revision"`
		Policy                json.RawMessage `json:"browser_policy"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, canonical})
	if err != nil {
		return View{}, err
	}
	at := model.TimeUTC(a.deps.Now())
	access, err := a.deps.Persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.deps.Persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyBrowserPolicy(policy, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "browser_policy").Wrap(err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, apperror.New("exam.draft.no_changes")
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "configure_draft_browser_policy", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1, "browser_policy_enabled": policy.Enabled,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.deps.Persistence.UpdateDraftBrowserPolicy(ctx, &store.ExamDraftBrowserPolicyUpdate{InstitutionOrigin: a.deps.InstitutionOrigin,
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision, Policy: policy, UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return View{}, errors.Join(mapped, auditErr)
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no Browser Policy update result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_draft_updated", effectErr)
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
	action, err := manageraccess.SelectAction(ctx, a.deps.Memberships, userID, access, at, ordinaryAction, overrideAction)
	if err != nil {
		return "", apperror.New("exam.unavailable").Wrap(err)
	}
	return action, nil
}

func project(snapshot *store.ExamAuthoringSnapshot) View {
	return View{Exam: *snapshot.Exam, Draft: *snapshot.Draft, OwnerUserID: snapshot.OwnerUserID,
		ManagerCount: snapshot.ManagerCount, Capacity: snapshot.Capacity,
		ResourceCount: snapshot.ResourceCount, HasStarterWorkspace: snapshot.HasStarterWorkspace}
}

var examStoreErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"exam_archived":                   "exam.archived",
		"exam_draft_revision":             "exam.draft.revision_conflict",
		"exam_revision":                   "exam.revision_conflict",
		"exam_draft_no_changes":           "exam.draft.no_changes",
		"exam_revision_no_changes":        "exam.revision.no_changes",
		"exam_revision_capacity":          "exam.revision.capacity_exceeded",
		"exam_manager_exists":             "exam.manager.exists",
		"exam_manager_missing":            "exam.manager.not_found",
		"exam_manager_ineligible":         "exam.manager.ineligible",
		"exam_manager_candidate_conflict": "exam.manager.candidate_conflict",
		"exam_owner_manager":              "exam.manager.owner_protected",
		"exam_owner_no_changes":           "exam.owner.no_changes",
	},
	NotFound:    "exam.not_found",
	Conflict:    "exam.conflict",
	Invalid:     "exam.invalid",
	Unavailable: "exam.unavailable",
}

type ConfigureDraftNativePolicyCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	NativePolicy          model.NativeSecurityPolicy
	IdempotencyKey        string
}

func (a *Authoring) ConfigureDraftNativePolicy(ctx context.Context, invocation invocation.Invocation, command ConfigureDraftNativePolicyCommand) (View, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 {
		return View{}, apperror.Invalid("exam.invalid", "draft_revision")
	}
	policy := model.DefaultExamPolicySet()
	policy.Native = command.NativePolicy
	if err := policy.Validate(); err != nil {
		return View{}, apperror.Invalid("exam.invalid", "native_policy").Wrap(err)
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationConfigureDraftNativePolicy, command.IdempotencyKey, struct {
		ExamID                string                     `json:"exam_id"`
		ExpectedDraftRevision int64                      `json:"expected_draft_revision"`
		Native                model.NativeSecurityPolicy `json:"native"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, command.NativePolicy.Clone()})
	if err != nil {
		return View{}, err
	}
	at := model.TimeUTC(a.deps.Now())
	access, err := a.deps.Persistence.Access(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no access projection"))
	}
	action, err := a.actionForAccess(ctx, principal.UserID, access, at, model.ActionExamManage, model.ActionExamManageOverride)
	if err != nil {
		return View{}, err
	}
	resource := model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()}
	if err = a.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return View{}, err
	}
	snapshot, err := a.deps.Persistence.Get(ctx, command.ExamID, principal.UserID)
	if err != nil {
		return View{}, examStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned an incomplete snapshot"))
	}
	candidate := *snapshot.Draft
	changed, err := candidate.ApplyNativePolicy(command.NativePolicy, at)
	if err != nil {
		return View{}, apperror.Invalid("exam.invalid", "native_policy").Wrap(err)
	}
	if !changed && snapshot.Draft.Revision == command.ExpectedDraftRevision && !snapshot.Exam.IsArchived() {
		return View{}, apperror.New("exam.draft.no_changes")
	}
	auditID, err := a.deps.Auditor.Begin(ctx, invocation, action, resource, model.RoleScopeAcademicUnit, access.Exam.AcademicUnitID.String(), "configure_draft_native_policy", map[string]any{
		"exam_id": command.ExamID.String(), "expected_draft_revision": command.ExpectedDraftRevision,
		"draft_revision": command.ExpectedDraftRevision + 1,
	}, nil)
	if err != nil {
		return View{}, err
	}
	result, err := a.deps.Persistence.UpdateDraftNativePolicy(ctx, &store.ExamDraftNativePolicyUpdate{
		ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: action == model.ActionExamManageOverride,
		ExpectedRevision: command.ExpectedDraftRevision, NativePolicy: command.NativePolicy.Clone(), UpdatedAt: model.MillisFromTime(candidate.UpdatedAt),
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
	}, idempotency)
	if err != nil {
		mapped := examStoreErrors.Translate(err)
		if auditErr := a.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
			return View{}, errors.Join(mapped, auditErr)
		}
		return View{}, mapped
	}
	if result == nil || result.Value == nil || result.Value.Draft == nil {
		return View{}, apperror.New("exam.unavailable").Wrap(errors.New("exam store returned no native policy update result"))
	}
	if !result.Replayed {
		if effectErr := a.deps.Effects.DraftUpdated(ctx, result.Value.Exam.ID, result.Value.Draft.Revision); effectErr != nil {
			a.deps.EffectFailures.Report(ctx, "exam_draft_updated", effectErr)
		}
	}
	return project(result.Value), nil
}
