// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package resource

import (
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

func TestCreateStagesVerifiedBytesBeforeAuditAndAtomicVisibility(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID, UserID: f.userID}}
	got, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "  Reference  ", DescriptionMarkdown: "Read **carefully**.", MediaType: model.ExamResourceMediaMarkdown, Body: strings.NewReader("# Notes"), Size: 7, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, "Reference", got.Resource.DisplayName)
	require.False(t, got.Resource.FileEntryID.IsZero())
	require.Equal(t, got.Rendition.RevisionID, got.Resource.SelectedFileRevisionID)
	require.Equal(t, int64(2), got.DraftRevision)
	// Contract: authorization precedes the reservation, verified bytes are stored
	// inside the reservation before the audit begins (guardrail 8) and the
	// atomic finalization commits before its effect (guardrail 14).
	require.Equal(t, []string{"access", "membership", "authorize", "list", "reserve", "content.store", "audit.begin", "finalize", "effect"}, f.order)
	require.Equal(t, "Read **carefully**.", f.persistence.finalization.Resource.DescriptionMarkdown)
	require.NotEmpty(t, f.persistence.finalization.Rendition.SHA256)
	require.Equal(t, 1, f.content.storeCalls, "content storage calls")
	wantIdempotency, prepareErr := prepareResourceIdempotency(f.invocation, idempotencyOperationAddResource, "test-key",
		f.examID, 1, "", "Reference", "Read **carefully**.", model.ExamResourceMediaMarkdown, 7, strings.Repeat("a", 64), nil)
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
}

func TestCreateInvalidContentRemainsInvisibleAndUnaudited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.storeErr = testInvalidContentError{}
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaPDF, Body: strings.NewReader("bad"), Size: 3, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.resource.content_invalid", fault.Code())
	require.Nil(t, f.persistence.finalization, "partial visibility/audit: finalize")
	require.False(t, f.auditor.began, "partial visibility/audit: finalize")
}

type testCapacityError struct{}

func (testCapacityError) Error() string         { return "content capacity is unavailable" }
func (testCapacityError) WorkCapacityExceeded() {}

func TestContentCapacityRefusalRemainsInvisibleAndUnaudited(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"create", "replace"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			f.content.storeErr = testCapacityError{}
			var err error
			if operation == "create" {
				_, err = f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
			} else {
				resourceID := model.NewExamResourceID()
				f.persistence.items = []store.ExamResourceRecord{testRecord(f.examID, resourceID, model.NewFileEntryID(), model.NewFileRevisionID(), 1)}
				_, err = f.service.ReplaceExamResourceContent(context.Background(), f.invocation, ReplaceContentCommand{ExamID: f.examID, ResourceID: resourceID, ExpectedDraftRevision: 1, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
			}
			var fault *apperror.Error
			require.ErrorAs(t, err, &fault)
			require.Equal(t, "service.busy", fault.Code())
			require.ErrorIs(t, err, f.content.storeErr)
			require.Nil(t, f.persistence.finalization, "partial visibility/audit: finalize")
			require.False(t, f.auditor.began, "partial visibility/audit: finalize")
			require.Equal(t, 0, f.effects.calls, "partial visibility/audit: finalize")
		})
	}
}

func TestFinalizeReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, 0, f.effects.calls, "replay effects")
}

func TestExactCreateRetryRecoversUnknownCommitWithoutRepublishing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayAfterFirst = true
	command := CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"}
	command.Body = strings.NewReader("notes")
	first, err := f.service.CreateExamResource(context.Background(), f.invocation, command)
	require.NoError(t, err)
	// Simulate a lost success response: the caller retries the exact command
	// with the original expected Draft revision and a fresh request body.
	command.Body = strings.NewReader("notes")
	recovered, err := f.service.CreateExamResource(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, first.Resource.ID, recovered.Resource.ID, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.reserveCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.finalizeCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 1, f.effects.calls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
}

func TestExactTenthCreateRetryReachesStoredOutcome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for position := 0; position < model.ExamResourceMaximumCount-1; position++ {
		record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
		record.Resource.Position = position
		f.persistence.items = append(f.persistence.items, record)
	}
	f.persistence.replayAfterFirst = true
	command := CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Last reference", MediaType: model.ExamResourceMediaText, Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"}
	command.Body = strings.NewReader("notes")
	first, err := f.service.CreateExamResource(context.Background(), f.invocation, command)
	require.NoError(t, err)
	f.persistence.items = append(f.persistence.items, first)
	command.Body = strings.NewReader("notes")
	recovered, err := f.service.CreateExamResource(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, first.Resource.ID, recovered.Resource.ID, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.reserveCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.finalizeCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 1, f.effects.calls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
}

func TestExactReplacementRetryRecoversUnknownCommitWithoutRepublishing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	current := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	f.persistence.items = []store.ExamResourceRecord{current}
	f.persistence.replayAfterFirst = true
	command := ReplaceContentCommand{ExamID: f.examID, ResourceID: current.Resource.ID, ExpectedDraftRevision: 1, MediaType: model.ExamResourceMediaText, Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"}
	command.Body = strings.NewReader("notes")
	first, err := f.service.ReplaceExamResourceContent(context.Background(), f.invocation, command)
	require.NoError(t, err)
	// Simulate the same lost-success response as Create: the reservation is
	// disposable and Finalize recovers the durable idempotent outcome.
	command.Body = strings.NewReader("notes")
	recovered, err := f.service.ReplaceExamResourceContent(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, first.Resource.SelectedFileRevisionID, recovered.Resource.SelectedFileRevisionID, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.reserveCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 2, f.persistence.finalizeCalls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	require.Equal(t, 1, f.effects.calls, "first=%#v recovered=%#v reserve=%d finalize=%d effects", first, recovered, f.persistence.reserveCalls, f.persistence.finalizeCalls)
	wantIdempotency, prepareErr := prepareResourceIdempotency(f.invocation, idempotencyOperationReplaceResourceContent, "test-key",
		f.examID, 1, current.Resource.ID.String(), "", "", model.ExamResourceMediaText, 5, strings.Repeat("a", 64), nil)
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
}

func TestCreateMapsStorageFailureToUnavailableWithoutSafeBackendDetails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.content.storeErr = errors.New("secret backend bucket and object key")
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v fault=%#v", err, fault)
	require.Equal(t, "exam.resource.unavailable", fault.Code(), "error=%v fault=%#v", err, fault)
	require.Empty(t, fault.Fields(), "error=%v fault=%#v", err, fault)
}

func TestOpenUsesExactAuthoritativeRenditionWithoutStorageDiscovery(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	f.persistence.items = []store.ExamResourceRecord{record}
	opened, err := f.service.OpenExamResource(context.Background(), f.invocation, OpenQuery{ExamID: f.examID, ResourceID: record.Resource.ID})
	require.NoError(t, err)
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	require.Equal(t, "protected", string(body), "body=%q content=%#v", body, f.content)
	require.Equal(t, record.Resource.SelectedFileRevisionID, f.content.openedRevision, "body=%q content=%#v", body, f.content)
	require.Equal(t, record.Rendition.ID, f.content.openedRendition, "body=%q content=%#v", body, f.content)
}

func TestMetadataAndOrderNoOpsDoNotBeginAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	f.persistence.items = []store.ExamResourceRecord{record}
	_, err := f.service.EditExamResourceMetadata(context.Background(), f.invocation, EditMetadataCommand{ExamID: f.examID, ResourceID: record.Resource.ID, ExpectedDraftRevision: 1, DisplayName: examResourceString(record.Resource.DisplayName), DescriptionMarkdown: examResourceString(record.Resource.DescriptionMarkdown), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "metadata no-op error")
	require.Equal(t, "exam.resource.no_changes", fault.Code(), "metadata no-op error")
	require.False(t, f.auditor.began, "metadata no-op error")
	_, err = f.service.ReorderExamResources(context.Background(), f.invocation, ReorderCommand{ExamID: f.examID, ExpectedDraftRevision: 1, ResourceIDs: []model.ExamResourceID{record.Resource.ID}, IdempotencyKey: "test-key"})
	require.ErrorAs(t, err, &fault, "reorder no-op error")
	require.Equal(t, "exam.resource.no_changes", fault.Code(), "reorder no-op error")
	require.False(t, f.auditor.began, "reorder no-op error")
}

func TestApparentMetadataNoOpWithStaleDraftReachesAuditedStoreGuard(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 2)
	f.persistence.items = []store.ExamResourceRecord{record}
	f.access.draftRevision = 2
	f.persistence.updateErr = store.NewErrConflict("exam_draft", "exam_draft_revision", nil)

	_, err := f.service.EditExamResourceMetadata(context.Background(), f.invocation, EditMetadataCommand{ExamID: f.examID, ResourceID: record.Resource.ID, ExpectedDraftRevision: 1, DisplayName: examResourceString(record.Resource.DisplayName), DescriptionMarkdown: examResourceString(record.Resource.DescriptionMarkdown), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.draft.revision_conflict", fault.Code())
	require.True(t, f.auditor.began)
	require.Equal(t, 1, f.persistence.updateCalls)
}

func TestApparentReorderNoOpWithArchivedExamReachesAuditedStoreGuard(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	f.persistence.items = []store.ExamResourceRecord{record}
	f.access.archived = true
	f.persistence.reorderErr = store.NewErrConflict("exam", "exam_archived", nil)

	_, err := f.service.ReorderExamResources(context.Background(), f.invocation, ReorderCommand{ExamID: f.examID, ExpectedDraftRevision: 1, ResourceIDs: []model.ExamResourceID{record.Resource.ID}, IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.archived", fault.Code())
	require.True(t, f.auditor.began)
	require.Equal(t, 1, f.persistence.reorderCalls)
}

func TestReorderPublishesCommittedRevisionAndReportsTransientFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	second := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
	second.Resource.Position = 1
	f.persistence.items = []store.ExamResourceRecord{first, second}
	f.persistence.reorderResult = &store.ExamResourceCommandResult{Items: []store.ExamResourceRecord{second, first}, DraftRevision: 7}
	f.effects.changedErr = errors.New("realtime unavailable")

	items, err := f.service.ReorderExamResources(context.Background(), f.invocation, ReorderCommand{ExamID: f.examID, ExpectedDraftRevision: 1, ResourceIDs: []model.ExamResourceID{second.Resource.ID, first.Resource.ID}, IdempotencyKey: "test-key"})
	require.NoError(t, err, "items=%#v error=%v effect=%#v", items, err, f.effects)
	require.Len(t, items, 2, "items=%#v error=%v effect=%#v", items, err, f.effects)
	require.Equal(t, int64(7), f.effects.draftRevision, "items=%#v error=%v effect=%#v", items, err, f.effects)
	require.Equal(t, 1, f.effects.reportCalls, "items=%#v error=%v effect=%#v", items, err, f.effects)
	want, prepareErr := prepareResourceIdempotency(f.invocation, idempotencyOperationReorderResources, "test-key",
		f.examID, 1, "", "", "", "", 0, "", []string{second.Resource.ID.String(), first.Resource.ID.String()})
	require.NoError(t, prepareErr)
	require.Equal(t, want, f.persistence.idempotency)
}

func TestRemovePassesOwnedIdempotencyToStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	resourceID := model.NewExamResourceID()
	_, err := f.service.RemoveExamResource(context.Background(), f.invocation, RemoveCommand{ExamID: f.examID,
		ResourceID: resourceID, ExpectedDraftRevision: 1, IdempotencyKey: "remove-key"})
	require.NoError(t, err)
	want, err := prepareResourceIdempotency(f.invocation, idempotencyOperationRemoveResource, "remove-key",
		f.examID, 1, resourceID.String(), "", "", "", 0, "", nil)
	require.NoError(t, err)
	require.Equal(t, want, f.persistence.idempotency)
}

func TestMetadataPatchPreservesOmittedFieldsAndAllowsExplicitEmptyDescription(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		displayName *string
		description *string
		wantName    string
		wantDesc    string
	}{
		{name: "name only", displayName: examResourceString("Renamed"), wantName: "Renamed", wantDesc: "Existing"},
		{name: "clear description", description: examResourceString(""), wantName: "Reference", wantDesc: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			record := testRecord(f.examID, model.NewExamResourceID(), model.NewFileEntryID(), model.NewFileRevisionID(), 1)
			record.Resource.DescriptionMarkdown = "Existing"
			f.persistence.items = []store.ExamResourceRecord{record}
			f.persistence.updateResult = &store.ExamResourceCommandResult{Value: &record}
			_, err := f.service.EditExamResourceMetadata(context.Background(), f.invocation, EditMetadataCommand{ExamID: f.examID, ResourceID: record.Resource.ID, ExpectedDraftRevision: 1, DisplayName: test.displayName, DescriptionMarkdown: test.description, IdempotencyKey: "test-key"})
			require.NoError(t, err)
			require.NotNil(t, f.persistence.metadataUpdate)
			require.Equal(t, test.wantName, f.persistence.metadataUpdate.DisplayName)
			require.Equal(t, test.wantDesc, f.persistence.metadataUpdate.DescriptionMarkdown)
			want, prepareErr := prepareMetadataIdempotency(f.invocation, EditMetadataCommand{ExamID: f.examID,
				ResourceID: record.Resource.ID, ExpectedDraftRevision: 1, DisplayName: test.displayName,
				DescriptionMarkdown: test.description, IdempotencyKey: "test-key"})
			require.NoError(t, prepareErr)
			require.Equal(t, want, f.persistence.idempotency)
		})
	}
}

func TestMetadataPatchRejectsOmittedFieldsBeforeAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.EditExamResourceMetadata(context.Background(), f.invocation, EditMetadataCommand{ExamID: f.examID, ResourceID: model.NewExamResourceID(), ExpectedDraftRevision: 1, IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.resource.invalid", fault.Code())
	require.False(t, f.auditor.began)
	require.Equal(t, 0, f.persistence.updateCalls)
}

func TestInvalidMetadataIsRejectedBeforeUploadReservation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "   ", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.resource.invalid", fault.Code())
	for _, event := range f.order {
		require.NotEqual(t, "reserve", event, "invalid metadata caused %q: order=%v", event, f.order)
		require.NotEqual(t, "content.store", event, "invalid metadata caused %q: order=%v", event, f.order)
		require.NotEqual(t, "audit.begin", event, "invalid metadata caused %q: order=%v", event, f.order)
	}
}

func TestCreateUsesExplicitOverrideWhenCurrentManagerMembershipIsAbsent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.memberships.items = []*model.AcademicUnitMember{}
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamManageOverride, f.authorizer.action, "action")
	require.NotNil(t, f.persistence.reservation, "action")
	require.True(t, f.persistence.reservation.ManagerOverride, "action")
}

func TestCreateStopsWhenCurrentManagerMembershipCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	failure := errors.New("membership unavailable")
	f.memberships.err = failure
	_, err := f.service.CreateExamResource(context.Background(), f.invocation, CreateCommand{ExamID: f.examID, ExpectedDraftRevision: 1, DisplayName: "Reference", MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.resource.unavailable", fault.Code())
	require.ErrorIs(t, err, failure)
	// Without current membership nothing is authorized or reserved.
	require.Empty(t, f.authorizer.action, "authorized without current membership")
	require.Nil(t, f.persistence.reservation, "reserved without current membership")
}

type fixture struct {
	service     *Service
	invocation  invocation.Invocation
	examID      model.ExamID
	unitID      model.AcademicUnitID
	userID      model.UserID
	order       []string
	persistence *storeFake
	access      *accessFake
	memberships *membershipFake
	authorizer  *authFake
	auditor     *auditFake
	effects     *effectFake
	content     *contentFake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{examID: model.NewExamID(), unitID: model.NewAcademicUnitID(), userID: model.NewUserID()}
	f.persistence = &storeFake{f: f}
	f.access = &accessFake{f: f, draftRevision: 1}
	f.memberships = &membershipFake{f: f}
	f.auditor = &auditFake{f: f}
	f.effects = &effectFake{f: f}
	f.content = &contentFake{f: f}
	f.authorizer = &authFake{f: f}
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	service, err := New(Dependencies{
		Persistence:    f.persistence,
		Access:         f.access,
		Memberships:    f.memberships,
		Authorizer:     f.authorizer,
		Auditor:        f.auditor,
		Effects:        f.effects,
		EffectFailures: f.effects,
		Content:        f.content,
		Now:            func() time.Time { return at },
		NewResourceID:  model.NewExamResourceID,
		NewEntryID:     model.NewFileEntryID,
		NewRevisionID:  model.NewFileRevisionID,
		NewLeaseID:     model.NewUploadLeaseID,
	})
	require.NoError(t, err)
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: at}, model.RequestMetadata{})
	return f
}

type storeFake struct {
	f                *fixture
	items            []store.ExamResourceRecord
	finalization     *store.ExamResourceUploadFinalization
	reservation      *store.ExamResourceUploadReservation
	replayed         bool
	updateCalls      int
	reorderCalls     int
	updateErr        error
	reorderErr       error
	reorderResult    *store.ExamResourceCommandResult
	metadataUpdate   *store.ExamResourceMetadataUpdate
	updateResult     *store.ExamResourceCommandResult
	reserveCalls     int
	finalizeCalls    int
	replayAfterFirst bool
	committed        *store.ExamResourceRecord
	idempotency      *store.CommandIdempotency
}

func (s *storeFake) List(context.Context, model.ExamID) ([]store.ExamResourceRecord, error) {
	s.f.order = append(s.f.order, "list")
	return append([]store.ExamResourceRecord(nil), s.items...), nil
}
func (s *storeFake) Get(_ context.Context, _ model.ExamID, id model.ExamResourceID) (*store.ExamResourceRecord, error) {
	for i := range s.items {
		if s.items[i].Resource.ID == id {
			return &s.items[i], nil
		}
	}
	return nil, store.NewErrNotFound("exam_resource", id.String())
}
func (s *storeFake) ReserveUpload(_ context.Context, in *store.ExamResourceUploadReservation) (*store.FileUpload, error) {
	s.f.order = append(s.f.order, "reserve")
	s.reserveCalls++
	s.reservation = in
	return &store.FileUpload{Entry: in.Entry, Revision: in.Revision, Lease: in.Lease}, nil
}
func (s *storeFake) FinalizeUpload(_ context.Context, in *store.ExamResourceUploadFinalization, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	s.f.order = append(s.f.order, "finalize")
	s.finalizeCalls++
	s.finalization, s.idempotency = in, command
	if s.replayAfterFirst && s.committed != nil {
		return &store.ExamResourceCommandResult{Value: s.committed, Replayed: true}, nil
	}
	record := store.ExamResourceRecord{Resource: in.Resource, Rendition: in.Rendition, DraftRevision: in.ExpectedDraftRevision + 1}
	if s.replayAfterFirst {
		s.committed = &record
	}
	return &store.ExamResourceCommandResult{Value: &record, Replayed: s.replayed}, nil
}
func (s *storeFake) UpdateMetadata(_ context.Context, input *store.ExamResourceMetadataUpdate, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	s.updateCalls++
	s.metadataUpdate, s.idempotency = input, command
	return s.updateResult, s.updateErr
}
func (s *storeFake) Reorder(_ context.Context, _ *store.ExamResourceReorder, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	s.reorderCalls++
	s.idempotency = command
	return s.reorderResult, s.reorderErr
}

type accessFake struct {
	f             *fixture
	draftRevision int64
	archived      bool
}

func (a *accessFake) Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error) {
	a.f.order = append(a.f.order, "access")
	exam, _ := model.NewExam(a.f.examID, a.f.unitID, a.f.userID, time.Now().UTC())
	if a.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(exam.UpdatedAt)
	}
	return &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: true}, nil
}

func (a *accessFake) Get(context.Context, model.ExamID, model.UserID) (*store.ExamAuthoringSnapshot, error) {
	at := time.Now().UTC()
	exam, _ := model.NewExam(a.f.examID, a.f.unitID, a.f.userID, at)
	if a.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(at)
	}
	draft, _ := model.NewExamDraft(a.f.examID, "Exam", "", model.DefaultExamPolicySet(), at)
	draft.Revision = a.draftRevision
	return &store.ExamAuthoringSnapshot{Exam: exam, Draft: draft, ActorIsManager: true}, nil
}
func (s *storeFake) Remove(_ context.Context, input *store.ExamResourceRemoval, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	s.idempotency = command
	record := testRecord(input.ExamID, input.ResourceID, model.NewFileEntryID(), model.NewFileRevisionID(), input.ExpectedDraftRevision+1)
	return &store.ExamResourceCommandResult{Value: &record, DraftRevision: input.ExpectedDraftRevision + 1}, nil
}

type membershipFake struct {
	f     *fixture
	items []*model.AcademicUnitMember
	err   error
}

func (m *membershipFake) ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.AcademicUnitMember, error) {
	m.f.order = append(m.f.order, "membership")
	if m.items == nil {
		return []*model.AcademicUnitMember{{AcademicUnitID: m.f.unitID, UserID: m.f.userID}}, m.err
	}
	return m.items, m.err
}

type authFake struct {
	f      *fixture
	action model.Action
}

func (a *authFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, _ model.Resource) error {
	a.f.order = append(a.f.order, "authorize")
	a.action = action
	return nil
}

type auditFake struct {
	f     *fixture
	began bool
}

func (a *auditFake) BeginAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error) {
	a.f.order = append(a.f.order, "audit.begin")
	a.began = true
	return model.NewAuditEventID(), nil
}
func (a *auditFake) Fail(context.Context, model.AuditEventID, string) error {
	a.f.order = append(a.f.order, "audit.fail")
	return nil
}

type effectFake struct {
	f             *fixture
	calls         int
	draftRevision int64
	changedErr    error
	reportCalls   int
}

func (e *effectFake) Changed(_ context.Context, _ model.ExamID, _ model.ExamResourceID, draftRevision int64, _ string) error {
	e.f.order = append(e.f.order, "effect")
	e.calls++
	e.draftRevision = draftRevision
	return e.changedErr
}
func (e *effectFake) Report(context.Context, string, error) { e.reportCalls++ }

type contentFake struct {
	f               *fixture
	storeCalls      int
	storeErr        error
	openedRevision  model.FileRevisionID
	openedRendition model.FileRenditionID
}

func (c *contentFake) StoreExamResource(_ context.Context, revision model.FileRevisionID, media model.ExamResourceMediaType, _ io.Reader, size int64, at time.Time) (model.FileRendition, error) {
	c.f.order = append(c.f.order, "content.store")
	c.storeCalls++
	if c.storeErr != nil {
		return model.FileRendition{}, c.storeErr
	}
	r, _ := model.NewFileRendition(model.NewFileRenditionID(), revision, "original", string(media), size, 0, 0, strings.Repeat("a", 64), at)
	return *r, nil
}
func (c *contentFake) OpenExamResource(_ context.Context, revision model.FileRevisionID, rendition model.FileRenditionID) (io.ReadCloser, error) {
	c.openedRevision, c.openedRendition = revision, rendition
	return io.NopCloser(strings.NewReader("protected")), nil
}
func (c *contentFake) RemoveExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) error {
	return nil
}
func testRecord(examID model.ExamID, resourceID model.ExamResourceID, entryID model.FileEntryID, revisionID model.FileRevisionID, draftRevision int64) store.ExamResourceRecord {
	at := time.Now().UTC()
	resource, _ := model.NewExamResource(resourceID, examID, entryID, revisionID, "Reference", "", 0, at)
	rendition, _ := model.NewFileRendition(model.NewFileRenditionID(), revisionID, "original", "text/plain", 9, 0, 0, strings.Repeat("a", 64), at)
	return store.ExamResourceRecord{Resource: resource, Rendition: rendition, DraftRevision: draftRevision}
}

func examResourceString(value string) *string { return &value }

type testInvalidContentError struct{}

func (testInvalidContentError) Error() string               { return "invalid content" }
func (testInvalidContentError) InvalidExamResourceContent() {}
