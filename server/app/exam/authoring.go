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
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, string, map[string]any, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

type Effects interface {
	Created(context.Context, model.ExamID) error
}
type EffectFailures interface {
	Report(context.Context, string, error)
}

type memberships interface {
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
}

type Authoring struct {
	persistence store.ExamAuthoringStore
	memberships memberships
	authorizer  Authorizer
	auditor     Auditor
	effects     Effects
	failures    EffectFailures
	now         func() time.Time
	newID       func() model.ExamID
}

func NewAuthoring(persistence store.ExamAuthoringStore, memberships memberships, authorizer Authorizer, auditor Auditor, effects Effects, failures EffectFailures, now func() time.Time, newID func() model.ExamID) (*Authoring, error) {
	if persistence == nil || memberships == nil || authorizer == nil || auditor == nil || effects == nil || failures == nil || now == nil || newID == nil {
		return nil, errors.New("exam authoring dependencies are required")
	}
	return &Authoring{persistence: persistence, memberships: memberships, authorizer: authorizer, auditor: auditor, effects: effects, failures: failures, now: now, newID: newID}, nil
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
	auditID, err := a.auditor.Begin(ctx, call, action, resource, "create", map[string]any{
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
	action := model.ActionExamViewOverride
	if access.ActorIsManager {
		ordinary, membershipErr := a.hasCurrentMembership(ctx, principal.UserID, access.Exam.AcademicUnitID, model.TimeUTC(a.now()))
		if membershipErr != nil {
			return View{}, unavailable(membershipErr)
		}
		if ordinary {
			action = model.ActionExamView
		}
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

func (a *Authoring) hasCurrentMembership(ctx context.Context, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	items, err := a.memberships.ListActiveByUser(ctx, userID.String(), model.MillisFromTime(at))
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
	switch {
	case errors.As(err, &idempotencyConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idempotencyInProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.not_found", Cause: err}
	case store.IsConflict(err):
		return &Fault{Code: "exam.conflict", Cause: err}
	default:
		if errors.As(err, &invalidInput) {
			return &Fault{Code: "exam.invalid", Cause: err}
		}
		return unavailable(err)
	}
}
