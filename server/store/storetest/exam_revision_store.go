// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	requireNoError(t, err)
	directoryID := model.NewStarterWorkspaceEntryID()
	directory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 2, directoryID, "cmd", at.Add(2*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, directory, examCommand(creator.ID, "exam.starter_workspace.directory.create.v1", "revision-directory", "revision-directory-command"))
	requireNoError(t, err)
	fileID := model.NewStarterWorkspaceEntryID()
	file := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, fileID, "cmd/main.go", at.Add(3*time.Second), 12)
	fileResult, err := ss.ExamStarterWorkspace().CreateFile(ctx, file, examCommand(creator.ID, "exam.starter_workspace.file.create.v1", "revision-file", "revision-file-command"))
	requireNoError(t, err)
	retentionFixture := ExamRevisionRetentionFixture{OldResourceRevisionID: resourceResult.Value.Resource.SelectedFileRevisionID,
		OldResourceRenditionID: resourceResult.Value.Rendition.ID, OldWorkspaceObjectID: fileResult.Object.ID,
		ExamID: examID, ActorUserID: creator.ID, Past: at}
	if probe != nil && probe.StageOriginal != nil {
		probe.StageOriginal(t, ctx, ss, retentionFixture)
	}

	// A failure at the final audit step occurs after the transaction has built
	// the snapshot and attempted both aggregate fences. Nothing may escape.
	rollbackInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(3500*time.Millisecond))
	rollbackInput.AuditEventID = model.NewId()
	_, err = ss.ExamRevision().Publish(ctx, rollbackInput, examCommand(creator.ID, "exam.revision.publish.v1", "revision-rollback", "revision-rollback-command"))
	if err == nil {
		t.Fatal("publication with a missing audit attempt committed")
	}
	rolledBack, err := ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, Limit: 10})
	requireNoError(t, err)
	authoringAfterRollback, authoringErr := ss.ExamAuthoring().Get(ctx, examID, creator.ID)
	requireNoError(t, authoringErr)
	if len(rolledBack) != 0 || authoringAfterRollback.Exam.Revision != 1 || authoringAfterRollback.Draft.Revision != 4 ||
		!authoringAfterRollback.Exam.DefaultRevisionID.IsZero() || !authoringAfterRollback.Draft.BaseRevisionID.IsZero() {
		t.Fatalf("failed publication leaked state: revisions=%#v authoring=%#v", rolledBack, authoringAfterRollback)
	}
	capacity := model.DefaultExamCapacityPolicy()
	capacity.WorkspaceMaximumEntries = 1
	institution.ExamCapacity = capacity
	institution, err = ss.Institution().Update(ctx, institution)
	requireNoError(t, err)
	capacityInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(3750*time.Millisecond))
	_, err = ss.ExamRevision().Publish(ctx, capacityInput,
		examCommand(creator.ID, "exam.revision.publish.v1", "revision-capacity", "revision-capacity-command"))
	var capacityConflict *store.ErrConflict
	if !errors.As(err, &capacityConflict) || capacityConflict.Constraint != "exam_revision_capacity" {
		t.Fatalf("publication capacity error=%v", err)
	}
	institution.ExamCapacity = model.DefaultExamCapacityPolicy()
	institution, err = ss.Institution().Update(ctx, institution)
	requireNoError(t, err)

	firstInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(4*time.Second))
	firstCommand := examCommand(creator.ID, "exam.revision.publish.v1", "revision-publish", "revision-publish-command")
	first, err := ss.ExamRevision().Publish(ctx, firstInput, firstCommand)
	requireNoError(t, err)
	if first.Replayed || first.Revision.Number != 1 || first.Revision.SourceDraftRevision != 4 || first.DraftRevision != 5 || first.ExamRevision != 2 || first.Revision.BaseRevisionID != "" {
		t.Fatalf("first publication=%#v", first)
	}
	firstSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, first.Revision.ID)
	requireNoError(t, err)
	if len(firstSnapshot.Resources) != 1 || firstSnapshot.Resources[0].FileRevisionID != resourceResult.Value.Resource.SelectedFileRevisionID ||
		len(firstSnapshot.StarterWorkspace) != 2 || firstSnapshot.StarterWorkspace[1].ObjectID != fileResult.Object.ID ||
		firstSnapshot.Capacity != model.DefaultExamCapacityPolicy() {
		t.Fatalf("publication did not pin exact Draft: %#v", firstSnapshot)
	}
	retentionFixture.RevisionID = first.Revision.ID
	retentionFixture.ResourceID = firstSnapshot.Resources[0].ResourceID
	retentionFixture.OldResourceEntryID = firstSnapshot.Resources[0].FileEntryID

	// A lost success response retries with the original Draft fence and a new
	// generated Revision ID. The stored outcome must win before stale/no-change.
	retry := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 4, at.Add(5*time.Second))
	replayed, err := ss.ExamRevision().Publish(ctx, retry, firstCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Revision.ID != first.Revision.ID || replayed.DraftRevision != 5 || replayed.ExamRevision != 2 {
		t.Fatalf("publication replay=%#v", replayed)
	}

	unchanged := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 5, at.Add(6*time.Second))
	_, err = ss.ExamRevision().Publish(ctx, unchanged, examCommand(creator.ID, "exam.revision.publish.v1", "revision-no-change", "revision-no-change-command"))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "exam_revision_no_changes" {
		t.Fatalf("unchanged publication error=%v", err)
	}

	// Change both content families after publication. The first Revision must
	// keep its exact old bytes while the second freezes the new selections.
	alteredCapacity := model.DefaultExamCapacityPolicy()
	alteredCapacity.ResourceMaximumCount = 20
	alteredCapacity.WorkspaceMaximumEntries = 750
	institution.ExamCapacity = alteredCapacity
	institution, err = ss.Institution().Update(ctx, institution)
	requireNoError(t, err)
	replacement := reserveExamResource(t, ctx, ss, examID, creator.ID, resourceResult.Value.Resource.ID, "Reference", 0, 5, at.Add(7*time.Second), model.ExamResourceMediaText, resourceResult.Value)
	replacedResource, err := ss.ExamResource().FinalizeUpload(ctx, replacement.finalization, examCommand(creator.ID, "exam.resource.replace.v1", "revision-resource-replace", "revision-resource-replace-command"))
	requireNoError(t, err)
	workspaceReplacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 6, fileID, "", at.Add(8*time.Second), 14)
	workspaceReplacement.ExpectedContentVersion = fileResult.Object.ContentVersion
	replacedWorkspace, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, workspaceReplacement, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "revision-workspace-replace", "revision-workspace-replace-command"))
	requireNoError(t, err)
	secondInput := examRevisionPublication(t, ctx, ss, examID, creator.ID, unit.ID, 7, at.Add(9*time.Second))
	second, err := ss.ExamRevision().Publish(ctx, secondInput, examCommand(creator.ID, "exam.revision.publish.v1", "revision-publish-second", "revision-publish-second-command"))
	requireNoError(t, err)
	secondSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, second.Revision.ID)
	requireNoError(t, err)
	if second.Revision.Number != 2 || second.Revision.BaseRevisionID != first.Revision.ID || secondSnapshot.Capacity != alteredCapacity ||
		secondSnapshot.Resources[0].FileRevisionID != replacedResource.Value.Resource.SelectedFileRevisionID || secondSnapshot.StarterWorkspace[1].ObjectID != replacedWorkspace.Object.ID {
		t.Fatalf("second publication=%#v", second)
	}
	retained, err := ss.ExamRevision().GetSnapshot(ctx, examID, first.Revision.ID)
	requireNoError(t, err)
	if retained.Capacity != model.DefaultExamCapacityPolicy() || retained.Resources[0].FileRevisionID != resourceResult.Value.Resource.SelectedFileRevisionID || retained.StarterWorkspace[1].ObjectID != fileResult.Object.ID {
		t.Fatalf("published history changed=%#v", retained)
	}
	summary, err := ss.ExamRevision().GetSummary(ctx, examID, first.Revision.ID)
	requireNoError(t, err)
	if summary.ResourceCount != 1 || summary.StarterWorkspaceEntries != 2 || summary.PolicyDigest != first.Revision.PolicyDigest {
		t.Fatalf("summary=%#v", summary)
	}
	listed, err := ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, Limit: 1})
	requireNoError(t, err)
	if len(listed) != 1 || listed[0].ID != second.Revision.ID {
		t.Fatalf("first revision page=%#v", listed)
	}
	listed, err = ss.ExamRevision().List(ctx, store.ExamRevisionListOptions{ExamID: examID, BeforeNumber: listed[0].Number, BeforeRevisionID: listed[0].ID, Limit: 200})
	requireNoError(t, err)
	if len(listed) != 1 || listed[0].ID != first.Revision.ID {
		t.Fatalf("second revision page=%#v", listed)
	}

	// The Exam/Draft lock serializes two fresh commands against the same fence:
	// exactly one publishes and the loser observes the advanced Draft.
	thirdReplacement := reserveExamResource(t, ctx, ss, examID, creator.ID, resourceResult.Value.Resource.ID, "Reference", 0, 8, at.Add(10*time.Second), model.ExamResourceMediaText, replacedResource.Value)
	thirdResource, err := ss.ExamResource().FinalizeUpload(ctx, thirdReplacement.finalization, examCommand(creator.ID, "exam.resource.replace.v1", "revision-race-replace", "revision-race-replace-command"))
	requireNoError(t, err)
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
	if successes != 1 || stale != 1 {
		t.Fatalf("concurrent publications successes=%d stale=%d errors=%v", successes, stale, errs)
	}

	// Publication and each authoring family share the same Exam/Draft lock. A
	// race must produce one complete before-or-after state, never a mixed one.
	raceTitle := "Revision race title"
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, newExamDraftTextUpdate(t, ctx, ss, examID, creator.ID, 10, &raceTitle, nil, at.Add(13*time.Second)), examCommand(creator.ID, "exam.draft.text.edit.v1", "revision-race-title", "revision-race-title-command"))
	requireNoError(t, err)
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
		requireNoError(t, snapshotErr)
		if snapshot.Resources[0].FileRevisionID != thirdResource.Value.Resource.SelectedFileRevisionID || resourceRaceResource != nil {
			t.Fatalf("resource race produced mixed snapshot=%#v mutation=%#v", snapshot, resourceRaceResource)
		}
	}

	raceInstructions := "Revision race instructions"
	_, err = ss.ExamAuthoring().UpdateDraftText(ctx, newExamDraftTextUpdate(t, ctx, ss, examID, creator.ID, 12, nil, &raceInstructions, at.Add(16*time.Second)), examCommand(creator.ID, "exam.draft.text.edit.v1", "revision-race-instructions", "revision-race-instructions-command"))
	requireNoError(t, err)
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
		requireNoError(t, snapshotErr)
		if snapshot.StarterWorkspace[1].ObjectID != replacedWorkspace.Object.ID || workspaceRaceResult != nil {
			t.Fatalf("Starter Workspace race produced mixed snapshot=%#v mutation=%#v", snapshot, workspaceRaceResult)
		}
	}
	if probe != nil && probe.Verify != nil {
		probe.Verify(t, ctx, ss, retentionFixture)
	}
}

func assertOneRevisionRaceWinner(t *testing.T, publishErr, authoringErr error) {
	t.Helper()
	if (publishErr == nil) == (authoringErr == nil) {
		t.Fatalf("race publish error=%v authoring error=%v; want exactly one success", publishErr, authoringErr)
	}
	loser := publishErr
	if loser == nil {
		loser = authoringErr
	}
	var conflict *store.ErrConflict
	if !errors.As(loser, &conflict) || conflict.Constraint != "exam_draft_revision" {
		t.Fatalf("race loser error=%v, want Draft revision conflict", loser)
	}
}

func examRevisionPublication(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, unitID model.AcademicUnitID, expected int64, at time.Time) *store.ExamRevisionPublication {
	t.Helper()
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, unitID)
	return &store.ExamRevisionPublication{RevisionID: model.NewExamRevisionID(), ExamID: examID, ActorUserID: actorID,
		ExpectedDraftRevision: expected, Kind: model.ExamRevisionPublicationStandard, PublishedAt: at,
		AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at)}
}
