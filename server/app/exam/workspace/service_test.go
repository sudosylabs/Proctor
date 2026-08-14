// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestServiceCreatesAFileOnlyAfterOpaqueContentAndDurableFinalize(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	body := []byte("package main\n")
	result, err := fixture.service.CreateFile(context.Background(), fixture.call, CreateFileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "cmd/main.go", MediaType: "text/x-go",
		ExpectedSHA256: strings.Repeat("a", 64), Body: bytes.NewReader(body), Size: int64(len(body)), Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.create.v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Entry.Path != "cmd/main.go" || result.DraftRevision != 2 || fixture.persistence.reservation == nil || fixture.persistence.mutation == nil {
		t.Fatalf("result/store = %#v/%#v", result, fixture.persistence)
	}
	if fixture.content.stagedBeforeFinalize != true || fixture.effects.operation != "file_created" {
		t.Fatalf("effect/content ordering = %#v/%#v", fixture.effects, fixture.content)
	}
	if fixture.effects.examID != fixture.examID || fixture.effects.entryID != result.Entry.ID || fixture.effects.revision != 2 ||
		fixture.effects.changedAt != time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("safe realtime projection = %#v", fixture.effects)
	}
	if fixture.auditor.values["path"] != nil || fixture.auditor.values["sha256"] != nil {
		t.Fatalf("sensitive values entered audit: %#v", fixture.auditor.values)
	}
}

func TestServiceDoesNotDeleteOrReclaimAStagedObjectAfterUnknownFinalize(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.persistence.finalizeErr = context.DeadlineExceeded
	_, err := fixture.service.CreateFile(context.Background(), fixture.call, CreateFileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/x-go",
		ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1, Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.create.v1"},
	})
	if faultCode(err) != "exam.starter_workspace.unavailable" || fixture.persistence.reclaimed {
		t.Fatalf("error/reclaimed = %v/%v", err, fixture.persistence.reclaimed)
	}
}

func TestServiceExactCreateFileRetryReturnsCommittedOutcomeWithoutRepublishing(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	command := CreateFileCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/plain",
		ExpectedSHA256: strings.Repeat("a", 64), Size: 1, Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.create.v1"}}
	command.Body = strings.NewReader("x")
	first, err := fixture.service.CreateFile(context.Background(), fixture.call, command)
	if err != nil {
		t.Fatal(err)
	}
	command.Body = strings.NewReader("x")
	replayed, err := fixture.service.CreateFile(context.Background(), fixture.call, command)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Entry.ID != first.Entry.ID || replayed.Object == nil || first.Object == nil || replayed.Object.ID != first.Object.ID || replayed.DraftRevision != first.DraftRevision {
		t.Fatalf("first=%#v replayed=%#v", first, replayed)
	}
	if fixture.persistence.finalizeCalls != 2 || len(fixture.persistence.reservations) != 2 || len(fixture.content.stagedIDs) != 2 ||
		len(fixture.persistence.reclaimedIDs) != 1 || fixture.persistence.reclaimedIDs[0] != fixture.content.stagedIDs[1] || fixture.effects.count != 1 {
		t.Fatalf("store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	}
}

func TestServiceExactReplaceFileRetryReturnsCommittedOutcomeWithoutRepublishing(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	command := ReplaceFileCommand{ExamID: fixture.examID, EntryID: model.NewStarterWorkspaceEntryID(), ExpectedDraftRevision: 1,
		ExpectedContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/plain", ExpectedSHA256: strings.Repeat("a", 64), Size: 1,
		Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.replace.v1"}}
	command.Body = strings.NewReader("x")
	first, err := fixture.service.ReplaceFile(context.Background(), fixture.call, command)
	if err != nil {
		t.Fatal(err)
	}
	command.Body = strings.NewReader("x")
	replayed, err := fixture.service.ReplaceFile(context.Background(), fixture.call, command)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Entry.ID != first.Entry.ID || replayed.Object == nil || first.Object == nil || replayed.Object.ID != first.Object.ID || replayed.DraftRevision != first.DraftRevision {
		t.Fatalf("first=%#v replayed=%#v", first, replayed)
	}
	if len(fixture.persistence.reclaimedIDs) != 1 || fixture.persistence.reclaimedIDs[0] != fixture.content.stagedIDs[1] || fixture.effects.count != 1 {
		t.Fatalf("store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	}
}

func TestServiceReplaceRequiresAndCarriesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	expected := model.WorkspaceContentVersion("wwwwwwwwwwwwwwwwwwwwwwwwww")
	result, err := fixture.service.ReplaceFile(context.Background(), fixture.call, ReplaceFileCommand{
		ExamID: fixture.examID, EntryID: model.StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), ExpectedDraftRevision: 1,
		ExpectedContentVersion: expected, MediaType: "text/plain", ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
		Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.replace.v1"},
	})
	if err != nil || result.DraftRevision != 2 || fixture.persistence.mutation == nil || fixture.persistence.mutation.ExpectedContentVersion != expected {
		t.Fatalf("result=%#v err=%v mutation=%#v", result, err, fixture.persistence.mutation)
	}
}

func TestServiceMapsStaleContentVersionAndReclaimsNewObject(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.persistence.finalizeErr = store.NewErrConflict("exam_starter_workspace_entry", "workspace_content_version", nil)
	_, err := fixture.service.ReplaceFile(context.Background(), fixture.call, ReplaceFileCommand{
		ExamID: fixture.examID, EntryID: model.StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), ExpectedDraftRevision: 1,
		ExpectedContentVersion: model.WorkspaceContentVersion("wwwwwwwwwwwwwwwwwwwwwwwwww"), MediaType: "text/plain",
		ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
		Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.replace.v1"},
	})
	if faultCode(err) != "exam.starter_workspace.content_conflict" || !fixture.persistence.reclaimed {
		t.Fatalf("error=%v reclaimed=%v", err, fixture.persistence.reclaimed)
	}
}

func TestServiceClassifiesInvalidStagedBytesSeparatelyFromBackendFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "invalid", err: fakeInvalidWorkspaceContentError{}, code: "exam.starter_workspace.invalid"},
		{name: "backend", err: errors.New("opaque backend failure"), code: "exam.starter_workspace.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			fixture.content.stageErr = test.err
			_, err := fixture.service.CreateFile(context.Background(), fixture.call, CreateFileCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/plain",
				ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
				Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.file.create.v1"},
			})
			if faultCode(err) != test.code || !fixture.persistence.reclaimed {
				t.Fatalf("error=%v reclaimed=%v", err, fixture.persistence.reclaimed)
			}
		})
	}
}

type fakeInvalidWorkspaceContentError struct{}

func (fakeInvalidWorkspaceContentError) Error() string                        { return "invalid content" }
func (fakeInvalidWorkspaceContentError) InvalidStarterWorkspaceContent() bool { return true }

func TestServiceRejectsInvalidPathsBeforeAuthorizationOrStorage(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	_, err := fixture.service.CreateDirectory(context.Background(), fixture.call, CreateDirectoryCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "../escape",
		Idempotency: &store.CommandIdempotency{Operation: "exam.starter_workspace.directory.create.v1"},
	})
	if faultCode(err) != "exam.starter_workspace.invalid" || fixture.authorizer.called || fixture.persistence.mutation != nil {
		t.Fatalf("error/auth/store = %v/%v/%#v", err, fixture.authorizer.called, fixture.persistence.mutation)
	}
}

type serviceFixture struct {
	service     *Service
	call        Call
	examID      model.ExamID
	persistence *fakeWorkspaceStore
	authorizer  *fakeWorkspaceAuthorizer
	auditor     *fakeWorkspaceAuditor
	effects     *fakeWorkspaceEffects
	content     *fakeWorkspaceContent
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	examID := model.ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee")
	userID := model.UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu")
	exam, _ := model.NewExam(examID, model.AcademicUnitID("aaaaaaaaaaaaaaaaaaaaaaaaaa"), userID, at)
	persistence := &fakeWorkspaceStore{access: &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: true}}
	authorizer := &fakeWorkspaceAuthorizer{}
	auditor := &fakeWorkspaceAuditor{}
	effects := &fakeWorkspaceEffects{}
	content := &fakeWorkspaceContent{persistence: persistence}
	service, err := NewService(persistence, persistence, fakeMemberships{unitID: exam.AcademicUnitID}, authorizer, auditor, content, effects, fakeFailures{},
		func() time.Time { return at },
		model.NewStarterWorkspaceEntryID, model.NewStarterWorkspaceObjectID, model.NewWorkspaceContentVersion)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: userID, SessionID: model.SessionID("ssssssssssssssssssssssssss"),
		CredentialID: model.PrincipalCredentialID("cccccccccccccccccccccccccc"), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: at}
	return &serviceFixture{service: service, call: NewCall(principal, model.RequestMetadata{}),
		examID: examID, persistence: persistence, authorizer: authorizer, auditor: auditor, effects: effects, content: content}
}

type fakeWorkspaceStore struct {
	access        *store.ExamAccessSnapshot
	reservation   *store.ExamStarterWorkspaceReservation
	reservations  []model.StarterWorkspaceObjectID
	mutation      *store.ExamStarterWorkspaceMutation
	finalizeErr   error
	reclaimed     bool
	reclaimedIDs  []model.StarterWorkspaceObjectID
	committed     *store.ExamStarterWorkspaceMutationResult
	finalizeCalls int
}

func (f *fakeWorkspaceStore) Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error) {
	return f.access, nil
}
func (f *fakeWorkspaceStore) List(context.Context, model.ExamID) ([]store.ExamStarterWorkspaceItem, error) {
	return []store.ExamStarterWorkspaceItem{}, nil
}
func (f *fakeWorkspaceStore) GetFile(context.Context, model.ExamID, model.StarterWorkspaceEntryID) (*store.ExamStarterWorkspaceItem, error) {
	return nil, store.NewErrNotFound("entry", "")
}
func (f *fakeWorkspaceStore) ReserveObject(_ context.Context, input *store.ExamStarterWorkspaceReservation) (*model.StarterWorkspaceObject, error) {
	f.reservation = input
	f.reservations = append(f.reservations, input.Object.ID)
	return input.Object, nil
}
func (f *fakeWorkspaceStore) finish(input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.mutation = input
	f.finalizeCalls++
	if f.finalizeErr != nil {
		return nil, f.finalizeErr
	}
	if f.committed != nil {
		replayed := *f.committed
		replayed.Replayed = true
		return &replayed, nil
	}
	object, _ := model.NewStagedStarterWorkspaceObject(input.ObjectID, input.ExamID, input.ActorUserID, model.TimeFromMillis(input.ChangedAt-1000), model.TimeFromMillis(input.ChangedAt).Add(time.Hour))
	_ = object.MarkCurrent(input.ContentVersion, input.MediaType, input.SizeBytes, input.SHA256, model.TimeFromMillis(input.ChangedAt))
	path := input.Path
	if path == "" {
		path = "main.go"
	}
	entry, _ := model.NewStarterWorkspaceFile(input.EntryID, input.ExamID, path, input.ObjectID, model.TimeFromMillis(input.ChangedAt))
	result := &store.ExamStarterWorkspaceMutationResult{Entry: entry, Object: object, DraftRevision: input.ExpectedDraftRevision + 1}
	f.committed = result
	return result, nil
}
func (f *fakeWorkspaceStore) CreateDirectory(_ context.Context, input *store.ExamStarterWorkspaceMutation, _ *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.mutation = input
	entry, _ := model.NewStarterWorkspaceDirectory(input.EntryID, input.ExamID, input.Path, model.TimeFromMillis(input.ChangedAt))
	return &store.ExamStarterWorkspaceMutationResult{Entry: entry, DraftRevision: input.ExpectedDraftRevision + 1}, nil
}
func (f *fakeWorkspaceStore) CreateFile(_ context.Context, input *store.ExamStarterWorkspaceMutation, _ *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return f.finish(input)
}
func (f *fakeWorkspaceStore) MoveEntry(context.Context, *store.ExamStarterWorkspaceMutation, *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	panic("unexpected")
}
func (f *fakeWorkspaceStore) ReplaceFile(_ context.Context, input *store.ExamStarterWorkspaceMutation, _ *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return f.finish(input)
}
func (f *fakeWorkspaceStore) RemoveEntry(context.Context, *store.ExamStarterWorkspaceMutation, *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	panic("unexpected")
}
func (f *fakeWorkspaceStore) MarkObjectReclaimable(_ context.Context, id model.StarterWorkspaceObjectID, _ time.Time) error {
	f.reclaimed = true
	f.reclaimedIDs = append(f.reclaimedIDs, id)
	return nil
}
func (f *fakeWorkspaceStore) ClaimObjectsForCleanup(context.Context, int, string) ([]model.StarterWorkspaceObject, error) {
	return nil, nil
}
func (f *fakeWorkspaceStore) CompleteObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error {
	return nil
}
func (f *fakeWorkspaceStore) ReleaseObjectCleanup(context.Context, model.StarterWorkspaceObjectID, string) error {
	return nil
}

type fakeMemberships struct{ unitID model.AcademicUnitID }

func (f fakeMemberships) ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error) {
	return []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}, nil
}

type fakeWorkspaceAuthorizer struct{ called bool }

func (f *fakeWorkspaceAuthorizer) Authorize(context.Context, Call, model.Action, model.Resource) error {
	f.called = true
	return nil
}

type fakeWorkspaceAuditor struct{ values map[string]any }

func (f *fakeWorkspaceAuditor) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, values, _ map[string]any) (string, error) {
	f.values = values
	return "iiiiiiiiiiiiiiiiiiiiiiiiii", nil
}
func (f *fakeWorkspaceAuditor) Fail(context.Context, string, string) error { return nil }

type fakeWorkspaceEffects struct {
	operation ChangeOperation
	count     int
	examID    model.ExamID
	entryID   model.StarterWorkspaceEntryID
	revision  int64
	changedAt time.Time
}

func (f *fakeWorkspaceEffects) Changed(_ context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64, operation ChangeOperation, changedAt time.Time) error {
	f.operation = operation
	f.count++
	f.examID, f.entryID, f.revision, f.changedAt = examID, entryID, revision, changedAt
	return nil
}

type fakeFailures struct{}

func (fakeFailures) Report(context.Context, string, error) {}

type fakeWorkspaceContent struct {
	persistence          *fakeWorkspaceStore
	stagedBeforeFinalize bool
	stagedIDs            []model.StarterWorkspaceObjectID
	stageErr             error
}

func (f *fakeWorkspaceContent) StageStarterWorkspaceObject(_ context.Context, id model.StarterWorkspaceObjectID, body io.Reader, size int64, media string) (*model.StarterWorkspaceContent, error) {
	if f.stageErr != nil {
		return nil, f.stageErr
	}
	data, _ := io.ReadAll(body)
	f.stagedIDs = append(f.stagedIDs, id)
	f.stagedBeforeFinalize = f.persistence.mutation == nil
	return &model.StarterWorkspaceContent{MediaType: media, SizeBytes: int64(len(data)), SHA256: strings.Repeat("a", 64)}, nil
}
func (*fakeWorkspaceContent) OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	return nil, nil
}

func faultCode(err error) string {
	if fault, ok := err.(*Fault); ok {
		return fault.Code
	}
	return ""
}
