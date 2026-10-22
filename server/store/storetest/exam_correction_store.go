// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NotNil(t, probe.OpenSitting, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.StageBytes, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.VerifyBytes, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.FileAvailability, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.Corrections, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.AssertAppendOnly, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.ExpireStage, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.ExpireStageOutcome, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.ReleaseStageCleanupProtection, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.RemoveBytes, "complete Exam correction SQL probe is required")
	require.NotNil(t, probe.AssertPurged, "complete Exam correction SQL probe is required")
	ctx := context.Background()
	capacity := model.DefaultExamCapacityPolicy()
	capacity.ResourceMaximumCount = 2
	capacity.ResourceMaximumBytes = 5
	fixture := newExamSittingFixtureWithCapacity(t, ctx, ss, capacity)
	startAt := fixture.period.StartsAt.Add(time.Hour)
	endAt := startAt.Add(2 * time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID, startAt, endAt, model.NowUTC())
	require.NoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, sitting.Revision, startAt, endAt)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule", "correction-sitting", "correction-sitting-command"))
	require.NoError(t, err)
	scheduledOnly, err := model.NewExamSitting(model.NewExamSittingID(), fixture.examID, fixture.revisionID, fixture.class.ID,
		startAt.Add(4*time.Hour), endAt.Add(4*time.Hour), model.NowUTC())
	require.NoError(t, err)
	scheduledOpen, scheduledDeadline := newExamSittingLifecycleJobs(t, scheduledOnly.ID, scheduledOnly.Revision, scheduledOnly.ScheduledStartAt, scheduledOnly.ScheduledEndAt)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: scheduledOnly, OpenJob: scheduledOpen, DeadlineJob: scheduledDeadline,
		ActorUserID: fixture.actor.ID, AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC(),
		Mail: newExamSittingMailFanout(t, fixture.actor.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(fixture.actor.ID, "exam.sitting.schedule", "correction-scheduled-control", "correction-scheduled-control-command"))
	require.NoError(t, err)
	forbiddenInstructions := "Cannot correct before opening"
	scheduledAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID,
		SittingID: scheduledOnly.ID, CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: scheduledOnly.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &forbiddenInstructions, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Scheduled correction control.", AcknowledgementRequired: true,
		PrivateReason: "Scheduled correction must fail", AppliedAt: model.NowUTC(),
		AuditEventID: scheduledAudit.ID, AuditAt: model.NowUTC()},
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-scheduled", "correction-scheduled-command"))
	requireConflictConstraint(t, err, "exam_sitting_state")
	probe.OpenSitting(t, ctx, sitting.ID)
	live, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	require.NoError(t, err)
	authoringBefore, err := ss.ExamAuthoring().Get(ctx, fixture.examID, fixture.actor.ID)
	require.NoError(t, err)

	at := model.NowUTC()
	entry, err := model.NewFileEntryForPurpose(model.NewFileEntryID(), model.FilePurposeExamResource, model.FileIndexingNone, at)
	require.NoError(t, err)
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entry.ID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, fixture.actor.ID, at, at.Add(model.UploadLeaseMaximumLifetime))
	require.NoError(t, err)
	stageAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	reservation := &store.ExamCorrectionResourceStageReservation{StageID: model.NewExamCorrectionResourceStageID(), ExamID: fixture.examID,
		SittingID: sitting.ID, BaseRevisionID: fixture.revisionID, Target: store.ExamCorrectionResourceAddition,
		ResourceID: model.NewExamResourceID(), Entry: entry, FileEntryID: entry.ID, Revision: revision, Lease: lease,
		RenditionID: model.NewFileRenditionID(), ActorUserID: fixture.actor.ID, CreatedAt: at,
		AuditEventID: stageAudit.ID, AuditAt: model.NowUTC()}
	stageCommand := examCommand(fixture.actor.ID, store.ExamCorrectionResourceStageOperation, "correction-stage", "correction-stage-command")
	stage, err := ss.ExamCorrection().ReserveResourceStage(ctx, reservation, stageCommand)
	require.NoError(t, err)
	require.Equal(t, store.ExamCorrectionResourceStagePending, stage.State)
	require.Equal(t, reservation.StageID, stage.ID)
	require.Equal(t, lease.ExpiresAt, stage.ExpiresAt)
	probe.StageBytes(t, ctx, revision.ID, reservation.RenditionID, "hello")
	rendition, err := model.NewFileRendition(reservation.RenditionID, revision.ID, "original", string(model.ExamResourceMediaText),
		5, 0, 0, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", at.Add(time.Second))
	require.NoError(t, err)
	ready, err := ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadiness{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at.Add(time.Second)})
	require.NoError(t, err)
	require.Equal(t, store.ExamCorrectionResourceStageReady, ready.State)
	require.NotNil(t, ready.Rendition)
	require.Equal(t, rendition.ID, ready.Rendition.ID)
	availability := probe.FileAvailability(t, ctx, revision.ID)
	require.Equal(t, model.FileAvailabilityPending, availability, "Ready stage availability")
	readyReplay, err := ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadiness{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at.Add(2 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, store.ExamCorrectionResourceStageReady, readyReplay.State)
	require.NotNil(t, readyReplay.Rendition)
	require.Equal(t, *rendition, *readyReplay.Rendition)
	reservationReplay := *reservation
	reservationReplay.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID
	reservedReplay, err := ss.ExamCorrection().ReserveResourceStage(ctx, &reservationReplay, stageCommand)
	require.NoError(t, err)
	require.Equal(t, store.ExamCorrectionResourceStageReady, reservedReplay.State)
	require.NotNil(t, reservedReplay.Rendition)
	require.Equal(t, stage.ID, reservedReplay.ID)

	instructions := "Corrected **live** instructions"
	applyAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	application := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: live.Sitting.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &instructions, Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: reservation.ResourceID,
			DisplayName: "Clarification", DescriptionMarkdown: "Read this **note**.", StageID: stage.ID}},
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Instructions and supporting material were corrected.", AcknowledgementRequired: true,
		PrivateReason: "Correct misleading supporting material", AppliedAt: at.Add(3 * time.Second), AuditEventID: applyAudit.ID, AuditAt: model.NowUTC()}
	applyCommand := examCommand(fixture.actor.ID, "exam.correction.apply", "correction-apply", "correction-apply-command")
	result, err := ss.ExamCorrection().Apply(ctx, application, applyCommand)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.NotNil(t, result.Revision)
	require.Equal(t, model.ExamRevisionPublicationLiveCorrection, result.Revision.Kind)
	require.Equal(t, capacity, result.Revision.Capacity)
	require.Equal(t, fixture.revisionID, result.PreviousRevisionID)
	require.Equal(t, result.Revision.ID, result.Sitting.Sitting.ExamRevisionID)
	require.Equal(t, live.Sitting.Revision+1, result.Sitting.Sitting.Revision)
	require.False(t, result.EffectiveAt.IsZero())
	require.True(t, model.TimeUTC(result.EffectiveAt).Equal(model.TimeUTC(result.Revision.PublishedAt)))
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, result.Revision.ID)
	require.NoError(t, err)
	base, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, fixture.revisionID)
	require.NoError(t, err)
	require.Equal(t, instructions, snapshot.InstructionsMarkdown, "correction snapshot=%#v base=%#v", snapshot, base)
	require.Equal(t, base.Title, snapshot.Title, "correction snapshot=%#v base=%#v", snapshot, base)
	require.Equal(t, base.PolicyDigest, snapshot.PolicyDigest, "correction snapshot=%#v base=%#v", snapshot, base)
	require.Equal(t, base.StarterWorkspaceDigest, snapshot.StarterWorkspaceDigest, "correction snapshot=%#v base=%#v", snapshot, base)
	require.Len(t, snapshot.Resources, 1, "correction snapshot=%#v base=%#v", snapshot, base)
	require.Equal(t, revision.ID, snapshot.Resources[0].FileRevisionID, "correction snapshot=%#v base=%#v", snapshot, base)
	availability = probe.FileAvailability(t, ctx, revision.ID)
	require.Equal(t, model.FileAvailabilityAvailable, availability, "consumed stage availability")

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
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Supporting material was checked.", AcknowledgementRequired: true,
		PrivateReason: "Verify no-op replacement", AppliedAt: model.NowUTC(), AuditEventID: noChangeAudit.ID, AuditAt: model.NowUTC()}
	_, err = ss.ExamCorrection().Apply(ctx, noChange,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-no-change", "correction-no-change-command"))
	requireConflictConstraint(t, err, "exam_correction_no_changes")
	readyAfterNoChange, err := ss.ExamCorrection().ReserveResourceStage(ctx, correctionReservationReplay(t, ctx, ss, fixture, replacementStage, result.Revision.ID), replacementCommand)
	require.NoError(t, err)
	require.Equal(t, store.ExamCorrectionResourceStageReady, readyAfterNoChange.State)

	// Override authority does not transfer the actor ownership of a staged
	// upload. This also proves a failed Apply leaves the Sitting unchanged.
	otherActor := saveUser(t, ctx, ss)
	wrongActor := *noChange
	wrongActor.RevisionID, wrongActor.ActorUserID, wrongActor.ManagerOverride = model.NewExamRevisionID(), otherActor.ID, true
	wrongActor.PrivateReason = "Attempt to use another manager stage"
	wrongActor.AuditEventID = saveExamSittingAudit(t, ctx, ss, otherActor.ID, fixture.examID, fixture.unitID).ID
	_, err = ss.ExamCorrection().Apply(ctx, &wrongActor,
		examCommand(otherActor.ID, "exam.correction.apply", "correction-wrong-actor", "correction-wrong-actor-command"))
	requireConflictConstraint(t, err, "exam_correction_resource_stage")
	afterFailed, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	require.NoError(t, err)
	require.Equal(t, result.Revision.ID, afterFailed.Sitting.ExamRevisionID)
	require.Equal(t, result.Sitting.Sitting.Revision, afterFailed.Sitting.Revision)

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
		}, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Supporting materials were corrected and reordered.", AcknowledgementRequired: true,
		PrivateReason: "Correct and reorder resources", AppliedAt: model.NowUTC(), AuditEventID: secondAudit.ID, AuditAt: model.NowUTC()}
	secondResult, err := ss.ExamCorrection().Apply(ctx, secondApply,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-second", "correction-second-command"))
	require.NoError(t, err)
	secondSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, secondResult.Revision.ID)
	require.NoError(t, err)
	require.Len(t, secondSnapshot.Resources, 2)
	require.Equal(t, additionStage.ResourceID, secondSnapshot.Resources[0].ResourceID)
	require.Equal(t, replacementStage.FileRevisionID, secondSnapshot.Resources[1].FileRevisionID)
	require.Equal(t, "Clarification renamed", secondSnapshot.Resources[1].DisplayName)
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
		}, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "A supporting material correction was prepared.", AcknowledgementRequired: true,
		PrivateReason: "Reject oversized corrected resource", AppliedAt: model.NowUTC(),
		AuditEventID: oversizedAudit.ID, AuditAt: model.NowUTC()}
	_, err = ss.ExamCorrection().Apply(ctx, oversizedApply,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-oversized-apply", "correction-oversized-apply-command"))
	requireConflictConstraint(t, err, "exam_correction_resource_limit")

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
		}, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Supporting material corrections were prepared.", AcknowledgementRequired: true,
		PrivateReason: "Reject excessive corrected resources", AppliedAt: model.NowUTC(),
		AuditEventID: overCountAudit.ID, AuditAt: model.NowUTC()}
	_, err = ss.ExamCorrection().Apply(ctx, overCountApply,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-over-count-apply", "correction-over-count-apply-command"))
	requireConflictConstraint(t, err, "exam_correction_resource_limit")

	// Omission from the next complete manifest removes only the new Revision's
	// relationship. Older immutable Revision snapshots remain exact.
	removeAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	removeApply := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: secondResult.Revision.ID, ExpectedSittingRevision: secondResult.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: secondSnapshot.Resources[1].ResourceID,
			DisplayName: secondSnapshot.Resources[1].DisplayName, DescriptionMarkdown: secondSnapshot.Resources[1].DescriptionMarkdown}},
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "An unnecessary clarification was removed.", AcknowledgementRequired: true,
		PrivateReason: "Remove unnecessary clarification", AppliedAt: model.NowUTC(), AuditEventID: removeAudit.ID, AuditAt: model.NowUTC()}
	removed, err := ss.ExamCorrection().Apply(ctx, removeApply,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-remove", "correction-remove-command"))
	require.NoError(t, err)
	removedSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, removed.Revision.ID)
	require.NoError(t, err)
	require.Len(t, removedSnapshot.Resources, 1)
	require.Equal(t, snapshot.Resources[0].ResourceID, removedSnapshot.Resources[0].ResourceID)
	oldSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, result.Revision.ID)
	require.NoError(t, err)
	require.Len(t, oldSnapshot.Resources, 1)
	require.Equal(t, revision.ID, oldSnapshot.Resources[0].FileRevisionID)
	probe.VerifyBytes(t, ctx, revision.ID, rendition.ID, "hello")
	probe.VerifyBytes(t, ctx, replacementStage.FileRevisionID, replacementStage.RenditionID, "hello")
	probe.VerifyBytes(t, ctx, additionStage.FileRevisionID, additionStage.RenditionID, "world")

	// Draft/default selection is outside live correction and remains unchanged.
	authoringAfter, err := ss.ExamAuthoring().Get(ctx, fixture.examID, fixture.actor.ID)
	require.NoError(t, err)
	require.Equal(t, authoringBefore.Exam.DefaultRevisionID, authoringAfter.Exam.DefaultRevisionID, "live correction changed default/Draft: before=%#v after=%#v", authoringBefore, authoringAfter)
	require.Equal(t, authoringBefore.Draft.BaseRevisionID, authoringAfter.Draft.BaseRevisionID, "live correction changed default/Draft: before=%#v after=%#v", authoringBefore, authoringAfter)
	require.Equal(t, authoringBefore.Draft.Revision, authoringAfter.Draft.Revision, "live correction changed default/Draft: before=%#v after=%#v", authoringBefore, authoringAfter)
	scheduledAfter, err := ss.ExamSitting().Get(ctx, fixture.examID, scheduledOnly.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingScheduled, scheduledAfter.Sitting.State)
	require.Equal(t, fixture.revisionID, scheduledAfter.Sitting.ExamRevisionID)
	require.Equal(t, int64(1), scheduledAfter.Sitting.Revision)

	// A stale current Revision/Sitting fence fails before interpreting a later
	// desired snapshot.
	stale := *secondApply
	stale.RevisionID = model.NewExamRevisionID()
	stale.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID
	_, err = ss.ExamCorrection().Apply(ctx, &stale,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-stale", "correction-stale-command"))
	requireConflictConstraint(t, err, "exam_sitting_revision")

	// A database uniqueness failure after all guards but before retarget rolls
	// back the prospective Revision, Sitting, audit completion, and outcome.
	rollbackInstructions := "Must roll back"
	rollbackAudit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	rollback := &store.ExamCorrectionApplication{RevisionID: result.Revision.ID, ExamID: fixture.examID, SittingID: sitting.ID,
		CurrentRevisionID: removed.Revision.ID, ExpectedSittingRevision: removed.Sitting.Sitting.Revision, ActorUserID: fixture.actor.ID,
		InstructionsMarkdown: &rollbackInstructions, Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: removedSnapshot.Resources[0].ResourceID,
			DisplayName: removedSnapshot.Resources[0].DisplayName, DescriptionMarkdown: removedSnapshot.Resources[0].DescriptionMarkdown}},
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "A correction rollback was tested.", AcknowledgementRequired: true,
		PrivateReason: "Exercise atomic rollback", AppliedAt: model.NowUTC(), AuditEventID: rollbackAudit.ID, AuditAt: model.NowUTC()}
	_, rollbackErr := ss.ExamCorrection().Apply(ctx, rollback,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-rollback", "correction-rollback-command"))
	require.Error(t, rollbackErr, "duplicate Revision rollback injection unexpectedly succeeded")
	afterRollback, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	require.NoError(t, err)
	require.Equal(t, removed.Revision.ID, afterRollback.Sitting.ExamRevisionID)
	require.Equal(t, removed.Sitting.Sitting.Revision, afterRollback.Sitting.Revision)
	rollbackAuditState, err := ss.Audit().Get(ctx, rollbackAudit.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, rollbackAuditState.Status)

	// Two distinct keys racing the same optimistic fences serialize under the
	// Exam/Sitting locks: exactly one immutable Revision and retarget wins.
	current, err := ss.ExamSitting().Get(ctx, fixture.examID, sitting.ID)
	require.NoError(t, err)
	racedSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, current.Sitting.ExamRevisionID)
	require.NoError(t, err)
	texts := []string{"Concurrent correction A", "Concurrent correction B"}
	applications := make([]*store.ExamCorrectionApplication, 2)
	commands := make([]*store.CommandIdempotency, 2)
	for index := range applications {
		auditAttempt := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
		applications[index] = &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: sitting.ID,
			CurrentRevisionID: current.Sitting.ExamRevisionID, ExpectedSittingRevision: current.Sitting.Revision, ActorUserID: fixture.actor.ID,
			InstructionsMarkdown: &texts[index], Resources: []store.ExamCorrectionResourceManifestItem{{ResourceID: racedSnapshot.Resources[0].ResourceID,
				DisplayName: racedSnapshot.Resources[0].DisplayName, DescriptionMarkdown: racedSnapshot.Resources[0].DescriptionMarkdown}},
			AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Concurrent correction notice.", AcknowledgementRequired: true,
			PrivateReason: "Concurrent correction race", AppliedAt: model.NowUTC(), AuditEventID: auditAttempt.ID, AuditAt: model.NowUTC()}
		commands[index] = examCommand(fixture.actor.ID, "exam.correction.apply", fmt.Sprintf("correction-race-%d", index), fmt.Sprintf("correction-race-command-%d", index))
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
	require.NotEqual(t, errorsSeen[1] == nil, errorsSeen[0] == nil, "concurrent Apply errors")
	loser := errorsSeen[0]
	if loser == nil {
		loser = errorsSeen[1]
	}
	requireConflictConstraint(t, loser, "exam_sitting_revision")
	winner := results[0]
	if winner == nil {
		winner = results[1]
	}
	winnerSnapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, winner.Revision.ID)
	require.NoError(t, err)

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
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "An additional supporting material was prepared.", AcknowledgementRequired: true,
		PrivateReason: "Expired upload must not publish", AppliedAt: model.NowUTC(), AuditEventID: expiredAudit.ID, AuditAt: model.NowUTC()}
	_, err = ss.ExamCorrection().Apply(ctx, expiredApply,
		examCommand(fixture.actor.ID, "exam.correction.apply", "correction-expired", "correction-expired-command"))
	requireConflictConstraint(t, err, "exam_correction_resource_stage")
	page, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	for _, candidate := range page.Candidates {
		require.NotEqual(t, expiredStage.FileRevisionID, candidate.RevisionID)
	}
	probe.ExpireStageOutcome(t, ctx, expiredStage.ID)
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	for _, candidate := range page.Candidates {
		require.NotEqual(t, expiredStage.FileRevisionID, candidate.RevisionID)
	}
	deleted, err := ss.CommandOutcome().DeleteExpired(ctx, 500)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, int64(1), "expired correction stage command outcome was not physically removed")
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	for _, candidate := range page.Candidates {
		require.NotEqual(t, expiredStage.FileRevisionID, candidate.RevisionID)
	}
	probe.ReleaseStageCleanupProtection(t, ctx, expiredStage.ID)
	page, err = ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	foundExpired := false
	for _, candidate := range page.Candidates {
		require.NotEqual(t, revision.ID, candidate.RevisionID)
		require.NotEqual(t, replacementStage.FileRevisionID, candidate.RevisionID)
		require.NotEqual(t, additionStage.FileRevisionID, candidate.RevisionID)
		if candidate.RevisionID == expiredStage.FileRevisionID && candidate.LeaseID == expiredStage.UploadLeaseID {
			foundExpired = true
		}
	}
	require.True(t, foundExpired, "failed expired correction stage is not purgeable: %#v", page.Candidates)
	var expiredCandidate *store.FilePurgeCandidate
	for index := range page.Candidates {
		if page.Candidates[index].RevisionID == expiredStage.FileRevisionID {
			expiredCandidate = &page.Candidates[index]
		}
	}
	require.NotNil(t, expiredCandidate, "expired correction candidate disappeared")
	claim, err := ss.File().ClaimPurgeCandidate(ctx, expiredCandidate)
	require.NoError(t, err)
	probe.RemoveBytes(t, ctx, expiredStage.FileRevisionID, expiredStage.RenditionID)
	require.NoError(t, ss.File().CompletePurge(ctx, claim))
	probe.AssertPurged(t, ctx, expiredStage)

	provenance := probe.Corrections(t, ctx, sitting.ID)
	require.Len(t, provenance, 4)
	require.Equal(t, fixture.revisionID, provenance[0].PreviousRevisionID)
	require.Equal(t, result.Revision.ID, provenance[0].CorrectionRevisionID)
	require.Equal(t, application.PrivateReason, provenance[0].PrivateReason)
	require.Equal(t, secondResult.Revision.ID, provenance[1].CorrectionRevisionID)
	require.Equal(t, removed.Revision.ID, provenance[2].CorrectionRevisionID)
	probe.AssertAppendOnly(t, ctx, sitting.ID)

	applyReplay := *application
	applyReplay.AuditEventID = saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID).ID
	replayed, err := ss.ExamCorrection().Apply(ctx, &applyReplay, applyCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, result.Revision.ID, replayed.Revision.ID)
	require.Equal(t, result.Revision.Capacity, replayed.Revision.Capacity)
	require.Equal(t, result.Sitting.Sitting.Revision, replayed.Sitting.Sitting.Revision)
	audit, err := ss.Audit().Get(ctx, applyAudit.ID)
	require.NoError(t, err)
	require.NotEmpty(t, string(audit.Result))
	require.False(t, containsAnyAuditData(audit, application.PrivateReason, instructions, "Clarification", rendition.SHA256, result.Revision.ContentDigest))
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
		require.NoError(t, err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, fixture.actor.ID, at, at.Add(model.UploadLeaseMaximumLifetime))
	require.NoError(t, err)
	audit := saveExamSittingAudit(t, ctx, ss, fixture.actor.ID, fixture.examID, fixture.unitID)
	reservation := &store.ExamCorrectionResourceStageReservation{StageID: model.NewExamCorrectionResourceStageID(), ExamID: fixture.examID,
		SittingID: sittingID, BaseRevisionID: baseRevisionID, Target: target, ResourceID: resourceID, Entry: entry, FileEntryID: entryID,
		Revision: revision, Lease: lease, RenditionID: model.NewFileRenditionID(), ActorUserID: fixture.actor.ID, CreatedAt: at,
		AuditEventID: audit.ID, AuditAt: model.NowUTC()}
	command := examCommand(fixture.actor.ID, store.ExamCorrectionResourceStageOperation, key, key+"-command")
	stage, err := ss.ExamCorrection().ReserveResourceStage(ctx, reservation, command)
	require.NoError(t, err)
	probe.StageBytes(t, ctx, revision.ID, reservation.RenditionID, body)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	rendition, err := model.NewFileRendition(reservation.RenditionID, revision.ID, "original", string(model.ExamResourceMediaText), int64(len(body)), 0, 0, digest, at)
	require.NoError(t, err)
	stage, err = ss.ExamCorrection().MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadiness{StageID: stage.ID,
		ActorUserID: fixture.actor.ID, Rendition: rendition, ReadyAt: at})
	require.NoError(t, err)
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
		AuditEventID: audit.ID, AuditAt: model.NowUTC()}
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
