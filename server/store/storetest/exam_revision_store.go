// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamRevisionStore(t *testing.T, ss store.Store) {
	testExamRevisionStore(t, ss, nil)
}

type ExamRevisionRetentionFixture struct {
	RevisionID             model.ExamRevisionID
	ResourceID             model.ExamResourceID
	OldResourceEntryID     model.FileEntryID
	OldResourceRevisionID  model.FileRevisionID
	OldResourceRenditionID model.FileRenditionID
	OldWorkspaceObjectID   model.StarterWorkspaceObjectID
	ExamID                 model.ExamID
	ActorUserID            model.UserID
	Past                   time.Time
}

type ExamRevisionRetentionProbe struct {
	StageOriginal func(*testing.T, context.Context, store.Store, ExamRevisionRetentionFixture)
	Verify        func(*testing.T, context.Context, store.Store, ExamRevisionRetentionFixture)
}

// TestExamRevisionStoreWithRetention runs conformance around a VFS-aware
// probe. It stages original bytes before publication, then verifies them only
// after the Draft replacements and cleanup eligibility changes have occurred.
func TestExamRevisionStoreWithRetention(t *testing.T, ss store.Store, probe ExamRevisionRetentionProbe) {
	testExamRevisionStore(t, ss, &probe)
}

func testExamRevisionStore(t *testing.T, ss store.Store, probe *ExamRevisionRetentionProbe) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "revision-unit")
	creator := saveUser(t, ctx, ss)
	at := model.NowUTC()
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "revision-exam")
	examID := created.Value.Exam.ID

	resource := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Reference", 0, 1, at.Add(time.Second), model.ExamResourceMediaText, nil)
	resourceResult, err := ss.ExamResource().FinalizeUpload(ctx, resource.finalization, examCommand(creator.ID, "exam.resource.add.v1", "revision-resource", "revision-resource-command"))
	require.NoError(t, err)
	directoryID := model.NewStarterWorkspaceEntryID()
	directory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 2, directoryID, "cmd", at.Add(2*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, directory, examCommand(creator.ID, "exam.starter_workspace.directory.create.v1", "revision-directory", "revision-directory-command"))
	require.NoError(t, err)
	fileID := model.NewStarterWorkspaceEntryID()
	file := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, fileID, "cmd/main.go", at.Add(3*time.Second), 12)
	fileResult, err := ss.ExamStarterWorkspace().CreateFile(ctx, file, examCommand(creator.ID, "exam.starter_workspace.file.create.v1", "revision-file", "revision-file-command"))
	require.NoError(t, err)
	retentionFixture := ExamRevisionRetentionFixture{OldResourceRevisionID: resourceResult.Value.Resource.SelectedFileRevisionID,
		OldResourceRenditionID: resourceResult.Value.Rendition.ID, OldWorkspaceObjectID: fileResult.Object.ID,
		ExamID: examID, ActorUserID: creator.ID, Past: at}
	if probe != nil && probe.StageOriginal != nil {
		probe.StageOriginal(t, ctx, ss, retentionFixture)
	}

	// A failure at the final audit step occurs after the transaction has built
	// the snapshot and attempted both aggregate fences. Nothing may escape.
	rollbackInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(3500*time.Millisecond))
	rollbackInput.AuditEventID = model.NewAuditEventID()
	_, err = ss.ExamRevision().Publish(ctx, rollbackInput, examCommand(creator.ID, "exam.revision.publish.v1", "revision-rollback", "revision-rollback-command"))
	require.Error(t, err, "publication with a missing audit attempt committed")
	rolledBack, err := ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, Limit: 10})
	require.NoError(t, err)
	authoringAfterRollback, authoringErr := ss.ExamAuthoring().Get(ctx, examID, creator.ID)
	require.NoError(t, authoringErr)
	require.Empty(t, rolledBack, "failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	require.Equal(t, int64(1), authoringAfterRollback.Exam.Revision, "failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	require.Equal(t, int64(4), authoringAfterRollback.Draft.Revision, "failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	require.True(t, authoringAfterRollback.Exam.DefaultRevisionID.IsZero(), "failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	require.True(t, authoringAfterRollback.Draft.BaseRevisionID.IsZero(), "failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	capacity := model.DefaultExamCapacityPolicy()
	capacity.WorkspaceMaximumEntries = 1
	institution.ExamCapacity = capacity
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	capacityInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(3750*time.Millisecond))
	_, err = ss.ExamRevision().Publish(ctx, capacityInput,
		examCommand(creator.ID, "exam.revision.publish.v1", "revision-capacity", "revision-capacity-command"))
	var capacityConflict *store.ErrConflict
	require.ErrorAs(t, err, &capacityConflict)
	require.Equal(t, "exam_revision_capacity", capacityConflict.Constraint)
	institution.ExamCapacity = model.DefaultExamCapacityPolicy()
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)

	firstInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(4*time.Second))
	firstCommand := examCommand(creator.ID, "exam.revision.publish.v1", "revision-publish", "revision-publish-command")
	first, err := ss.ExamRevision().Publish(ctx, firstInput, firstCommand)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	require.Equal(t, int64(1), first.Revision.Number)
	require.Equal(t, int64(4), first.Revision.SourceDraftRevision)
	require.Equal(t, int64(5), first.DraftRevision)
	require.Equal(t, int64(2), first.ExamRevision)
	require.Empty(t, first.Revision.BaseRevisionID)
	firstSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, first.Revision.ID)
	require.NoError(t, err)
	require.Len(t, firstSnapshot.Resources, 1)
	require.Equal(t, resourceResult.Value.Resource.SelectedFileRevisionID, firstSnapshot.Resources[0].FileRevisionID)
	require.Len(t, firstSnapshot.StarterWorkspace, 2)
	require.Equal(t, fileResult.Object.ID, firstSnapshot.StarterWorkspace[1].ObjectID)
	require.Equal(t, model.DefaultExamCapacityPolicy(), firstSnapshot.Capacity)
	retentionFixture.RevisionID = first.Revision.ID
	retentionFixture.ResourceID = firstSnapshot.Resources[0].ResourceID
	retentionFixture.OldResourceEntryID = firstSnapshot.Resources[0].FileEntryID

	// A lost success response retries with the original Draft fence and a new
	// generated Revision ID. The stored outcome must win before stale/no-change.
	retry := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(5*time.Second))
	replayed, err := ss.ExamRevision().Publish(ctx, retry, firstCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.Revision.ID, replayed.Revision.ID)
	require.Equal(t, int64(5), replayed.DraftRevision)
	require.Equal(t, int64(2), replayed.ExamRevision)

	unchanged := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 5, at.Add(6*time.Second))
	_, err = ss.ExamRevision().Publish(ctx, unchanged, examCommand(creator.ID, "exam.revision.publish.v1", "revision-no-change", "revision-no-change-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "exam_revision_no_changes", conflict.Constraint)

	// Change both content families after publication. The first Revision must
	// keep its exact old bytes while the second freezes the new selections.
	alteredCapacity := model.DefaultExamCapacityPolicy()
	alteredCapacity.ResourceMaximumCount = 20
	alteredCapacity.WorkspaceMaximumEntries = 750
	institution.ExamCapacity = alteredCapacity
	_, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	replacement := reserveExamResource(t, ctx, ss, examID, creator.ID, resourceResult.Value.Resource.ID, "Reference", 0, 5, at.Add(7*time.Second), model.ExamResourceMediaText, resourceResult.Value)
	replacedResource, err := ss.ExamResource().FinalizeUpload(ctx, replacement.finalization, examCommand(creator.ID, "exam.resource.replace.v1", "revision-resource-replace", "revision-resource-replace-command"))
	require.NoError(t, err)
	workspaceReplacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 6, fileID, "", at.Add(8*time.Second), 14)
	workspaceReplacement.ExpectedContentVersion = fileResult.Object.ContentVersion
	replacedWorkspace, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, workspaceReplacement, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "revision-workspace-replace", "revision-workspace-replace-command"))
	require.NoError(t, err)
	secondInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 7, at.Add(9*time.Second))
	second, err := ss.ExamRevision().Publish(ctx, secondInput, examCommand(creator.ID, "exam.revision.publish.v1", "revision-publish-second", "revision-publish-second-command"))
	require.NoError(t, err)
	secondSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, second.Revision.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Revision.Number)
	require.Equal(t, first.Revision.ID, second.Revision.BaseRevisionID)
	require.Equal(t, alteredCapacity, secondSnapshot.Capacity)
	require.Equal(t, replacedResource.Value.Resource.SelectedFileRevisionID, secondSnapshot.Resources[0].FileRevisionID)
	require.Equal(t, replacedWorkspace.Object.ID, secondSnapshot.StarterWorkspace[1].ObjectID)
	retained, err := ss.ExamRevision().GetSnapshot(ctx, examID, first.Revision.ID)
	require.NoError(t, err)
	require.Equal(t, model.DefaultExamCapacityPolicy(), retained.Capacity)
	require.Equal(t, resourceResult.Value.Resource.SelectedFileRevisionID, retained.Resources[0].FileRevisionID)
	require.Equal(t, fileResult.Object.ID, retained.StarterWorkspace[1].ObjectID)
	summary, err := ss.ExamRevision().GetSummary(ctx, examID, first.Revision.ID)
	require.NoError(t, err)
	require.Equal(t, 1, summary.ResourceCount)
	require.Equal(t, 2, summary.StarterWorkspaceEntries)
	require.Equal(t, first.Revision.PolicyDigest, summary.PolicyDigest)
	listed, err := ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, second.Revision.ID, listed[0].ID)
	listed, err = ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, BeforeNumber: listed[0].Number, BeforeRevisionID: listed[0].ID, Limit: 200})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, first.Revision.ID, listed[0].ID)

	// The Exam/Draft lock serializes two fresh commands against the same fence:
	// exactly one publishes and the loser observes the advanced Draft.
	thirdReplacement := reserveExamResource(t, ctx, ss, examID, creator.ID, resourceResult.Value.Resource.ID, "Reference", 0, 8, at.Add(10*time.Second), model.ExamResourceMediaText, replacedResource.Value)
	thirdResource, err := ss.ExamResource().FinalizeUpload(ctx, thirdReplacement.finalization, examCommand(creator.ID, "exam.resource.replace.v1", "revision-race-replace", "revision-race-replace-command"))
	require.NoError(t, err)
	inputs := []*store.ExamRevisionPublication{
		examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 9, at.Add(11*time.Second)),
		examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 9, at.Add(12*time.Second)),
	}
	commands := []*store.CommandIdempotency{
		examCommand(creator.ID, "exam.revision.publish.v1", "revision-race-a", "revision-race-command-a"),
		examCommand(creator.ID, "exam.revision.publish.v1", "revision-race-b", "revision-race-command-b"),
	}
	errs := make([]error, len(inputs))
	results := make([]*store.ExamRevisionPublicationResult, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = ss.ExamRevision().Publish(ctx, inputs[index], commands[index])
		}(index)
	}
	wait.Wait()
	var successes, stale int
	for index := range inputs {
		if errs[index] == nil && results[index] != nil {
			successes++
			continue
		}
		var conflict *store.ErrConflict
		if errors.As(errs[index], &conflict) && conflict.Constraint == "exam_draft_revision" {
			stale++
		}
	}
	require.Equal(t, 1, successes, "concurrent publications successes=%d stale=%d errors=%v", successes, stale, errs)
	require.Equal(t, 1, stale, "concurrent publications successes=%d stale=%d errors=%v", successes, stale, errs)

	// Publication and each authoring family share the same Exam/Draft lock. A
	// race must produce one complete before-or-after state, never a mixed one.
	raceTitle := "Revision race title"
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, newExamDraftTextUpdate(t, ctx, ss, examID, creator.ID, 10, &raceTitle, nil, at.Add(13*time.Second)), examCommand(creator.ID, "exam.draft.text.edit.v1", "revision-race-title", "revision-race-title-command"))
	require.NoError(t, err)
	fourthReplacement := reserveExamResource(t, ctx, ss, examID, creator.ID, resourceResult.Value.Resource.ID, "Reference", 0, 11, at.Add(14*time.Second), model.ExamResourceMediaText, thirdResource.Value)
	resourceRacePublication := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 11, at.Add(15*time.Second))
	var resourceRaceRevision *store.ExamRevisionPublicationResult
	var resourceRaceResource *store.ExamResourceCommandResult
	var publicationErr, resourceErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		resourceRaceRevision, publicationErr = ss.ExamRevision().Publish(ctx, resourceRacePublication, examCommand(creator.ID, "exam.revision.publish.v1", "revision-resource-race-publish", "revision-resource-race-publish-command"))
	}()
	go func() {
		defer wait.Done()
		resourceRaceResource, resourceErr = ss.ExamResource().FinalizeUpload(ctx, fourthReplacement.finalization, examCommand(creator.ID, "exam.resource.replace.v1", "revision-resource-race-replace", "revision-resource-race-replace-command"))
	}()
	wait.Wait()
	assertOneRevisionRaceWinner(t, publicationErr, resourceErr)
	if resourceRaceRevision != nil {
		snapshot, snapshotErr := ss.ExamRevision().GetSnapshot(ctx, examID, resourceRaceRevision.Revision.ID)
		require.NoError(t, snapshotErr)
		require.Equal(t, thirdResource.Value.Resource.SelectedFileRevisionID, snapshot.Resources[0].FileRevisionID, "resource race produced mixed snapshot=%#v mutation", snapshot)
		require.Nil(t, resourceRaceResource, "resource race produced mixed snapshot=%#v mutation", snapshot)
	}

	raceInstructions := "Revision race instructions"
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, newExamDraftTextUpdate(t, ctx, ss, examID, creator.ID, 12, nil, &raceInstructions, at.Add(16*time.Second)), examCommand(creator.ID, "exam.draft.text.edit.v1", "revision-race-instructions", "revision-race-instructions-command"))
	require.NoError(t, err)
	workspaceRace := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 13, fileID, "", at.Add(17*time.Second), 16)
	workspaceRace.ExpectedContentVersion = replacedWorkspace.Object.ContentVersion
	workspaceRacePublication := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 13, at.Add(18*time.Second))
	var workspaceRaceRevision *store.ExamRevisionPublicationResult
	var workspaceRaceResult *store.ExamStarterWorkspaceMutationResult
	var workspacePublicationErr, workspaceErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		workspaceRaceRevision, workspacePublicationErr = ss.ExamRevision().Publish(ctx, workspaceRacePublication, examCommand(creator.ID, "exam.revision.publish.v1", "revision-workspace-race-publish", "revision-workspace-race-publish-command"))
	}()
	go func() {
		defer wait.Done()
		workspaceRaceResult, workspaceErr = ss.ExamStarterWorkspace().ReplaceFile(ctx, workspaceRace, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "revision-workspace-race-replace", "revision-workspace-race-replace-command"))
	}()
	wait.Wait()
	assertOneRevisionRaceWinner(t, workspacePublicationErr, workspaceErr)
	if workspaceRaceRevision != nil {
		snapshot, snapshotErr := ss.ExamRevision().GetSnapshot(ctx, examID, workspaceRaceRevision.Revision.ID)
		require.NoError(t, snapshotErr)
		require.Equal(t, replacedWorkspace.Object.ID, snapshot.StarterWorkspace[1].ObjectID, "Starter Workspace race produced mixed snapshot=%#v mutation", snapshot)
		require.Nil(t, workspaceRaceResult, "Starter Workspace race produced mixed snapshot=%#v mutation", snapshot)
	}
	if probe != nil && probe.Verify != nil {
		probe.Verify(t, ctx, ss, retentionFixture)
	}
}

func assertOneRevisionRaceWinner(t *testing.T, publishErr, authoringErr error) {
	t.Helper()
	require.NotEqual(t, authoringErr == nil, publishErr == nil, "race publish error=%v authoring error=%v; want exactly one success", publishErr, authoringErr)
	loser := publishErr
	if loser == nil {
		loser = authoringErr
	}
	var conflict *store.ErrConflict
	require.ErrorAs(t, loser, &conflict, "race loser error")
	require.Equal(t, "exam_draft_revision", conflict.Constraint, "race loser error")
}

func examRevisionPublication(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, unitID model.AcademicUnitID, expected int64, at time.Time) *store.ExamRevisionPublication {
	t.Helper()
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, unitID)
	return &store.ExamRevisionPublication{RevisionID: model.NewExamRevisionID(), ExamID: examID, ActorUserID: actorID,
		ExpectedDraftRevision: expected, Kind: model.ExamRevisionPublicationStandard, PublishedAt: at,
		AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at)}
}
