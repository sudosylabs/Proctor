// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestServiceCreatesAFileOnlyAfterOpaqueContentAndDurableFinalize(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	body := []byte("package main\n")
	result, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, CreateFileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "cmd/main.go", MediaType: "text/x-go",
		ExpectedSHA256: strings.Repeat("a", 64), Body: bytes.NewReader(body), Size: int64(len(body)), IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.Equal(t, "cmd/main.go", result.Entry.Path, "result/store = %#v/%#v", result, fixture.persistence)
	require.Equal(t, int64(2), result.DraftRevision, "result/store = %#v/%#v", result, fixture.persistence)
	require.NotNil(t, fixture.persistence.reservation, "result/store = %#v/%#v", result, fixture.persistence)
	require.NotNil(t, fixture.persistence.mutation, "result/store = %#v/%#v", result, fixture.persistence)
	require.True(t, fixture.content.stagedBeforeFinalize, "effect/content ordering = %#v/%#v", fixture.effects, fixture.content)
	require.Equal(t, "file_created", string(fixture.effects.operation), "effect/content ordering = %#v/%#v", fixture.effects, fixture.content)
	require.Equal(t, fixture.examID, fixture.effects.examID)
	require.Equal(t, result.Entry.ID, fixture.effects.entryID)
	require.Equal(t, int64(2), fixture.effects.revision)
	require.Equal(t, time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC), fixture.effects.changedAt)
	require.Nil(t, fixture.auditor.values["path"])
	require.Nil(t, fixture.auditor.values["sha256"])
	wantIdempotency, prepareErr := prepareWorkspaceIdempotency(fixture.invocation, idempotencyOperationCreateFile,
		"test-key", fixture.examID, 1, "", "", "cmd/main.go", "text/x-go", int64(len(body)), strings.Repeat("a", 64))
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, fixture.persistence.idempotency)
}

func TestFileFinalizationPreservesReservationMicroseconds(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	at := time.Date(2026, 8, 14, 10, 0, 0, 123456789, time.UTC)
	fixture.service.deps.Now = func() time.Time { return at }
	_, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, CreateFileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "empty.txt", MediaType: "text/plain",
		ExpectedSHA256: strings.Repeat("a", 64), Body: bytes.NewReader(nil), Size: 0, IdempotencyKey: "microsecond-file",
	})
	require.NoError(t, err)
	want := model.TimeUTC(at)
	require.True(t, fixture.persistence.reservation.Object.CreatedAt.Equal(want), "reservation, finalization, and publication must preserve the same microsecond instant")
	require.True(t, fixture.persistence.mutation.ChangedAt.Equal(want), "reservation, finalization, and publication must preserve the same microsecond instant")
	require.True(t, fixture.effects.changedAt.Equal(want), "reservation, finalization, and publication must preserve the same microsecond instant")
}

func TestServiceStopsWhenCurrentManagerMembershipCannotBeRead(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	failure := errors.New("membership unavailable")
	fixture.service.deps.Memberships = fakeMemberships{err: failure}
	_, err := fixture.service.CreateExamStarterWorkspaceDirectory(context.Background(), fixture.invocation, CreateDirectoryCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "src", IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.starter_workspace.unavailable", faultCode(err))
	require.ErrorIs(t, err, failure)
	require.False(t, fixture.authorizer.called, "membership failure continued: authorizer=%#v audit=%#v mutation", fixture.authorizer, fixture.auditor)
	require.Nil(t, fixture.auditor.values, "membership failure continued: authorizer=%#v audit=%#v mutation", fixture.authorizer, fixture.auditor)
	require.Nil(t, fixture.persistence.mutation, "membership failure continued: authorizer=%#v audit=%#v mutation", fixture.authorizer, fixture.auditor)
}

func TestServiceDoesNotDeleteOrReclaimAStagedObjectAfterUnknownFinalize(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.persistence.finalizeErr = context.DeadlineExceeded
	_, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, CreateFileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/x-go",
		ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1, IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.starter_workspace.unavailable", faultCode(err), "error/reclaimed")
	require.False(t, fixture.persistence.reclaimed, "error/reclaimed")
}

func TestServiceExactCreateFileRetryReturnsCommittedOutcomeWithoutRepublishing(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	command := CreateFileCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/plain",
		ExpectedSHA256: strings.Repeat("a", 64), Size: 1, IdempotencyKey: "test-key"}
	command.Body = strings.NewReader("x")
	first, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	command.Body = strings.NewReader("x")
	replayed, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.Entry.ID, replayed.Entry.ID, "first=%#v replayed=%#v", first, replayed)
	require.NotNil(t, replayed.Object, "first=%#v replayed=%#v", first, replayed)
	require.NotNil(t, first.Object, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.Object.ID, replayed.Object.ID, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.DraftRevision, replayed.DraftRevision, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, 2, fixture.persistence.finalizeCalls, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Len(t, fixture.persistence.reservations, 2, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Len(t, fixture.content.stagedIDs, 2, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Len(t, fixture.persistence.reclaimedIDs, 1, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Equal(t, fixture.content.stagedIDs[1], fixture.persistence.reclaimedIDs[0], "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Equal(t, 1, fixture.effects.count, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
}

func TestServiceExactReplaceFileRetryReturnsCommittedOutcomeWithoutRepublishing(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	command := ReplaceFileCommand{ExamID: fixture.examID, EntryID: model.NewStarterWorkspaceEntryID(), ExpectedDraftRevision: 1,
		ExpectedContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/plain", ExpectedSHA256: strings.Repeat("a", 64), Size: 1,
		IdempotencyKey: "test-key"}
	command.Body = strings.NewReader("x")
	first, err := fixture.service.ReplaceExamStarterWorkspaceFile(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	command.Body = strings.NewReader("x")
	replayed, err := fixture.service.ReplaceExamStarterWorkspaceFile(context.Background(), fixture.invocation, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.Entry.ID, replayed.Entry.ID, "first=%#v replayed=%#v", first, replayed)
	require.NotNil(t, replayed.Object, "first=%#v replayed=%#v", first, replayed)
	require.NotNil(t, first.Object, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.Object.ID, replayed.Object.ID, "first=%#v replayed=%#v", first, replayed)
	require.Equal(t, first.DraftRevision, replayed.DraftRevision, "first=%#v replayed=%#v", first, replayed)
	require.Len(t, fixture.persistence.reclaimedIDs, 1, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Equal(t, fixture.content.stagedIDs[1], fixture.persistence.reclaimedIDs[0], "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
	require.Equal(t, 1, fixture.effects.count, "store=%#v content=%#v effects=%#v", fixture.persistence, fixture.content, fixture.effects)
}

func TestServiceReplaceRequiresAndCarriesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	expected := model.WorkspaceContentVersion("wwwwwwwwwwwwwwwwwwwwwwwwww")
	result, err := fixture.service.ReplaceExamStarterWorkspaceFile(context.Background(), fixture.invocation, ReplaceFileCommand{
		ExamID: fixture.examID, EntryID: model.StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), ExpectedDraftRevision: 1,
		ExpectedContentVersion: expected, MediaType: "text/plain", ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
		IdempotencyKey: "test-key",
	})
	require.NoError(t, err, "result=%#v err=%v mutation", result, err)
	require.Equal(t, int64(2), result.DraftRevision, "result=%#v err=%v mutation", result, err)
	require.NotNil(t, fixture.persistence.mutation, "result=%#v err=%v mutation", result, err)
	require.Equal(t, expected, fixture.persistence.mutation.ExpectedContentVersion, "result=%#v err=%v mutation", result, err)
	wantIdempotency, prepareErr := prepareWorkspaceIdempotency(fixture.invocation, idempotencyOperationReplaceFile,
		"test-key", fixture.examID, 1, expected, "dddddddddddddddddddddddddd", "", "text/plain", 1, strings.Repeat("a", 64))
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, fixture.persistence.idempotency)
}

func TestMetadataWorkspaceMutationsPassOwnedIdempotencyToStore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation string
		invoke    func(*serviceFixture, model.StarterWorkspaceEntryID) error
		path      string
	}{
		{name: "create directory", operation: idempotencyOperationCreateDirectory, path: "src", invoke: func(f *serviceFixture, _ model.StarterWorkspaceEntryID) error {
			_, err := f.service.CreateExamStarterWorkspaceDirectory(context.Background(), f.invocation, CreateDirectoryCommand{ExamID: f.examID,
				ExpectedDraftRevision: 1, Path: "src", IdempotencyKey: "test-key"})
			return err
		}},
		{name: "move entry", operation: idempotencyOperationMoveEntry, path: "src/main.go", invoke: func(f *serviceFixture, id model.StarterWorkspaceEntryID) error {
			_, err := f.service.MoveExamStarterWorkspaceEntry(context.Background(), f.invocation, MoveEntryCommand{ExamID: f.examID, EntryID: id,
				ExpectedDraftRevision: 1, Path: "src/main.go", IdempotencyKey: "test-key"})
			return err
		}},
		{name: "remove entry", operation: idempotencyOperationRemoveEntry, invoke: func(f *serviceFixture, id model.StarterWorkspaceEntryID) error {
			_, err := f.service.RemoveExamStarterWorkspaceEntry(context.Background(), f.invocation, RemoveEntryCommand{ExamID: f.examID, EntryID: id,
				ExpectedDraftRevision: 1, IdempotencyKey: "test-key"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			entryID := model.NewStarterWorkspaceEntryID()
			require.NoError(t, test.invoke(fixture, entryID))
			semanticEntryID := ""
			if test.operation != idempotencyOperationCreateDirectory {
				semanticEntryID = entryID.String()
			}
			want, err := prepareWorkspaceIdempotency(fixture.invocation, test.operation, "test-key", fixture.examID,
				1, "", semanticEntryID, test.path, "", 0, "")
			require.NoError(t, err)
			require.Equal(t, want, fixture.persistence.idempotency)
		})
	}
}

func TestServiceMapsStaleContentVersionAndReclaimsNewObject(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.persistence.finalizeErr = store.NewErrConflict("exam_starter_workspace_entry", "workspace_content_version", nil)
	_, err := fixture.service.ReplaceExamStarterWorkspaceFile(context.Background(), fixture.invocation, ReplaceFileCommand{
		ExamID: fixture.examID, EntryID: model.StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), ExpectedDraftRevision: 1,
		ExpectedContentVersion: model.WorkspaceContentVersion("wwwwwwwwwwwwwwwwwwwwwwwwww"), MediaType: "text/plain",
		ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
		IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.starter_workspace.content_conflict", faultCode(err))
	require.True(t, fixture.persistence.reclaimed)
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
			_, err := fixture.service.CreateExamStarterWorkspaceFile(context.Background(), fixture.invocation, CreateFileCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "main.go", MediaType: "text/plain",
				ExpectedSHA256: strings.Repeat("a", 64), Body: strings.NewReader("x"), Size: 1,
				IdempotencyKey: "test-key",
			})
			require.Equal(t, test.code, faultCode(err))
			require.True(t, fixture.persistence.reclaimed)
		})
	}
}

type fakeInvalidWorkspaceContentError struct{}

func (fakeInvalidWorkspaceContentError) Error() string                        { return "invalid content" }
func (fakeInvalidWorkspaceContentError) InvalidStarterWorkspaceContent() bool { return true }

func TestServiceRejectsInvalidPathsBeforeAuthorizationOrStorage(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	_, err := fixture.service.CreateExamStarterWorkspaceDirectory(context.Background(), fixture.invocation, CreateDirectoryCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Path: "../escape",
		IdempotencyKey: "test-key",
	})
	require.Equal(t, "exam.starter_workspace.invalid", faultCode(err), "error/auth/store")
	require.False(t, fixture.authorizer.called, "error/auth/store")
	require.Nil(t, fixture.persistence.mutation, "error/auth/store")
}

type serviceFixture struct {
	service     *Service
	invocation  invocation.Invocation
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
	service, err := New(Dependencies{
		Persistence:    persistence,
		Access:         persistence,
		Memberships:    fakeMemberships{unitID: exam.AcademicUnitID},
		Authorizer:     authorizer,
		Auditor:        auditor,
		Content:        content,
		Effects:        effects,
		EffectFailures: fakeFailures{},
		Now:            func() time.Time { return at },
		NewEntryID:     model.NewStarterWorkspaceEntryID,
		NewObjectID:    model.NewStarterWorkspaceObjectID,
		NewVersion:     model.NewWorkspaceContentVersion,
	})
	require.NoError(t, err)
	principal := model.Principal{UserID: userID, SessionID: model.SessionID("ssssssssssssssssssssssssss"),
		CredentialID: model.PrincipalCredentialID("cccccccccccccccccccccccccc"), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: at}
	return &serviceFixture{service: service, invocation: invocation.New(principal, model.RequestMetadata{}),
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
	idempotency   *store.CommandIdempotency
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
	object, _ := model.NewStagedStarterWorkspaceObject(input.ObjectID, input.ExamID, input.ActorUserID, input.ChangedAt.Add(-time.Second), input.ChangedAt.Add(time.Hour))
	_ = object.MarkCurrent(input.ContentVersion, input.MediaType, input.SizeBytes, input.SHA256, input.ChangedAt)
	path := input.Path
	if path == "" {
		path = "main.go"
	}
	entry, _ := model.NewStarterWorkspaceFile(input.EntryID, input.ExamID, path, input.ObjectID, input.ChangedAt)
	result := &store.ExamStarterWorkspaceMutationResult{Entry: entry, Object: object, DraftRevision: input.ExpectedDraftRevision + 1}
	f.committed = result
	return result, nil
}
func (f *fakeWorkspaceStore) CreateDirectory(_ context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.mutation, f.idempotency = input, command
	entry, _ := model.NewStarterWorkspaceDirectory(input.EntryID, input.ExamID, input.Path, input.ChangedAt)
	return &store.ExamStarterWorkspaceMutationResult{Entry: entry, DraftRevision: input.ExpectedDraftRevision + 1}, nil
}
func (f *fakeWorkspaceStore) CreateFile(_ context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.idempotency = command
	return f.finish(input)
}
func (f *fakeWorkspaceStore) MoveEntry(_ context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.mutation, f.idempotency = input, command
	entry, _ := model.NewStarterWorkspaceDirectory(input.EntryID, input.ExamID, input.Path, input.ChangedAt)
	return &store.ExamStarterWorkspaceMutationResult{Entry: entry, DraftRevision: input.ExpectedDraftRevision + 1}, nil
}
func (f *fakeWorkspaceStore) ReplaceFile(_ context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.idempotency = command
	return f.finish(input)
}
func (f *fakeWorkspaceStore) RemoveEntry(_ context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	f.mutation, f.idempotency = input, command
	entry, _ := model.NewStarterWorkspaceDirectory(input.EntryID, input.ExamID, "removed", input.ChangedAt)
	return &store.ExamStarterWorkspaceMutationResult{Entry: entry, DraftRevision: input.ExpectedDraftRevision + 1}, nil
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

type fakeMemberships struct {
	unitID model.AcademicUnitID
	err    error
}

func (f fakeMemberships) ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.AcademicUnitMember, error) {
	return []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}, f.err
}

type fakeWorkspaceAuthorizer struct{ called bool }

func (f *fakeWorkspaceAuthorizer) Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error {
	f.called = true
	return nil
}

type fakeWorkspaceAuditor struct{ values map[string]any }

func (f *fakeWorkspaceAuditor) BeginAtScope(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, values, _ map[string]any) (model.AuditEventID, error) {
	f.values = values
	return "iiiiiiiiiiiiiiiiiiiiiiiiii", nil
}
func (f *fakeWorkspaceAuditor) Fail(context.Context, model.AuditEventID, string) error { return nil }

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
	if fault, ok := apperror.As(err); ok {
		return fault.Code()
	}
	return ""
}
