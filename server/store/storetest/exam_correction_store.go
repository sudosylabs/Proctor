// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamCorrectionSQLProbe struct {
	OpenSitting                   func(*testing.T, context.Context, model.ExamSittingID)
	StageBytes                    func(*testing.T, context.Context, model.FileRevisionID, model.FileRenditionID, string)
	VerifyBytes                   func(*testing.T, context.Context, model.FileRevisionID, model.FileRenditionID, string)
	FileAvailability              func(*testing.T, context.Context, model.FileRevisionID) model.FileAvailability
	Corrections                   func(*testing.T, context.Context, model.ExamSittingID) []ExamCorrectionProvenanceProbe
	AssertAppendOnly              func(*testing.T, context.Context, model.ExamSittingID)
	ExpireStage                   func(*testing.T, context.Context, model.ExamCorrectionResourceStageID)
	ExpireStageOutcome            func(*testing.T, context.Context, model.ExamCorrectionResourceStageID)
	ReleaseStageCleanupProtection func(*testing.T, context.Context, model.ExamCorrectionResourceStageID)
	RemoveBytes                   func(*testing.T, context.Context, model.FileRevisionID, model.FileRenditionID)
	AssertPurged                  func(*testing.T, context.Context, *store.ExamCorrectionResourceStage)
}

type ExamCorrectionProvenanceProbe struct {
	PreviousRevisionID   model.ExamRevisionID
	CorrectionRevisionID model.ExamRevisionID
	PrivateReason        string
	SittingRevision      int64
}

func TestExamCorrectionStore(t *testing.T, ss store.Store, probe ExamCorrectionSQLProbe) {
	t.Helper()
	if probe.OpenSitting == nil || probe.StageBytes == nil || probe.VerifyBytes == nil || probe.FileAvailability == nil ||
		probe.Corrections == nil || probe.AssertAppendOnly == nil || probe.ExpireStage == nil || probe.ExpireStageOutcome == nil ||
		probe.ReleaseStageCleanupProtection == nil ||
		probe.RemoveBytes == nil || probe.AssertPurged == nil {
		t.Fatal("complete Exam correction SQL probe is required")
	}
	ctx := context.Background()
	capacity := model.DefaultExamCapacityPolicy()
	capacity.ResourceMaximumCount = 2
	capacity.ResourceMaximumBytes = 5
	fixture := newExamSittingFixtureWithCapacity(t, ctx, ss, capacity)
	startAt := fixture.period.StartsAt.Add(time.Hour)
	endAt := startAt.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, startAt, endAt, model.NowUTC())
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, sitting.Revision, startAt, endAt)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "correction-sitting", "correction-sitting-command"))
	requireNoError(t, err)
	scheduledOnly, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID,
		startAt.Add(4*time.Hour), endAt.Add(4*time.Hour), model.NowUTC())
	requireNoError(t, err)
	scheduledOpen, scheduledDeadline := newExamSittingLifecycleJobs(t, scheduledOnly.ID, scheduledOnly.Revision, scheduledOnly.ScheduledStartAt, scheduledOnly.ScheduledEndAt)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: scheduledOnly, OpenJob: scheduledOpen, DeadlineJob: scheduledDeadline,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule.v1", "correction-scheduled-control", "correction-scheduled-control-command"))
	requireNoError(t, err)
	forbiddenInstructions := "Cannot correct before opening"
	scheduledAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID,
		SittingID: scheduledOnly.ID, CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: scheduledOnly.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &forbiddenInstructions, CandidateSummary: "Scheduled correction control.", AcknowledgementRequired: true,
		PrivateReason: "Scheduled correction must fail", AppliedAt: model.NowUTC(),
		AuditEventID: scheduledAudit.ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-scheduled", "correction-scheduled-command"))
	requireExamSittingConflict(t, err, "exam_sitting_state")
	probe.OpenSitting(t, ctx, sitting.ID)
	live, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	authoringBefore, err := ss.ExamAuthoring().Get(ctx, fixture.examID, fixture.actor.ID)
	requireNoError(t, err)

	at := model.NowUTC()
	entry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeExamResource, model.FileIndexingNone, at)
	requireNoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	requireNoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, fixture.actor.ID, at, at.Add(model.UploadLeaseMaximumLifetime))
	requireNoError(t, err)
	stageAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	reservation := &store.ExamCorrectionResourceStageReservation{StageID: model.NewExamCorrectionResourceStageID(), ExamID: fixture.examID,
		SittingID: sitting.ID, BaseRevisionID: fixture.revisionID, Target: store.ExamCorrectionResourceAddition,
		ResourceID: model.NewExamResourceID(), Entry: entry, FileEntryID: entry.ID, Revision: revision, Lease: lease,
		RenditionID: model.NewFileRenditionID(), ActorUserID: fixture.actor.ID, CreatedAt: at,
		AuditEventID: stageAudit.ID.String(), AuditAt: model.GetMillis()}
	stageCommand := examCommand(fixture.actor.ID, store.ExamCorrectionResourceStageOperation, "correction-stage", "correction-stage-command")
	stage, err := ss.ExamCorrection().ReserveResourceStage(ctx, reservation, stageCommand)
	requireNoError(t, err)
	if stage.State != store.ExamCorrectionResourceStagePending || stage.ID != reservation.StageID || stage.ExpiresAt != lease.ExpiresAt {
		t.Fatalf("ReserveResourceStage()=%#v", stage)
	}
	probe.StageBytes(t, ctx, revision.ID, reservation.RenditionID, "hello")
	rendition, err := model.NewFileRendition(reservation.RenditionID, revision.ID, "original", string(model.ExamResourceMediaText),
		5, 0, 0, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", at.Add(time.Second))
	requireNoError(t, err)
	ready, err := ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadyInput{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at.Add(time.Second)})
	requireNoError(t, err)
	if ready.State != store.ExamCorrectionResourceStageReady || ready.Rendition == nil || ready.Rendition.ID != rendition.ID {
		t.Fatalf("MarkResourceStageReady()=%#v", ready)
	}
	if availability := probe.FileAvailability(t, ctx, revision.ID); availability != model.FileAvailabilityPending {
		t.Fatalf("Ready stage availability=%q, want pending/invisible", availability)
	}
	readyReplay, err := ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadyInput{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at.Add(2 * time.Second)})
	requireNoError(t, err)
	if readyReplay.State != store.ExamCorrectionResourceStageReady || readyReplay.Rendition == nil || *readyReplay.Rendition != *rendition {
		t.Fatalf("MarkResourceStageReady(replay)=%#v", readyReplay)
	}
	reservationReplay := *reservation
	reservationReplay.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String()
	reservedReplay, err := ss.ExamCorrection().ReserveResourceStage(ctx, &reservationReplay, stageCommand)
	requireNoError(t, err)
	if reservedReplay.State != store.ExamCorrectionResourceStageReady || reservedReplay.Rendition == nil || reservedReplay.ID != stage.ID {
		t.Fatalf("ReserveResourceStage(replay after Ready)=%#v", reservedReplay)
	}

	instructions := "Corrected **live** instructions"
	applyAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	application := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: live.Sitting.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &instructions, Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: reservation.ResourceID,
			DisplayName: "Clarification", DescriptionMarkdown: "Read this **note**.", StageID: stage.ID}},
		CandidateSummary: "Instructions and supporting material were corrected.", AcknowledgementRequired: true,
		PrivateReason: "Correct misleading supporting material", AppliedAt: at.Add(3 * time.Second), AuditEventID: applyAudit.ID.String(), AuditAt: model.GetMillis()}
	applyCommand := examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-apply", "correction-apply-command")
	result, err := ss.ExamCorrection().Apply(ctx, application, applyCommand)
	requireNoError(t, err)
	if result.Replayed || result.Revision == nil || result.Revision.Kind != model.ExamRevisionPublicationLiveCorrection ||
		result.Revision.Capacity != capacity ||
		result.PreviousRevisionID != fixture.revisionID || result.Sitting.Sitting.ExamRevisionID != result.Revision.ID ||
		result.Sitting.Sitting.Revision != live.Sitting.Revision+1 || result.EffectiveAt.IsZero() ||
		!model.TimeUTC(result.EffectiveAt).Equal(model.TimeUTC(result.Revision.PublishedAt)) {
		t.Fatalf("Apply()=%#v", result)
	}
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, result.Revision.ID)
	requireNoError(t, err)
	base, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, fixture.revisionID)
	requireNoError(t, err)
	if snapshot.InstructionsMarkdown != instructions || snapshot.Title != base.Title || snapshot.PolicyDigest != base.PolicyDigest ||
		snapshot.StarterWorkspaceDigest != base.StarterWorkspaceDigest || len(snapshot.Resources) != 1 || snapshot.Resources[0].FileRevisionID != revision.ID {
		t.Fatalf("correction snapshot=%#v base=%#v", snapshot, base)
	}
	if availability := probe.FileAvailability(t, ctx, revision.ID); availability != model.FileAvailabilityAvailable {
		t.Fatalf("consumed stage availability=%q, want available", availability)
	}

	// Replacing an opaque storage generation with the exact same verified
	// candidate bytes is a no-op. The Ready stage remains unconsumed and can be
	// used by a later real metadata/content correction.
	replacementStage, replacementCommand := reserveReadyCorrectionStage(t, ctx, ss, probe, fixture, sitting.ID, result.Revision.ID,
		store.ExamCorrectionResourceReplacement, snapshot.Resources[0].ResourceID, snapshot.Resources[0].FileEntryID, "same-replacement", "hello")
	noChangeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	noChange := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: result.Revision.ID, ExpectedSittingRevision: result.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: snapshot.Resources[0].ResourceID,
			DisplayName: snapshot.Resources[0].DisplayName, DescriptionMarkdown: snapshot.Resources[0].DescriptionMarkdown, StageID: replacementStage.ID}},
		CandidateSummary: "Supporting material was checked.", AcknowledgementRequired: true,
		PrivateReason: "Verify no-op replacement", AppliedAt: model.NowUTC(), AuditEventID: noChangeAudit.ID.String(), AuditAt: model.GetMillis()}
	_, err = ss.ExamCorrection().Apply(ctx, noChange,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-no-change", "correction-no-change-command"))
	requireExamSittingConflict(t, err, "exam_correction_no_changes")
	readyAfterNoChange, err := ss.ExamCorrection().ReserveResourceStage(ctx, correctionReservationReplay(t, ctx, ss, fixture, replacementStage, result.Revision.ID), replacementCommand)
	requireNoError(t, err)
	if readyAfterNoChange.State != store.ExamCorrectionResourceStageReady {
		t.Fatalf("no-op consumed stage: %#v", readyAfterNoChange)
	}

	// Override authority does not transfer the actor ownership of a staged
	// upload. This also proves a failed Apply leaves the Sitting unchanged.
	otherActor := saveUser(t, ctx, ss)
	wrongActor := *noChange
	wrongActor.RevisionID, wrongActor.ActorUserID, wrongActor.ManagerOverride = model.NewExamRevisionID(), otherActor.ID, true
	wrongActor.PrivateReason = "Attempt to use another manager stage"
	wrongActor.AuditEventID = saveExamSittingAudit(t, ctx, ss, otherActor.ID, fixture.examID, fixture.unitID).ID.String()
	_, err = ss.ExamCorrection().Apply(ctx, &wrongActor,
		examCommand(otherActor.ID, "exam.correction.apply.v1", "correction-wrong-actor", "correction-wrong-actor-command"))
	requireExamSittingConflict(t, err, "exam_correction_resource_stage")
	afterFailed, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	if afterFailed.Sitting.ExamRevisionID != result.Revision.ID || afterFailed.Sitting.Revision != result.Sitting.Sitting.Revision {
		t.Fatalf("failed Apply changed Sitting: %#v", afterFailed)
	}

	// One complete manifest can add a resource, replace another, edit metadata,
	// and reorder the stable identities in the same atomic correction.
	additionStage, _ := reserveReadyCorrectionStage(t, ctx, ss, probe, fixture, sitting.ID, result.Revision.ID,
		store.ExamCorrectionResourceAddition, "", "", "second-addition", "world")
	secondAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	secondApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: result.Revision.ID, ExpectedSittingRevision: result.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{
			{ResourceID: additionStage.ResourceID, DisplayName: "Second", DescriptionMarkdown: "new", StageID: additionStage.ID},
			{ResourceID: snapshot.Resources[0].ResourceID, DisplayName: "Clarification renamed", DescriptionMarkdown: "updated", StageID: replacementStage.ID},
		}, CandidateSummary: "Supporting materials were corrected and reordered.", AcknowledgementRequired: true,
		PrivateReason: "Correct and reorder resources", AppliedAt: model.NowUTC(), AuditEventID: secondAudit.ID.String(), AuditAt: model.GetMillis()}
	secondResult, err := ss.ExamCorrection().Apply(ctx, secondApply,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-second", "correction-second-command"))
	requireNoError(t, err)
	secondSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, secondResult.Revision.ID)
	requireNoError(t, err)
	if len(secondSnapshot.Resources) != 2 || secondSnapshot.Resources[0].ResourceID != additionStage.ResourceID ||
		secondSnapshot.Resources[1].FileRevisionID != replacementStage.FileRevisionID || secondSnapshot.Resources[1].DisplayName != "Clarification renamed" {
		t.Fatalf("complete corrected manifest=%#v", secondSnapshot.Resources)
	}
	oversizedStage, _ := reserveReadyCorrectionStage(t, ctx, ss, probe, fixture, sitting.ID, secondResult.Revision.ID,
		store.ExamCorrectionResourceReplacement, secondSnapshot.Resources[0].ResourceID, secondSnapshot.Resources[0].FileEntryID,
		"correction-oversized", "larger")
	oversizedAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	oversizedApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: secondResult.Revision.ID, ExpectedSittingRevision: secondResult.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{
			{ResourceID: secondSnapshot.Resources[0].ResourceID, DisplayName: secondSnapshot.Resources[0].DisplayName,
				DescriptionMarkdown: secondSnapshot.Resources[0].DescriptionMarkdown, StageID: oversizedStage.ID},
			{ResourceID: secondSnapshot.Resources[1].ResourceID, DisplayName: secondSnapshot.Resources[1].DisplayName,
				DescriptionMarkdown: secondSnapshot.Resources[1].DescriptionMarkdown},
		}, CandidateSummary: "A supporting material correction was prepared.", AcknowledgementRequired: true,
		PrivateReason: "Reject oversized corrected resource", AppliedAt: model.NowUTC(),
		AuditEventID: oversizedAudit.ID.String(), AuditAt: model.GetMillis()}
	_, err = ss.ExamCorrection().Apply(ctx, oversizedApply,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-oversized-apply", "correction-oversized-apply-command"))
	requireExamSittingConflict(t, err, "exam_correction_resource_limit")

	overCountStage, _ := reserveReadyCorrectionStage(t, ctx, ss, probe, fixture, sitting.ID, secondResult.Revision.ID,
		store.ExamCorrectionResourceAddition, "", "", "correction-over-count", "third")
	overCountAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	overCountApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: secondResult.Revision.ID, ExpectedSittingRevision: secondResult.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{
			{ResourceID: secondSnapshot.Resources[0].ResourceID, DisplayName: secondSnapshot.Resources[0].DisplayName,
				DescriptionMarkdown: secondSnapshot.Resources[0].DescriptionMarkdown},
			{ResourceID: secondSnapshot.Resources[1].ResourceID, DisplayName: secondSnapshot.Resources[1].DisplayName,
				DescriptionMarkdown: secondSnapshot.Resources[1].DescriptionMarkdown},
			{ResourceID: overCountStage.ResourceID, DisplayName: "Third", StageID: overCountStage.ID},
		}, CandidateSummary: "Supporting material corrections were prepared.", AcknowledgementRequired: true,
		PrivateReason: "Reject excessive corrected resources", AppliedAt: model.NowUTC(),
		AuditEventID: overCountAudit.ID.String(), AuditAt: model.GetMillis()}
	_, err = ss.ExamCorrection().Apply(ctx, overCountApply,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-over-count-apply", "correction-over-count-apply-command"))
	requireExamSittingConflict(t, err, "exam_correction_resource_limit")

	// Omission from the next complete manifest removes only the new Revision's
	// relationship. Older immutable Revision snapshots remain exact.
	removeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	removeApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: secondResult.Revision.ID, ExpectedSittingRevision: secondResult.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: secondSnapshot.Resources[1].ResourceID,
			DisplayName: secondSnapshot.Resources[1].DisplayName, DescriptionMarkdown: secondSnapshot.Resources[1].DescriptionMarkdown}},
		CandidateSummary: "An unnecessary clarification was removed.", AcknowledgementRequired: true,
		PrivateReason: "Remove unnecessary clarification", AppliedAt: model.NowUTC(), AuditEventID: removeAudit.ID.String(), AuditAt: model.GetMillis()}
	removed, err := ss.ExamCorrection().Apply(ctx, removeApply,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-remove", "correction-remove-command"))
	requireNoError(t, err)
	removedSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, removed.Revision.ID)
	requireNoError(t, err)
	if len(removedSnapshot.Resources) != 1 || removedSnapshot.Resources[0].ResourceID != snapshot.Resources[0].ResourceID {
		t.Fatalf("removal snapshot=%#v", removedSnapshot.Resources)
	}
	oldSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, result.Revision.ID)
	requireNoError(t, err)
	if len(oldSnapshot.Resources) != 1 || oldSnapshot.Resources[0].FileRevisionID != revision.ID {
		t.Fatalf("old immutable resource changed: %#v", oldSnapshot.Resources)
	}
	probe.VerifyBytes(t, ctx, revision.ID, rendition.ID, "hello")
	probe.VerifyBytes(t, ctx, replacementStage.FileRevisionID, replacementStage.RenditionID, "hello")
	probe.VerifyBytes(t, ctx, additionStage.FileRevisionID, additionStage.RenditionID, "world")

	// Draft/default selection is outside live correction and remains unchanged.
	authoringAfter, err := ss.ExamAuthoring().Get(ctx, fixture.examID, fixture.actor.ID)
	requireNoError(t, err)
	if authoringAfter.Exam.DefaultRevisionID != authoringBefore.Exam.DefaultRevisionID ||
		authoringAfter.Draft.BaseRevisionID != authoringBefore.Draft.BaseRevisionID || authoringAfter.Draft.Revision != authoringBefore.Draft.Revision {
		t.Fatalf("live correction changed default/Draft: before=%#v after=%#v", authoringBefore, authoringAfter)
	}
	scheduledAfter, err := ss.ExamSitting().Get(ctx, fixture.examID, scheduledOnly.ID)
	requireNoError(t, err)
	if scheduledAfter.Sitting.State != model.ExamSittingScheduled || scheduledAfter.Sitting.ExamRevisionID != fixture.revisionID || scheduledAfter.Sitting.Revision != 1 {
		t.Fatalf("correction changed another Sitting: %#v", scheduledAfter)
	}

	// A stale current Revision/Sitting fence fails before interpreting a later
	// desired snapshot.
	stale := *secondApply
	stale.RevisionID = model.NewExamRevisionID()
	stale.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String()
	_, err = ss.ExamCorrection().Apply(ctx, &stale,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-stale", "correction-stale-command"))
	requireExamSittingConflict(t, err, "exam_sitting_revision")

	// A database uniqueness failure after all guards but before retarget rolls
	// back the prospective Revision, Sitting, audit completion, and outcome.
	rollbackInstructions := "Must roll back"
	rollbackAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	rollback := &store.ExamCorrectionApplication{RevisionID: result.Revision.ID, ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: removed.Revision.ID, ExpectedSittingRevision: removed.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &rollbackInstructions, Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: removedSnapshot.Resources[0].ResourceID,
			DisplayName: removedSnapshot.Resources[0].DisplayName, DescriptionMarkdown: removedSnapshot.Resources[0].DescriptionMarkdown}},
		CandidateSummary: "A correction rollback was tested.", AcknowledgementRequired: true,
		PrivateReason: "Exercise atomic rollback", AppliedAt: model.NowUTC(), AuditEventID: rollbackAudit.ID.String(), AuditAt: model.GetMillis()}
	if _, rollbackErr := ss.ExamCorrection().Apply(ctx, rollback,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-rollback", "correction-rollback-command")); rollbackErr == nil {
		t.Fatal("duplicate Revision rollback injection unexpectedly succeeded")
	}
	afterRollback, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	if afterRollback.Sitting.ExamRevisionID != removed.Revision.ID || afterRollback.Sitting.Revision != removed.Sitting.Sitting.Revision {
		t.Fatalf("rollback injection partially retargeted Sitting: %#v", afterRollback)
	}
	rollbackAuditState, err := ss.Audit().Get(ctx, rollbackAudit.ID.String())
	requireNoError(t, err)
	if rollbackAuditState.Status != model.AuditStatusAttempt {
		t.Fatalf("rollback injection completed audit: %#v", rollbackAuditState)
	}

	// Two distinct keys racing the same optimistic fences serialize under the
	// Exam/Sitting locks: exactly one immutable Revision and retarget wins.
	current, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	requireNoError(t, err)
	racedSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, current.Sitting.ExamRevisionID)
	requireNoError(t, err)
	texts := []string{"Concurrent correction A", "Concurrent correction B"}
	applications := make([]*store.ExamCorrectionApplication, 2)
	commands := make([]*store.CommandIdempotency, 2)
	for index := range applications {
		auditAttempt := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
		applications[index] = &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
			CurrentRevisionID: current.Sitting.ExamRevisionID, ExpectedSittingRevision: current.Sitting.Revision, ActorUserID: fixture.actor.ID,
			InstructionsMarkdown: &texts[index], Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: racedSnapshot.Resources[0].ResourceID,
				DisplayName: racedSnapshot.Resources[0].DisplayName, DescriptionMarkdown: racedSnapshot.Resources[0].DescriptionMarkdown}},
			CandidateSummary: "Concurrent correction notice.", AcknowledgementRequired: true,
			PrivateReason: "Concurrent correction race", AppliedAt: model.NowUTC(), AuditEventID: auditAttempt.ID.String(), AuditAt: model.GetMillis()}
		commands[index] = examCommand(fixture.actor.ID, "exam.correction.apply.v1", fmt.Sprintf("correction-race-%d", index), fmt.Sprintf("correction-race-command-%d", index))
	}
	var wait sync.WaitGroup
	wait.Add(2)
	results := make([]*store.ExamCorrectionResult, 2)
	errorsSeen := make([]error, 2)
	for index := range applications {
		go func(index int) {
			defer wait.Done()
			results[index], errorsSeen[index] = ss.ExamCorrection().Apply(ctx, applications[index], commands[index])
		}(index)
	}
	wait.Wait()
	if (errorsSeen[0] == nil) == (errorsSeen[1] == nil) {
		t.Fatalf("concurrent Apply errors=%v,%v results=%#v,%#v", errorsSeen[0], errorsSeen[1], results[0], results[1])
	}
	loser := errorsSeen[0]
	if loser == nil {
		loser = errorsSeen[1]
	}
	requireExamSittingConflict(t, loser, "exam_sitting_revision")
	winner := results[0]
	if winner == nil {
		winner = results[1]
	}
	winnerSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, winner.Revision.ID)
	requireNoError(t, err)

	// A Ready stage that expires before Apply cannot become visible. The exact
	// failed stage remains pending and is discoverable by generic lease cleanup.
	expiredStage, _ := reserveReadyCorrectionStage(t, ctx, ss, probe, fixture, sitting.ID, winner.Revision.ID,
		store.ExamCorrectionResourceAddition, "", "", "expired-stage", "changed")
	probe.ExpireStage(t, ctx, expiredStage.ID)
	expiredAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	expiredApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: winner.Revision.ID, ExpectedSittingRevision: winner.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{
			{ResourceID: winnerSnapshot.Resources[0].ResourceID, DisplayName: winnerSnapshot.Resources[0].DisplayName, DescriptionMarkdown: winnerSnapshot.Resources[0].DescriptionMarkdown},
			{ResourceID: expiredStage.ResourceID, DisplayName: "Expired", StageID: expiredStage.ID},
		},
		CandidateSummary: "An additional supporting material was prepared.", AcknowledgementRequired: true,
		PrivateReason: "Expired upload must not publish", AppliedAt: model.NowUTC(), AuditEventID: expiredAudit.ID.String(), AuditAt: model.GetMillis()}
	_, err = ss.ExamCorrection().Apply(ctx, expiredApply,
		examCommand(fixture.actor.ID, "exam.correction.apply.v1", "correction-expired", "correction-expired-command"))
	requireExamSittingConflict(t, err, "exam_correction_resource_stage")
	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	requireNoError(t, err)
	for _, candidate := range page.Candidates {
		if candidate.RevisionID == expiredStage.FileRevisionID {
			t.Fatalf("stage with a live command outcome became purgeable: %#v", candidate)
		}
	}
	probe.ExpireStageOutcome(t, ctx, expiredStage.ID)
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	requireNoError(t, err)
	for _, candidate := range page.Candidates {
		if candidate.RevisionID == expiredStage.FileRevisionID {
			t.Fatalf("stage with an expired but retained command outcome became purgeable: %#v", candidate)
		}
	}
	deleted, err := ss.CommandOutcome().DeleteExpired(ctx, 500)
	requireNoError(t, err)
	if deleted < 1 {
		t.Fatal("expired correction stage command outcome was not physically removed")
	}
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	requireNoError(t, err)
	for _, candidate := range page.Candidates {
		if candidate.RevisionID == expiredStage.FileRevisionID {
			t.Fatalf("recently replay-protected stage became immediately purgeable: %#v", candidate)
		}
	}
	probe.ReleaseStageCleanupProtection(t, ctx, expiredStage.ID)
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	requireNoError(t, err)
	foundExpired := false
	for _, candidate := range page.Candidates {
		if candidate.RevisionID == revision.ID || candidate.RevisionID == replacementStage.FileRevisionID || candidate.RevisionID == additionStage.FileRevisionID {
			t.Fatalf("Revision-pinned correction bytes became cleanup eligible: %#v", candidate)
		}
		if candidate.RevisionID == expiredStage.FileRevisionID && candidate.LeaseID == expiredStage.UploadLeaseID {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("failed expired correction stage is not purgeable: %#v", page.Candidates)
	}
	var expiredCandidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		if page.Candidates[index].RevisionID == expiredStage.FileRevisionID {
			expiredCandidate = &page.Candidates[index]
		}
	}
	if expiredCandidate == nil {
		t.Fatal("expired correction candidate disappeared")
	}
	claim, err := ss.File().ClaimPurgeCandidate(ctx, expiredCandidate)
	requireNoError(t, err)
	probe.RemoveBytes(t, ctx, expiredStage.FileRevisionID, expiredStage.RenditionID)
	requireNoError(t, ss.File().CompletePurge(ctx, claim))
	probe.AssertPurged(t, ctx, expiredStage)

	provenance := probe.Corrections(t, ctx, sitting.ID)
	if len(provenance) != 4 || provenance[0].PreviousRevisionID != fixture.revisionID || provenance[0].CorrectionRevisionID != result.Revision.ID ||
		provenance[0].PrivateReason != application.PrivateReason || provenance[1].CorrectionRevisionID != secondResult.Revision.ID ||
		provenance[2].CorrectionRevisionID != removed.Revision.ID {
		t.Fatalf("correction provenance=%#v", provenance)
	}
	probe.AssertAppendOnly(t, ctx, sitting.ID)

	applyReplay := *application
	applyReplay.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID.String()
	replayed, err := ss.ExamCorrection().Apply(ctx, &applyReplay, applyCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Revision.ID != result.Revision.ID || replayed.Revision.Capacity != result.Revision.Capacity ||
		replayed.Sitting.Sitting.Revision != result.Sitting.Sitting.Revision {
		t.Fatalf("Apply(replay)=%#v", replayed)
	}
	audit, err := ss.Audit().Get(ctx, applyAudit.ID.String())
	requireNoError(t, err)
	if string(audit.Result) == "" || containsAnyAuditData(audit, application.PrivateReason, instructions, "Clarification", rendition.SHA256, result.Revision.ContentDigest) {
		t.Fatalf("private/authored correction data leaked into ordinary audit: %#v", audit)
	}
}

func reserveReadyCorrectionStage(t *testing.T, ctx context.Context, ss store.Store, probe ExamCorrectionSQLProbe, fixture examSittingFixture,
	sittingID model.ExamSittingID, baseRevisionID model.ExamRevisionID, target store.ExamCorrectionResourceStageTarget,
	resourceID model.ExamResourceID, entryID model.FileEntryID, key, body string,
) (*store.ExamCorrectionResourceStage, *store.CommandIdempotency) {
	t.Helper()
	at := model.NowUTC()
	var entry *model.FileEntry
	var err error
	if target == store.ExamCorrectionResourceAddition {
		resourceID, entryID = model.NewExamResourceID(), model.NewFileEntryID()
		entry, err = model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
		requireNoError(t, err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	requireNoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, fixture.actor.ID, at, at.Add(model.UploadLeaseMaximumLifetime))
	requireNoError(t, err)
	audit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	reservation := &store.ExamCorrectionResourceStageReservation{StageID: model.NewExamCorrectionResourceStageID(), ExamID: fixture.examID,
		SittingID: sittingID, BaseRevisionID: baseRevisionID, Target: target, ResourceID: resourceID, Entry: entry, FileEntryID: entryID,
		Revision: revision, Lease: lease, RenditionID: model.NewFileRenditionID(), ActorUserID: fixture.actor.ID, CreatedAt: at,
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
	command := examCommand(fixture.actor.ID, store.ExamCorrectionResourceStageOperation, key, key+"-command")
	stage, err := ss.ExamCorrection().ReserveResourceStage(ctx, reservation, command)
	requireNoError(t, err)
	probe.StageBytes(t, ctx, revision.ID, reservation.RenditionID, body)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	rendition, err := model.NewFileRendition(reservation.RenditionID, revision.ID, "original", string(model.ExamResourceMediaText), int64(len(body)), 0, 0, digest, at)
	requireNoError(t, err)
	stage, err = ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadyInput{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at})
	requireNoError(t, err)
	return stage, command
}

func correctionReservationReplay(t *testing.T, ctx context.Context, ss store.Store, fixture examSittingFixture,
	stage *store.ExamCorrectionResourceStage, baseRevisionID model.ExamRevisionID,
) *store.ExamCorrectionResourceStageReservation {
	t.Helper()
	audit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	return &store.ExamCorrectionResourceStageReservation{StageID: stage.ID, ExamID: fixture.examID, SittingID: stage.SittingID,
		BaseRevisionID: baseRevisionID, Target: stage.Target, ResourceID: stage.ResourceID, FileEntryID: stage.FileEntryID,
		Revision: &model.FileRevision{ID: stage.FileRevisionID, FileEntryID: stage.FileEntryID, CreatedAt: stage.CreatedAt,
			Availability: model.FileAvailabilityPending, IndexingState: model.FileIndexingNotRequired},
		Lease: &model.UploadLease{ID: stage.UploadLeaseID, FileRevisionID: stage.FileRevisionID, CreatedByUserID: fixture.actor.ID,
			CreatedAt: stage.CreatedAt, UpdatedAt: stage.CreatedAt, ExpiresAt: stage.ExpiresAt, Revision: 1},
		RenditionID: stage.RenditionID, ActorUserID: fixture.actor.ID, CreatedAt: stage.CreatedAt,
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}

func containsAnyAuditData(audit *model.AuditEvent, values ...string) bool {
	all := string(audit.Parameters) + string(audit.PriorState) + string(audit.Result)
	for _, value := range values {
		if value != "" && strings.Contains(all, value) {
			return true
		}
	}
	return false
}
