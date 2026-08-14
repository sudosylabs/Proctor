// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCreateOwnsAuthorizationAuditPersistenceAndEffects(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	got, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{
		AcademicUnitID: fixture.unitID, Title: "  Algorithms  ", InstructionsMarkdown: "Use **Go**.",
		Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Exam.ID.IsZero() || got.Exam.CreatorUserID != fixture.userID || got.Exam.OwnerUserID != fixture.userID || got.Draft.Title != "Algorithms" || got.ManagerCount != 1 {
		t.Fatalf("view = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamCreate || fixture.authorizer.resource != (model.Resource{Type: model.ResourceAcademicUnit, ID: fixture.unitID.String()}) {
		t.Fatalf("authorization = %s %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	wantOrder := []string{"membership", "authorize", "audit.begin", "store.create", "effect.created"}
	if !reflect.DeepEqual(*fixture.order, wantOrder) {
		t.Fatalf("order = %v, want %v", *fixture.order, wantOrder)
	}
	if fixture.persistence.creation.Exam.ID != fixture.persistence.creation.Draft.ExamID || fixture.persistence.creation.Manager.UserID != fixture.userID {
		t.Fatalf("atomic aggregate = %#v", fixture.persistence.creation)
	}
	if fixture.persistence.creation.Draft.Policy != model.DefaultExamPolicySet() {
		t.Fatalf("policy = %#v", fixture.persistence.creation.Draft.Policy)
	}
}

func TestCreateUsesExplicitOverrideWithoutMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", Idempotency: &store.CommandIdempotency{}})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamCreateOverride {
		t.Fatalf("action = %s, want override", fixture.authorizer.action)
	}
}

func TestCreateRequiresIdempotencyBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
	if len(*fixture.order) != 0 {
		t.Fatalf("side effects before required idempotency validation: %v", *fixture.order)
	}
}

func TestCreateRejectsInvalidDraftBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "   ", Idempotency: &store.CommandIdempotency{}})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.invalid" {
		t.Fatalf("error = %v", err)
	}
	if len(*fixture.order) != 0 {
		t.Fatalf("invalid draft caused effects: %v", *fixture.order)
	}
}

func TestCreateReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	command := &store.CommandIdempotency{UserID: fixture.userID, Operation: "exam.create.v1"}
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", Idempotency: command})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.idempotency != command || fixture.effects.calls != 0 {
		t.Fatalf("idempotency/effects = %#v/%d", fixture.persistence.idempotency, fixture.effects.calls)
	}
}

func TestCreateFailureCompletesAuditAsFailed(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.persistence.err = store.NewErrConflict("exam", "exams_pkey", errors.New("duplicate"))
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", Idempotency: &store.CommandIdempotency{}})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.conflict" {
		t.Fatalf("error = %v", err)
	}
	if fixture.auditor.failedCode != "exam.conflict" {
		t.Fatalf("failed audit code = %q", fixture.auditor.failedCode)
	}
}

func TestEditDraftTextOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Distributed Systems  "
	instructions := "Use **Go** and submit all files."
	command := &store.CommandIdempotency{UserID: fixture.userID, Operation: "exam.draft.text.edit.v1"}
	view, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1,
		Title: &title, InstructionsMarkdown: &instructions, Idempotency: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.Title != "Distributed Systems" || view.Draft.InstructionsMarkdown != instructions || view.Draft.Revision != 2 {
		t.Fatalf("view = %#v", view)
	}
	if fixture.authorizer.action != model.ActionExamManage || fixture.persistence.textUpdate == nil || fixture.persistence.idempotency != command {
		t.Fatalf("authorization/update = %s / %#v", fixture.authorizer.action, fixture.persistence.textUpdate)
	}
	if fixture.auditor.value["title"] != nil || fixture.auditor.value["instructions_markdown"] != nil ||
		fixture.auditor.scopeType != model.RoleScopeAcademicUnit || fixture.auditor.scopeID != fixture.unitID.String() || fixture.effects.updatedRevision != 2 {
		t.Fatalf("unsafe audit/scope/effect = %#v / %s:%s / %d", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID, fixture.effects.updatedRevision)
	}
	want := []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}
	if !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestEditDraftTextNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Test  "
	_, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.draft.no_changes" {
		t.Fatalf("error = %v, want exam.draft.no_changes", err)
	}
	if want := []string{"store.access", "membership", "authorize", "store.get"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestEditDraftTextRejectsArchivedAndStaleDraftsWithoutPublishing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		archived         bool
		expectedRevision int64
		want             string
	}{
		{name: "archived", archived: true, expectedRevision: 1, want: "exam.archived"},
		{name: "stale", expectedRevision: 2, want: "exam.draft.revision_conflict"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.persistence.archived = test.archived
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			title := "Changed"
			_, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: test.expectedRevision, Title: &title,
				Idempotency: &store.CommandIdempotency{},
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.want {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
			if fixture.auditor.value == nil || fixture.persistence.textUpdate == nil || fixture.effects.updatedRevision != 0 || fixture.auditor.failedCode != test.want {
				t.Fatalf("rejected update effects: audit=%#v store=%#v effect=%d failed=%s", fixture.auditor.value, fixture.persistence.textUpdate, fixture.effects.updatedRevision, fixture.auditor.failedCode)
			}
		})
	}
}

func TestEditDraftTextReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "Changed"
	if _, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		Idempotency: &store.CommandIdempotency{},
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.effects.updatedRevision != 0 {
		t.Fatalf("replay published revision %d", fixture.effects.updatedRevision)
	}
}

func TestEditDraftTextUsesExplicitOverrideWithoutManagerMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	title := "Changed"
	if _, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		Idempotency: &store.CommandIdempotency{},
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamManageOverride || fixture.persistence.textUpdate == nil || !fixture.persistence.textUpdate.ManagerOverride {
		t.Fatalf("override action/update = %s / %#v", fixture.authorizer.action, fixture.persistence.textUpdate)
	}
	if want := []string{"store.access", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestGetSelectsManagerOrOverrideAuthorization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		manager bool
		action  model.Action
	}{{"manager", true, model.ActionExamView}, {"override", false, model.ActionExamViewOverride}} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = test.manager
			if test.manager {
				fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			}
			view, err := fixture.service.Get(context.Background(), fixture.call, fixture.examID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Exam.ID != fixture.examID || fixture.authorizer.action != test.action || fixture.authorizer.resource.Type != model.ResourceExam {
				t.Fatalf("view/auth = %#v / %s %#v", view, fixture.authorizer.action, fixture.authorizer.resource)
			}
			want := []string{"store.access", "authorize", "store.get"}
			if test.manager {
				want = []string{"store.access", "membership", "authorize", "store.get"}
			}
			if !reflect.DeepEqual(*fixture.order, want) {
				t.Fatalf("order = %v, want %v", *fixture.order, want)
			}
		})
	}
}

func TestGetRequiresOverrideWhenManagerMembershipWasRevoked(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	denied := errors.New("override denied")
	fixture.authorizer.err = denied
	if _, err := fixture.service.Get(context.Background(), fixture.call, fixture.examID); !errors.Is(err, denied) {
		t.Fatalf("error = %v, want override denial", err)
	}
	if fixture.authorizer.action != model.ActionExamViewOverride {
		t.Fatalf("action = %s, want override after membership revocation", fixture.authorizer.action)
	}
	if want := []string{"store.access", "membership", "authorize"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestCallClonesCredentialScopes(t *testing.T) {
	t.Parallel()
	principal := testPrincipal(model.NewUserID())
	principal.CredentialType = model.CredentialPersonalAccessToken
	principal.SessionID = ""
	principal.CredentialScopes = []string{string(model.ActionExamCreate)}
	principal.AuthenticationStrength = ""
	principal.AuthenticatedAt = time.Time{}
	principal.ClientType = model.SessionClientCLI
	call := NewCall(principal, model.RequestMetadata{RequestID: "request"})
	principal.CredentialScopes[0] = string(model.ActionAuditView)
	got := call.Principal()
	if got.CredentialScopes[0] != string(model.ActionExamCreate) {
		t.Fatalf("call principal was aliased: %#v", got)
	}
}

type authoringFixture struct {
	service     *Authoring
	call        Call
	unitID      model.AcademicUnitID
	examID      model.ExamID
	userID      model.UserID
	order       *[]string
	authorizer  *authorizerFake
	memberships *membershipsFake
	auditor     *auditorFake
	persistence *authoringStoreFake
	effects     *effectsFake
}

func newAuthoringFixture(t *testing.T) authoringFixture {
	t.Helper()
	order := []string{}
	unitID, examID, userID := model.NewAcademicUnitID(), model.NewExamID(), model.NewUserID()
	authorizer := &authorizerFake{order: &order}
	memberships := &membershipsFake{order: &order}
	auditor := &auditorFake{order: &order}
	persistence := &authoringStoreFake{order: &order, examID: examID, unitID: unitID, actorID: userID}
	effects := &effectsFake{order: &order}
	service, err := NewAuthoring(persistence, memberships, authorizer, auditor, effects, effects, func() time.Time {
		return time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	}, func() model.ExamID { return examID })
	if err != nil {
		t.Fatal(err)
	}
	return authoringFixture{service: service, call: NewCall(testPrincipal(userID), model.RequestMetadata{}), unitID: unitID, examID: examID, userID: userID, order: &order, authorizer: authorizer, memberships: memberships, auditor: auditor, persistence: persistence, effects: effects}
}

func testPrincipal(userID model.UserID) model.Principal {
	now := time.Now().UTC()
	return model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: now}
}

type authorizerFake struct {
	order    *[]string
	action   model.Action
	resource model.Resource
	err      error
}

func (f *authorizerFake) Authorize(_ context.Context, _ Call, action model.Action, resource model.Resource) error {
	*f.order = append(*f.order, "authorize")
	f.action, f.resource = action, resource
	return f.err
}

type membershipsFake struct {
	order *[]string
	items []*model.AcademicUnitMember
	err   error
}

func (f *membershipsFake) ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error) {
	*f.order = append(*f.order, "membership")
	return f.items, f.err
}

type auditorFake struct {
	order      *[]string
	failedCode string
	value      map[string]any
	scopeType  model.RoleScopeType
	scopeID    string
	err        error
}

func (f *auditorFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, scopeType model.RoleScopeType, scopeID, _ string, value map[string]any, _ map[string]any) (string, error) {
	*f.order = append(*f.order, "audit.begin")
	f.value = value
	f.scopeType, f.scopeID = scopeType, scopeID
	return model.NewId(), f.err
}
func (f *auditorFake) Fail(_ context.Context, _ string, code string) error {
	*f.order = append(*f.order, "audit.fail")
	f.failedCode = code
	return f.err
}

type authoringStoreFake struct {
	order          *[]string
	examID         model.ExamID
	unitID         model.AcademicUnitID
	actorID        model.UserID
	actorIsManager bool
	archived       bool
	replayed       bool
	creation       *store.ExamAuthoringCreation
	textUpdate     *store.ExamDraftTextUpdate
	idempotency    *store.CommandIdempotency
	err            error
}

func (f *authoringStoreFake) Create(_ context.Context, input *store.ExamAuthoringCreation, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.create")
	f.creation, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshotFromCreation(input, true), Replayed: f.replayed}, nil
}
func (f *authoringStoreFake) UpdateDraftText(_ context.Context, input *store.ExamDraftTextUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_text")
	f.textUpdate, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	snapshot, err := f.Get(context.Background(), input.ExamID, input.ActorUserID)
	*f.order = (*f.order)[:len(*f.order)-1]
	if err != nil {
		return nil, err
	}
	if f.replayed {
		return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: true}, nil
	}
	if !snapshot.ActorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if snapshot.Exam.IsArchived() {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if snapshot.Draft.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	if _, err := snapshot.Draft.ApplyTextPatch(input.Title, input.InstructionsMarkdown, model.TimeFromMillis(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: f.replayed}, nil
}
func (f *authoringStoreFake) Access(_ context.Context, examID model.ExamID, _ model.UserID) (*store.ExamAccessSnapshot, error) {
	*f.order = append(*f.order, "store.access")
	if f.err != nil {
		return nil, f.err
	}
	exam, _ := model.NewExam(examID, f.unitID, f.actorID, time.Now().UTC())
	if f.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(time.Now().UTC())
	}
	return &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: f.actorIsManager}, nil
}
func (f *authoringStoreFake) Get(_ context.Context, examID model.ExamID, _ model.UserID) (*store.ExamAuthoringSnapshot, error) {
	*f.order = append(*f.order, "store.get")
	if f.err != nil {
		return nil, f.err
	}
	at := time.Now().UTC()
	exam, _ := model.NewExam(examID, f.unitID, f.actorID, at)
	if f.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(at)
	}
	draft, _ := model.NewExamDraft(examID, "Test", "", model.DefaultExamPolicySet(), at)
	return &store.ExamAuthoringSnapshot{Exam: exam, Draft: draft, OwnerUserID: f.actorID, ManagerCount: 1, ActorIsManager: f.actorIsManager}, nil
}
func (f *authoringStoreFake) Resolve(context.Context, model.ExamID) (*model.Exam, error) {
	return nil, nil
}
func snapshotFromCreation(input *store.ExamAuthoringCreation, actor bool) *store.ExamAuthoringSnapshot {
	return &store.ExamAuthoringSnapshot{Exam: input.Exam, Draft: input.Draft, OwnerUserID: input.Exam.OwnerUserID, ManagerCount: 1, ActorIsManager: actor}
}

type effectsFake struct {
	order           *[]string
	calls           int
	updatedRevision int64
	err             error
}

func (f *effectsFake) Created(context.Context, model.ExamID) error {
	*f.order = append(*f.order, "effect.created")
	f.calls++
	return f.err
}
func (f *effectsFake) DraftUpdated(_ context.Context, _ model.ExamID, revision int64) error {
	*f.order = append(*f.order, "effect.updated")
	f.updatedRevision = revision
	return f.err
}
func (f *effectsFake) Report(context.Context, string, error) {}
