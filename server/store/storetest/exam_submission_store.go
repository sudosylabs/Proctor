// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExamSubmissionSQLProbe exposes only PostgreSQL-only fault, retention, and
// concurrency fixtures which the public Submission contract cannot create.
type ExamSubmissionSQLProbe struct {
	ConcurrentPeer       store.ExamSubmissionStore
	AssertSchema         func(*testing.T, context.Context)
	AssertRetentionFence func(*testing.T, context.Context, model.SubmissionID)
	CorruptManifest      func(*testing.T, context.Context, model.SubmissionID)
	IntegrityPersistence func(*testing.T, context.Context, model.ExamAttemptID, int64) SubmissionIntegrityPersistence
}

type SubmissionIntegrityPersistence struct {
	PendingQualifiers int64
	UnresolvedMissing int64
}

// attachSubmissionReceipt stamps the seal with the PostgreSQL action instant
// that ResolveSealTarget reserves, as the application does, then attaches the
// receipt notice for that instant. An application-clock instant can lead the
// database clock and be refused as a future seal time. A selector that cannot
// resolve keeps the caller's instant so Seal reports its own refusal.
func attachSubmissionReceipt(t *testing.T, ctx context.Context, ss store.Store, candidate *model.User, input *store.ExamSubmissionSeal) {
	t.Helper()
	if target, err := ss.ExamSubmission().ResolveSealTarget(ctx, input.Access); err == nil && !target.SealAt.IsZero() {
		input.AuditAt = target.SealAt
	}
	input.Notice = submissionReceiptNotice(t, candidate, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionReceived)
	input.ExpectedRecipientRevision = candidate.Revision
}

func attachAutomaticSubmissionReceipt(t *testing.T, candidate *model.User, input *store.ExamSubmissionAutomaticSeal) {
	t.Helper()
	input.Notice = submissionReceiptNotice(t, candidate, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionAutomaticallySealed)
	input.ExpectedRecipientRevision = candidate.Revision
}

func attachManagerEndedSubmissionReceipt(t *testing.T, candidate *model.User, input *store.ExamSubmissionManagerEnd) {
	t.Helper()
	input.Notice = submissionReceiptNotice(t, candidate, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionManagerEnded)
	input.ExpectedRecipientRevision = candidate.Revision
}

func submissionReceiptNotice(t *testing.T, candidate *model.User, submissionID model.SubmissionID, auditAt time.Time,
	key model.MailTemplateKey,
) *store.PreparedMail {
	t.Helper()
	at := model.TimeUTC(auditAt)
	occurrenceID := model.MailOccurrenceID(submissionID.String())
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	job, err = job.RequestCancellation(at)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSubmissionReceipt,
		TemplateKey: key, ActorUserID: candidate.ID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: candidate.ID,
		TemplateKey: key, TemplateDigest: strings.Repeat("0", 64), MaskedRecipient: "c***@example.edu",
		State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at, MessageDate: at,
		Deadline: at.Add(72 * time.Hour), MessageID: "<receipt." + deliveryID.String() + "@example.test>",
		PublicFailureCode: model.MailDeliveryDisabledCode, Revision: 1}
	err = occurrence.Validate()
	require.NoError(t, err)
	err = delivery.Validate()
	require.NoError(t, err)
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}
}

// TestExamSubmissionStore verifies voluntary terminal sealing and protected
// manager inspection through the public Submission Store contract.
func TestExamSubmissionStore(t *testing.T, ss store.Store, submissions store.ExamSubmissionStore, probes ...ExamSubmissionSQLProbe) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "submission-connect", "submission-connect"))
	require.NoError(t, err)

	workspaceAccess := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	object, err := ss.ExamAttemptWorkspace().ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: workspaceAccess, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	require.NoError(t, err)
	object, err = ss.ExamAttemptWorkspace().MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{
		Access: workspaceAccess, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(),
		Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 7, SHA256: strings.Repeat("b", 64)},
	})
	require.NoError(t, err)
	attemptEntryID := model.NewAttemptWorkspaceEntryID()
	mutation, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{
		Access: workspaceAccess, Operation: model.AttemptWorkspaceMutationCreateFile, EntryID: attemptEntryID,
		DestinationPath: "answer.txt", ObjectID: object.ID,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}, examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "submission-file", "submission-file"))
	require.NoError(t, err)

	access := store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: credentialHash, ExpectedWorkspaceCursor: mutation.Change.Cursor,
		ExpectedCurrentRevisionID: fixture.sitting.ExamRevisionID,
	}
	target, err := submissions.ResolveSealTarget(ctx, access)
	require.NoError(t, err)
	require.NotNil(t, target)
	require.Equal(t, fixture.examID, target.ExamID)
	require.Equal(t, fixture.sitting.ID, target.SittingID)
	require.Equal(t, fixture.class.ID, target.ClassID)
	require.Equal(t, fixture.candidate.ID, target.CandidateUserID)
	require.Equal(t, connected.Workspace.ID, target.WorkspaceID)
	require.False(t, target.SealAt.IsZero())

	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: target.SealAt}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-seal", "submission-seal")
	sealed, err := submissions.Seal(ctx, input, command)
	require.NoError(t, err)
	require.NotNil(t, sealed)
	require.False(t, sealed.Replayed)
	require.Equal(t, input.SubmissionID, sealed.Receipt.SubmissionID)
	require.Equal(t, connected.Attempt.ID, sealed.Receipt.AttemptID)
	require.Equal(t, model.ExamAttemptSubmitted, sealed.Receipt.State)
	require.Equal(t, mutation.Change.Cursor, sealed.Receipt.WorkspaceCursor)
	require.True(t, validStoretestDigest(sealed.Receipt.ManifestDigest))
	require.True(t, sealed.Receipt.SubmittedAt.Equal(input.Notice.Occurrence.CreatedAt))
	require.Equal(t, fixture.examID, sealed.ExamID)
	require.Equal(t, fixture.sitting.ID, sealed.SittingID)
	require.Equal(t, fixture.class.ID, sealed.ClassID)
	require.Equal(t, fixture.candidate.ID, sealed.CandidateUserID)
	require.Equal(t, connected.Participation.ID, sealed.ParticipationID)
	require.Equal(t, connected.Participation.Generation, sealed.Generation)
	require.Equal(t, connected.Connection.ID, sealed.ConnectionID)
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	replayTarget, err := submissions.ResolveSealTarget(ctx, access)
	require.NoError(t, err)
	require.NotNil(t, replayTarget)
	require.True(t, replayTarget.Replayed)
	receiptDelivery, err := ss.Mail().GetDelivery(ctx, input.Notice.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailOccurrenceID(input.SubmissionID.String()), receiptDelivery.OccurrenceID)
	require.Equal(t, model.MailTemplateExamSubmissionReceived, receiptDelivery.TemplateKey)
	require.Equal(t, fixture.candidate.ID, receiptDelivery.TargetUserID)
	audit, err := ss.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	for _, secret := range []string{credentialHash, fixture.session.ID.String(), "cmd/main.go"} {
		require.False(t, bytes.Contains(audit.Result, []byte(secret)), "Submission audit exposed protected value %q: %s", secret, audit.Result)
	}

	authorization, err := submissions.Resolve(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, authorization)
	require.Equal(t, sealed.Receipt.SubmissionID, authorization.SubmissionID)
	require.Equal(t, fixture.examID, authorization.ExamID)
	require.Equal(t, fixture.sitting.ID, authorization.SittingID)
	require.Equal(t, connected.Attempt.ID, authorization.AttemptID)
	require.Equal(t, fixture.unitID, authorization.AcademicUnitID)
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.NotNil(t, header)
	require.Equal(t, sealed.Receipt.SubmissionID, header.ID)
	require.Equal(t, connected.Attempt.ID, header.AttemptID)
	require.Equal(t, connected.Workspace.ID, header.WorkspaceID)
	require.Equal(t, mutation.Change.Cursor, header.WorkspaceCursor)
	require.Equal(t, sealed.Receipt.ManifestDigest, header.ManifestDigest)
	require.Equal(t, 3, header.ManifestEntryCount)
	require.Equal(t, int64(20), header.ManifestTotalFileBytes)
	require.Equal(t, model.SubmissionIntegritySettled, header.IntegrityState)
	require.Equal(t, int64(0), header.UnresolvedIntegrityCount)
	require.True(t, header.SubmittedAt.Equal(sealed.Receipt.SubmittedAt))
	page, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
		SubmissionID: header.ID, Limit: 200,
	})
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Equal(t, header.ID, page.SubmissionID)
	require.Equal(t, header.WorkspaceCursor, page.WorkspaceCursor)
	require.Equal(t, header.ManifestDigest, page.ManifestDigest)
	require.False(t, page.HasMore)
	require.Len(t, page.Items, 3)
	require.Less(t, page.Items[0].EntryID.String(), page.Items[1].EntryID.String())
	require.Less(t, page.Items[1].EntryID.String(), page.Items[2].EntryID.String())
	firstPage, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{SubmissionID: header.ID, Limit: 1})
	require.NoError(t, err)
	require.NotNil(t, firstPage)
	require.True(t, firstPage.HasMore)
	require.Len(t, firstPage.Items, 1)
	require.Equal(t, page.Items[0], firstPage.Items[0])
	secondPage, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
		SubmissionID: header.ID, AfterEntryID: firstPage.Items[0].EntryID, Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, secondPage)
	require.True(t, secondPage.HasMore)
	require.Len(t, secondPage.Items, 1)
	require.Equal(t, page.Items[1], secondPage.Items[0])
	var starterFile, attemptFile, directory store.ExamSubmissionManifestItem
	for _, item := range page.Items {
		switch item.Kind {
		case model.StarterWorkspaceEntryFile:
			if item.EntryID == attemptEntryID {
				attemptFile = item
			} else {
				starterFile = item
			}
		case model.StarterWorkspaceEntryDirectory:
			directory = item
		}
	}
	selector, err := submissions.ResolveFile(ctx, header.ID, starterFile.EntryID)
	require.NoError(t, err)
	require.NotNil(t, selector)
	require.Equal(t, starterFile, selector.Entry)
	require.Equal(t, model.AttemptWorkspaceStorageStarter, selector.StorageOrigin)
	require.True(t, selector.StarterObjectID.IsValid())
	require.True(t, selector.AttemptObjectID.IsZero())
	require.Equal(t, starterFile.ContentVersion, selector.ContentVersion)
	attemptSelector, err := submissions.ResolveFile(ctx, header.ID, attemptFile.EntryID)
	require.NoError(t, err)
	require.NotNil(t, attemptSelector)
	require.Equal(t, attemptFile, attemptSelector.Entry)
	require.Equal(t, model.AttemptWorkspaceStorageAttempt, attemptSelector.StorageOrigin)
	require.True(t, attemptSelector.AttemptObjectID.IsValid())
	require.True(t, attemptSelector.StarterObjectID.IsZero())
	require.Equal(t, object.ID, attemptSelector.AttemptObjectID)
	require.Equal(t, object.ContentVersion, attemptSelector.ContentVersion)
	_, err = submissions.ResolveFile(ctx, header.ID, directory.EntryID)
	require.True(t, store.IsNotFound(err), "ResolveFile(directory) error")

	_, err = ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: store.CandidateAttemptAccess{
		AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
	}, ExpectedCursor: -1, Limit: 200})
	require.True(t, store.IsNotFound(err), "candidate Workspace read after Submission error")
	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptSubmitted, manager.Attempt.State)
	require.NotNil(t, manager.LatestParticipation)
	require.Equal(t, model.AttemptParticipationEnded, manager.LatestParticipation.State)
	require.Equal(t, model.AttemptParticipationEndSubmitted, manager.LatestParticipation.EndReason)
	require.Nil(t, manager.CurrentConnection)

	replay := *input
	replay.SubmissionID = model.NewSubmissionID()
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	replayed, err := submissions.Seal(ctx, &replay, command)
	require.NoError(t, err)
	require.NotNil(t, replayed, "Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	require.True(t, replayed.Replayed, "Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.Receipt, replayed.Receipt, "Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.ParticipationID, replayed.ParticipationID, "Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.ConnectionID, replayed.ConnectionID, "Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	receipts, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamSubmissionReceived}, Limit: 200})
	require.NoError(t, err)
	matchingReceipts := 0
	for _, delivery := range receipts {
		if delivery.OccurrenceID == model.MailOccurrenceID(input.SubmissionID.String()) {
			matchingReceipts++
		}
		require.NotEqual(t, model.MailOccurrenceID(replay.SubmissionID.String()), delivery.OccurrenceID)
	}
	require.Equal(t, 1, matchingReceipts, "voluntary receipt count=%d, deliveries=%#v", matchingReceipts, receipts)

	different := *input
	different.SubmissionID = model.NewSubmissionID()
	different.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, err = submissions.Seal(ctx, &different,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-second", "submission-second"))
	require.Error(t, err, "Seal(different key after terminal state) succeeded")
	got, getErr := submissions.Get(ctx, different.SubmissionID)
	require.True(t, store.IsNotFound(getErr), "Get(uncommitted second Submission)")
	require.Nil(t, got, "Get(uncommitted second Submission)")

	testExamSubmissionRollback(t, ctx, ss, submissions)
	testExamSubmissionIntegrityGap(t, ctx, ss, submissions, probes...)
	testExamSubmissionHistoricalIntegrityGap(t, ctx, ss, submissions)
	testExamSubmissionAccessFences(t, ctx, ss, submissions)
	testExamCorrectionAcknowledgement(t, ctx, ss, submissions)
	testManagerEndedExamSubmission(t, ctx, ss, submissions)
	testAutomaticExamSubmissionSealing(t, ctx, ss, submissions, probes...)

	if len(probes) != 0 {
		probe := probes[0]
		if probe.ConcurrentPeer != nil {
			testIndependentExamSubmissionReplay(t, ctx, ss, submissions, probe.ConcurrentPeer)
			testConcurrentExamSubmission(t, ctx, ss, submissions, probe.ConcurrentPeer)
		}
		if probe.AssertSchema != nil {
			probe.AssertSchema(t, ctx)
		}
		if probe.AssertRetentionFence != nil {
			probe.AssertRetentionFence(t, ctx, header.ID)
		}
		if probe.CorruptManifest != nil {
			probe.CorruptManifest(t, ctx, header.ID)
			_, getErr := submissions.Get(ctx, header.ID)
			require.Error(t, getErr, "Get(corrupt immutable manifest) succeeded")
		}
	}
}

func testExamCorrectionAcknowledgement(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore,
) {
	t.Helper()
	t.Run("Correction acknowledgement is ordered, pause-safe, replayable, and terminally fenced", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "correction-acknowledgement-connect")
		firstInstructions := "First corrected instructions"
		firstAudit := saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID)
		first, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{
			RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: fixture.sitting.Revision,
			ActorUserID: fixture.manager.ID, InstructionsMarkdown: &firstInstructions,
			AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "The first correction changes the instructions.", AcknowledgementRequired: true,
			PrivateReason: "exercise ordered acknowledgement", AppliedAt: model.NowUTC(),
			AuditEventID: firstAudit.ID, AuditAt: model.NowUTC(),
		}, examCommand(fixture.manager.ID, "exam.correction.apply", "correction-acknowledgement-first", "correction-acknowledgement-first"))
		require.NoError(t, err)
		secondInstructions := "Second corrected instructions"
		secondAudit := saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID)
		second, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{
			RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			CurrentRevisionID: first.Revision.ID, ExpectedSittingRevision: first.Sitting.Sitting.Revision,
			ActorUserID: fixture.manager.ID, InstructionsMarkdown: &secondInstructions,
			AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "The second correction changes the instructions again.", AcknowledgementRequired: true,
			PrivateReason: "exercise ordered acknowledgement", AppliedAt: model.NowUTC(),
			AuditEventID: secondAudit.ID, AuditAt: model.NowUTC(),
		}, examCommand(fixture.manager.ID, "exam.correction.apply", "correction-acknowledgement-second", "correction-acknowledgement-second"))
		require.NoError(t, err)

		candidateAccess := store.CandidateAttemptAccess{
			AttemptID: connected.Attempt.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
			ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focusAccess.ContinuityCredentialHash,
		}
		presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, candidateAccess)
		require.NoError(t, err)
		require.True(t, presentation.RuntimeCapabilities.ExamRevision.AcknowledgementRequired)
		require.Len(t, presentation.LiveCorrections, 2)
		require.Equal(t, first.Revision.ID, presentation.LiveCorrections[0].RevisionID)
		require.Equal(t, second.Revision.ID, presentation.LiveCorrections[1].RevisionID)
		require.Equal(t, model.CorrectionAcknowledgementPending, presentation.LiveCorrections[0].AcknowledgementState)
		require.Equal(t, model.CorrectionAcknowledgementPending, presentation.LiveCorrections[1].AcknowledgementState)

		newAcknowledgement := func(revisionID, currentRevisionID model.ExamRevisionID) *store.ExamAttemptCorrectionAcknowledgement {
			return &store.ExamAttemptCorrectionAcknowledgement{Access: candidateAccess,
				ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
				CorrectionRevisionID: revisionID, ExpectedCurrentRevisionID: currentRevisionID,
				AuditEvent: newExamAttemptAudit(fixture)}
		}
		stale := newAcknowledgement(first.Revision.ID, first.Revision.ID)
		_, err = ss.ExamAttempt().ResolveCorrectionAcknowledgementTarget(ctx, *stale)
		requireConflictConstraint(t, err, "exam_sitting_revision_selection")
		outOfOrder := newAcknowledgement(second.Revision.ID, second.Revision.ID)
		_, err = ss.ExamAttempt().AcknowledgeCorrection(ctx, outOfOrder,
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-out-of-order", "correction-acknowledgement-out-of-order"))
		requireConflictConstraint(t, err, "exam_correction_acknowledgement_order")

		blockedAccess := submissionAccess(focusAccess, second.Revision.ID, connected.Workspace.Cursor, 0)
		blockedSubmission := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: blockedAccess,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		attachSubmissionReceipt(t, ctx, ss, fixture.candidate, blockedSubmission)
		_, err = submissions.Seal(ctx, blockedSubmission,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
				"correction-acknowledgement-blocked-submission", "correction-acknowledgement-blocked-submission"))
		requireConflictConstraint(t, err, "exam_correction_acknowledgement_required")

		paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: second.Sitting.Sitting.Revision,
			PrivateReason: "prove acknowledgement remains allowed while paused", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.pause", "correction-acknowledgement-pause", "correction-acknowledgement-pause"))
		require.NoError(t, err)
		firstInputs := [2]*store.ExamAttemptCorrectionAcknowledgement{
			newAcknowledgement(first.Revision.ID, second.Revision.ID),
			newAcknowledgement(first.Revision.ID, second.Revision.ID),
		}
		firstCommands := [2]*store.CommandIdempotency{
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-first-ack-a", "correction-acknowledgement-first-ack"),
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-first-ack-b", "correction-acknowledgement-first-ack"),
		}
		target, err := ss.ExamAttempt().ResolveCorrectionAcknowledgementTarget(ctx, *firstInputs[0])
		require.NoError(t, err)
		require.NotNil(t, target)
		require.Equal(t, connected.Attempt.ID, target.AttemptID)
		require.Equal(t, first.Revision.ID, target.CorrectionRevisionID)
		require.Equal(t, second.Revision.ID, target.CurrentRevisionID)
		require.Equal(t, fixture.candidate.ID, target.CandidateUserID)
		type acknowledgementOutcome struct {
			index  int
			result *store.ExamAttemptCorrectionAcknowledgementResult
			err    error
		}
		outcomes := make(chan acknowledgementOutcome, len(firstInputs))
		var acknowledgements sync.WaitGroup
		for index := range firstInputs {
			acknowledgements.Add(1)
			go func(index int) {
				defer acknowledgements.Done()
				result, acknowledgeErr := ss.ExamAttempt().AcknowledgeCorrection(ctx, firstInputs[index], firstCommands[index])
				outcomes <- acknowledgementOutcome{index: index, result: result, err: acknowledgeErr}
			}(index)
		}
		acknowledgements.Wait()
		close(outcomes)
		var acknowledged, duplicateAcknowledgement *store.ExamAttemptCorrectionAcknowledgementResult
		winnerIndex, fresh, duplicates := -1, 0, 0
		for outcome := range outcomes {
			require.NoError(t, outcome.err)
			require.NotNil(t, outcome.result, "concurrent AcknowledgeCorrection() returned nil")
			if outcome.result.Duplicate {
				duplicates++
				duplicateAcknowledgement = outcome.result
			} else {
				fresh++
				winnerIndex, acknowledged = outcome.index, outcome.result
			}
		}
		require.Equal(t, 1, fresh, "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, 1, duplicates, "concurrent AcknowledgeCorrection() fresh")
		require.NotNil(t, acknowledged, "concurrent AcknowledgeCorrection() fresh")
		require.NotNil(t, duplicateAcknowledgement, "concurrent AcknowledgeCorrection() fresh")
		require.False(t, acknowledged.Replayed, "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, connected.Attempt.ID, acknowledged.AttemptID, "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, first.Revision.ID, acknowledged.CorrectionRevisionID, "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, second.Revision.ID, acknowledged.CurrentRevisionID, "concurrent AcknowledgeCorrection() fresh")
		require.False(t, acknowledged.AcknowledgedAt.IsZero(), "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, acknowledged.AcknowledgedAt, duplicateAcknowledgement.AcknowledgedAt, "concurrent AcknowledgeCorrection() fresh")
		require.Equal(t, acknowledged.MutationAuditEventID, duplicateAcknowledgement.MutationAuditEventID, "concurrent AcknowledgeCorrection() fresh")
		requireSuccessfulAudit(t, ctx, ss, acknowledged.MutationAuditEventID)

		replayInput := newAcknowledgement(first.Revision.ID, second.Revision.ID)
		replayed, err := ss.ExamAttempt().AcknowledgeCorrection(ctx, replayInput, firstCommands[winnerIndex])
		require.NoError(t, err)
		require.NotNil(t, replayed, "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		require.True(t, replayed.Replayed, "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		require.False(t, replayed.Duplicate, "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		require.Equal(t, acknowledged.AcknowledgedAt, replayed.AcknowledgedAt, "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		require.Equal(t, acknowledged.MutationAuditEventID, replayed.MutationAuditEventID, "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		require.True(t, replayInput.AuditEvent.ID.IsZero(), "AcknowledgeCorrection(exact replay) = %#v, first=%#v", replayed, acknowledged)
		replayAudit, err := ss.Audit().Get(ctx, replayed.MutationAuditEventID)
		require.NoError(t, err)
		require.False(t, bytes.Contains(replayAudit.Result, []byte(`"idempotency_replayed"`)))
		require.False(t, bytes.Contains(replayAudit.Result, []byte(focusAccess.ContinuityCredentialHash)))
		require.False(t, bytes.Contains(replayAudit.Result, []byte(fixture.session.ID.String())))

		duplicateInput := newAcknowledgement(first.Revision.ID, second.Revision.ID)
		duplicate, err := ss.ExamAttempt().AcknowledgeCorrection(ctx, duplicateInput,
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-alternate-key", "correction-acknowledgement-first-ack"))
		require.NoError(t, err)
		require.NotNil(t, duplicate)
		require.True(t, duplicate.Duplicate)
		require.False(t, duplicate.Replayed)
		require.Equal(t, acknowledged.AcknowledgedAt, duplicate.AcknowledgedAt)

		secondInput := newAcknowledgement(second.Revision.ID, second.Revision.ID)
		secondAcknowledged, err := ss.ExamAttempt().AcknowledgeCorrection(ctx, secondInput,
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-second-ack", "correction-acknowledgement-second-ack"))
		require.NoError(t, err)
		require.NotNil(t, secondAcknowledged)
		require.False(t, secondAcknowledged.Duplicate)
		require.False(t, secondAcknowledged.Replayed)
		presentation, err = ss.ExamAttempt().GetCandidatePresentation(ctx, candidateAccess)
		require.NoError(t, err)
		require.False(t, presentation.RuntimeCapabilities.ExamRevision.AcknowledgementRequired)
		require.Equal(t, model.CorrectionAcknowledgementAcknowledged, presentation.LiveCorrections[0].AcknowledgementState)
		require.Equal(t, model.CorrectionAcknowledgementAcknowledged, presentation.LiveCorrections[1].AcknowledgementState)

		pausedSubmission := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: blockedAccess,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		attachSubmissionReceipt(t, ctx, ss, fixture.candidate, pausedSubmission)
		_, err = submissions.Seal(ctx, pausedSubmission,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
				"correction-acknowledgement-paused-submission", "correction-acknowledgement-paused-submission"))
		requireConflictConstraint(t, err, "exam_sitting_state")
		resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision,
			PrivateReason: "finish correction acknowledgement conformance", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.resume", "correction-acknowledgement-resume", "correction-acknowledgement-resume"))
		require.NoError(t, err)
		require.Equal(t, model.ExamSittingOpen, resumed.Value.Sitting.State)
		thirdInstructions := "Third corrected instructions"
		third, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{
			RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			CurrentRevisionID: second.Revision.ID, ExpectedSittingRevision: resumed.Value.Sitting.Revision,
			ActorUserID: fixture.manager.ID, InstructionsMarkdown: &thirdInstructions,
			AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "A later correction changes the instructions without requiring acknowledgement.",
			PrivateReason: "prove retained acknowledgement replay after a later correction", AppliedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.NowUTC(),
		}, examCommand(fixture.manager.ID, "exam.correction.apply",
			"correction-acknowledgement-third", "correction-acknowledgement-third"))
		require.NoError(t, err)
		afterCorrectionReplay := newAcknowledgement(first.Revision.ID, second.Revision.ID)
		replayed, err = ss.ExamAttempt().AcknowledgeCorrection(ctx, afterCorrectionReplay, firstCommands[winnerIndex])
		require.NoError(t, err)
		require.NotNil(t, replayed)
		require.True(t, replayed.Replayed)
		require.False(t, replayed.Duplicate)
		require.Equal(t, second.Revision.ID, replayed.CurrentRevisionID)
		require.Equal(t, acknowledged.AcknowledgedAt, replayed.AcknowledgedAt)
		afterCorrectionDuplicate := newAcknowledgement(first.Revision.ID, second.Revision.ID)
		duplicate, err = ss.ExamAttempt().AcknowledgeCorrection(ctx, afterCorrectionDuplicate,
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-after-later-correction", "correction-acknowledgement-first-ack"))
		require.NoError(t, err)
		require.NotNil(t, duplicate)
		require.True(t, duplicate.Duplicate)
		require.False(t, duplicate.Replayed)
		require.Equal(t, second.Revision.ID, duplicate.CurrentRevisionID)
		require.Equal(t, acknowledged.AcknowledgedAt, duplicate.AcknowledgedAt)
		blockedAccess.ExpectedCurrentRevisionID = third.Revision.ID
		terminalInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: blockedAccess,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		attachSubmissionReceipt(t, ctx, ss, fixture.candidate, terminalInput)
		sealed, err := submissions.Seal(ctx, terminalInput,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
				"correction-acknowledgement-terminal-submission", "correction-acknowledgement-terminal-submission"))
		require.NoError(t, err)
		require.Equal(t, third.Revision.ID, sealed.Receipt.ExamRevisionID)
		terminalAcknowledgement := newAcknowledgement(first.Revision.ID, second.Revision.ID)
		_, err = ss.ExamAttempt().AcknowledgeCorrection(ctx, terminalAcknowledgement,
			examCommand(fixture.candidate.ID, store.ExamAttemptCorrectionAcknowledgementOperation,
				"correction-acknowledgement-after-terminal", "correction-acknowledgement-after-terminal"))
		require.Error(t, err, "AcknowledgeCorrection(after Submission) succeeded")
	})
}

func testManagerEndedExamSubmission(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore,
) {
	t.Helper()
	t.Run("Manager end seals without an integrity judgment", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, _ := connectFocusLossFixture(t, ctx, ss, fixture, "manager-end-connect")
		request := store.ExamSubmissionManagerEndRequest{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			AttemptID: connected.Attempt.ID, ActorUserID: fixture.manager.ID,
			ExpectedAttemptRevision: connected.Attempt.Revision, PrivateReason: "candidate requested an assisted early end"}
		selfRequest := request
		selfRequest.ActorUserID = fixture.candidate.ID
		selfRequest.ManagerOverride = true
		selfPreparation, selfErr := submissions.PrepareManagerEnd(ctx, selfRequest)
		require.Nil(t, selfPreparation, "PrepareManagerEnd(self)")
		require.True(t, store.IsNotFound(selfErr), "PrepareManagerEnd(self)")
		preparation, err := submissions.PrepareManagerEnd(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, preparation)
		require.False(t, preparation.Replayed)
		require.Equal(t, connected.Attempt.ID, preparation.Target.AttemptID)
		require.Equal(t, fixture.sitting.ExamRevisionID, preparation.Target.CurrentRevisionID)
		require.False(t, preparation.SealAt.IsZero())
		audit := saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID)
		input := &store.ExamSubmissionManagerEnd{Request: request, Target: preparation.Target,
			SubmissionID: model.NewSubmissionID(), AuditEventID: audit.ID, AuditAt: preparation.SealAt}
		attachManagerEndedSubmissionReceipt(t, fixture.candidate, input)
		command := examCommand(fixture.manager.ID, store.ExamSubmissionManagerEndOperation, "manager-end-once", "manager-end-once")
		ended, err := submissions.EndByManager(ctx, input, command)
		require.NoError(t, err)
		require.NotNil(t, ended)
		require.False(t, ended.Replayed)
		require.True(t, ended.ConnectionClosed)
		require.Equal(t, input.SubmissionID, ended.Receipt.SubmissionID)
		require.Equal(t, connected.Attempt.ID, ended.Receipt.AttemptID)
		require.Equal(t, fixture.sitting.ExamRevisionID, ended.Receipt.ExamRevisionID)
		header, err := submissions.Get(ctx, ended.Receipt.SubmissionID)
		require.NoError(t, err)
		require.Equal(t, model.ExamSubmissionManagerEndedAttempt, header.Provenance)
		require.Equal(t, model.SubmissionIntegrityGapped, header.IntegrityState)
		require.Equal(t, int64(1), header.UnresolvedIntegrityCount)
		discrepancies, err := ss.ExamIntegrityReview().ListDiscrepancies(ctx, store.ExamIntegrityDiscrepancyListOptions{
			SubmissionID: header.ID, Limit: store.ExamIntegrityReviewDiscrepancyReadMaximum,
		})
		require.NoError(t, err)
		require.Len(t, discrepancies.Items, 1)
		require.Equal(t, model.IntegrityDiscrepancyFocusLossGap, discrepancies.Items[0].Kind)
		require.Equal(t, string(model.IntegrityDiscrepancyFocusLossSourceNotFinalized), discrepancies.Items[0].GapReason)
		require.Equal(t, int64(1), discrepancies.Items[0].UnresolvedCount)
		managed, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.ExamAttemptSubmitted, managed.Attempt.State)
		require.NotNil(t, managed.LatestParticipation)
		require.Equal(t, model.AttemptParticipationEndManagerEnded, managed.LatestParticipation.EndReason)
		require.Nil(t, managed.CurrentConnection)
		delivery, err := ss.Mail().GetDelivery(ctx, input.Notice.Delivery.ID)
		require.NoError(t, err)
		require.Equal(t, model.MailTemplateExamSubmissionManagerEnded, delivery.TemplateKey)
		replayPreparation, err := submissions.PrepareManagerEnd(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, replayPreparation)
		require.True(t, replayPreparation.Replayed)
		replay := &store.ExamSubmissionManagerEnd{Request: request, Target: replayPreparation.Target,
			SubmissionID: model.NewSubmissionID(), AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID,
				fixture.examID, fixture.unitID).ID, AuditAt: replayPreparation.SealAt}
		replayed, err := submissions.EndByManager(ctx, replay, command)
		require.NoError(t, err)
		require.NotNil(t, replayed)
		require.True(t, replayed.Replayed)
		require.False(t, replayed.ConnectionClosed)
		require.Equal(t, ended.Receipt, replayed.Receipt)
		receipts, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
			TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamSubmissionManagerEnded}, Limit: 200})
		require.NoError(t, err)
		require.Len(t, receipts, 1, "manager-ended receipt count")
	})
}

func testAutomaticExamSubmissionSealing(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore, probes ...ExamSubmissionSQLProbe,
) {
	t.Helper()
	t.Run("Closing Sitting sealing and no-shows", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		active, activeAccess := connectFocusLossFixture(t, ctx, ss, fixture, "automatic-active-connect")

		suspendedFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(-time.Minute))
		suspended, suspendedAccess := connectFocusLossFixture(t, ctx, ss, suspendedFixture, "automatic-suspended-connect")
		suspension := recordFocusLoss(t, ctx, ss, suspendedFixture, suspendedAccess, 1, 500,
			model.FocusLossSourceApplicationBackgrounded)
		require.NotNil(t, suspension.Attempt)
		require.Equal(t, model.ExamAttemptSuspended, suspension.Attempt.State)
		require.NotNil(t, suspension.Participation)
		require.Equal(t, model.AttemptParticipationEndPolicySuspended, suspension.Participation.EndReason)

		submittedFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(-time.Minute))
		submitted, submittedAccess := connectFocusLossFixture(t, ctx, ss, submittedFixture, "automatic-submitted-connect")
		submittedSealAccess := submissionAccess(submittedAccess, submittedFixture.sitting.ExamRevisionID, submitted.Workspace.Cursor, 0)
		submittedInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: submittedSealAccess,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, submittedFixture).ID, AuditAt: model.NowUTC()}
		attachSubmissionReceipt(t, ctx, ss, submittedFixture.candidate, submittedInput)
		alreadySubmitted, err := submissions.Seal(ctx, submittedInput,
			examCommand(submittedFixture.candidate.ID, store.ExamSubmissionSealOperation,
				"automatic-existing-submission", "automatic-existing-submission"))
		require.NoError(t, err)

		noShowFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(-time.Minute))
		lateFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(time.Minute))
		renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1,
			AttemptID: active.Attempt.ID, ParticipationID: active.Participation.ID, ConnectionID: active.Connection.ID,
			CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Generation: active.Participation.Generation,
			DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
			Sequence: 1, ContinuityCredentialHash: activeAccess.ContinuityCredentialHash,
		}
		prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
		closeAt := model.NowUTC()
		closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "finish every acknowledged Attempt", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID,
			AuditAt:      model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.close", "automatic-close", "automatic-close"))
		require.NoError(t, err)
		require.True(t, closing.Changed)
		require.Equal(t, model.ExamSittingClosing, closing.Value.Sitting.State)
		_, err = ss.ExamAttempt().RenewParticipation(ctx, renewal)
		requireConflictConstraint(t, err, "exam_sitting_state")

		unfinished, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
			AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID,
			AuditAt:      model.NowUTC()})
		require.NoError(t, err)
		require.False(t, unfinished.Changed)
		require.Equal(t, model.ExamSittingClosing, unfinished.Value.Sitting.State)

		page, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, Limit: 1})
		require.NoError(t, err)
		require.Len(t, page, 1)
		secondPage, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, AfterAttemptID: page[0].AttemptID, Limit: 2})
		require.NoError(t, err)
		allTargets := append(append([]store.ExamSubmissionAutomaticSealTarget(nil), page...), secondPage...)
		require.Len(t, allTargets, 2, "automatic seal target pages = %#v / %#v", page, secondPage)
		require.Less(t, allTargets[0].AttemptID.String(), allTargets[1].AttemptID.String(), "automatic seal target pages = %#v / %#v", page, secondPage)
		wantAttempts := map[model.ExamAttemptID]bool{active.Attempt.ID: true, suspended.Attempt.ID: true}
		for _, target := range allTargets {
			require.True(t, wantAttempts[target.AttemptID], "automatic seal target = %#v, submitted", target)
			require.NotEqual(t, submitted.Attempt.ID, target.AttemptID, "automatic seal target = %#v, submitted", target)
		}

		var activeTarget, suspendedTarget store.ExamSubmissionAutomaticSealTarget
		for _, target := range allTargets {
			if target.AttemptID == active.Attempt.ID {
				activeTarget = target
			} else {
				suspendedTarget = target
			}
		}
		activeResult := raceAutomaticExamSubmissionSeal(t, ctx, ss, submissions, activeTarget, probes...)
		require.True(t, activeResult.ConnectionClosed)
		automaticPreparation, replayErr := submissions.PrepareAutomaticSeal(ctx, activeTarget)
		require.NoError(t, replayErr)
		require.NotNil(t, automaticPreparation)
		require.True(t, automaticPreparation.Replayed)
		require.False(t, automaticPreparation.SealAt.IsZero())
		suspendedAudit := saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID)
		suspendedPreparation, preparationErr := submissions.PrepareAutomaticSeal(ctx, suspendedTarget)
		require.NoError(t, preparationErr)
		require.NotNil(t, suspendedPreparation)
		require.False(t, suspendedPreparation.Replayed)
		require.False(t, suspendedPreparation.SealAt.IsZero())
		rollbackInput := &store.ExamSubmissionAutomaticSeal{Target: suspendedTarget, SubmissionID: model.NewSubmissionID(),
			AuditEventID: model.NewAuditEventID(), AuditAt: suspendedPreparation.SealAt}
		attachAutomaticSubmissionReceipt(t, suspendedFixture.candidate, rollbackInput)
		_, rollbackErr := submissions.SealForSittingClose(ctx, rollbackInput)
		require.Error(t, rollbackErr, "automatic Seal(with missing audit) succeeded")
		delivery, deliveryErr := ss.Mail().GetDelivery(ctx, rollbackInput.Notice.Delivery.ID)
		require.True(t, store.IsNotFound(deliveryErr), "automatic rolled-back receipt")
		require.Nil(t, delivery, "automatic rolled-back receipt")
		suspendedInput := &store.ExamSubmissionAutomaticSeal{
			Target: suspendedTarget, SubmissionID: model.NewSubmissionID(), AuditEventID: suspendedAudit.ID,
			AuditAt: suspendedPreparation.SealAt}
		attachAutomaticSubmissionReceipt(t, suspendedFixture.candidate, suspendedInput)
		suspendedResult, err := submissions.SealForSittingClose(ctx, suspendedInput)
		require.NoError(t, err)
		require.False(t, suspendedResult.Replayed)
		require.False(t, suspendedResult.ConnectionClosed)
		automaticReceipts, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
			TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamSubmissionAutomaticallySealed}, Limit: 200})
		require.NoError(t, listErr)
		require.Len(t, automaticReceipts, 2)
		requireSuccessfulAudit(t, ctx, ss, suspendedAudit.ID)

		for _, result := range []*store.ExamSubmissionAutomaticSealResult{activeResult, suspendedResult} {
			header, getErr := submissions.Get(ctx, result.Receipt.SubmissionID)
			require.NoError(t, getErr)
			require.Equal(t, model.SubmissionIntegrityGapped, header.IntegrityState)
			require.GreaterOrEqual(t, header.UnresolvedIntegrityCount, int64(1))
			require.Equal(t, result.Receipt.WorkspaceCursor, header.WorkspaceCursor)
			require.Equal(t, result.Receipt.ManifestDigest, header.ManifestDigest)
			manifest, listErr := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
				SubmissionID: header.ID, Limit: model.ExamSubmissionManifestReadMaximum})
			require.NoError(t, listErr)
			require.Len(t, manifest.Items, 2)
			require.Equal(t, header.ManifestDigest, manifest.ManifestDigest)
			discrepancies, discrepancyErr := ss.ExamIntegrityReview().ListDiscrepancies(ctx,
				store.ExamIntegrityDiscrepancyListOptions{SubmissionID: header.ID,
					Limit: store.ExamIntegrityReviewDiscrepancyReadMaximum})
			require.NoError(t, discrepancyErr)
			require.Len(t, discrepancies.Items, 1)
			require.Equal(t, model.IntegrityDiscrepancyFocusLossGap, discrepancies.Items[0].Kind)
			require.Equal(t, string(model.IntegrityDiscrepancyFocusLossSourceNotFinalized), discrepancies.Items[0].GapReason)
			require.Equal(t, int64(1), discrepancies.Items[0].UnresolvedCount)
		}

		activeManager, err := ss.ExamAttempt().Get(ctx, fixture.examID, active.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.ExamAttemptSubmitted, activeManager.Attempt.State)
		require.NotNil(t, activeManager.LatestParticipation)
		require.Equal(t, model.AttemptParticipationEndSittingClosed, activeManager.LatestParticipation.EndReason)
		suspendedManager, err := ss.ExamAttempt().Get(ctx, fixture.examID, suspended.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.ExamAttemptSubmitted, suspendedManager.Attempt.State)
		require.NotNil(t, suspendedManager.LatestParticipation)
		require.Equal(t, model.AttemptParticipationEndPolicySuspended, suspendedManager.LatestParticipation.EndReason)

		remaining, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, Limit: 200})
		require.NoError(t, err)
		require.Empty(t, remaining)
		closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
			AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID,
			AuditAt:      model.NowUTC()})
		require.NoError(t, err)
		require.True(t, closed.Changed)
		require.Equal(t, store.ExamSittingTransitionSealingCompleted, closed.Transition)
		require.Equal(t, model.ExamSittingClosed, closed.Value.Sitting.State)

		noShows, err := ss.ExamSitting().ListNoShows(ctx, store.ExamSittingNoShowListOptions{
			SittingID: fixture.sitting.ID, Limit: 200})
		require.NoError(t, err)
		require.Len(t, noShows, 1, "ListNoShows() = %#v; want=%s late=%s", noShows, noShowFixture.candidate.ID, lateFixture.candidate.ID)
		require.Equal(t, noShowFixture.candidate.ID, noShows[0].CandidateUserID, "ListNoShows() = %#v; want=%s late=%s", noShows, noShowFixture.candidate.ID, lateFixture.candidate.ID)
		existing, getErr := submissions.Get(ctx, alreadySubmitted.Receipt.SubmissionID)
		require.NoError(t, getErr, "existing Submission after automatic sealing = %#v", existing)
		require.Equal(t, alreadySubmitted.Receipt.SubmissionID, existing.ID, "existing Submission after automatic sealing = %#v", existing)
	})
}

func addExamAttemptCandidate(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	startsAt time.Time,
) examAttemptFixture {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: startsAt.Add(-time.Hour)})
	require.NoError(t, err)
	enrollment, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: fixture.class.ID,
		UserID: candidate.ID, StartsAt: startsAt})
	require.NoError(t, err)
	session := saveRegisteredDesktopSession(t, ctx, ss, candidate.ID, fixture.institutionID)
	fixture.candidate, fixture.session, fixture.membership = candidate, session, enrollment.Membership
	return fixture
}

func raceAutomaticExamSubmissionSeal(t *testing.T, ctx context.Context, ss store.Store,
	primary store.ExamSubmissionStore, target store.ExamSubmissionAutomaticSealTarget, probes ...ExamSubmissionSQLProbe,
) *store.ExamSubmissionAutomaticSealResult {
	t.Helper()
	peer := primary
	if len(probes) != 0 && probes[0].ConcurrentPeer != nil {
		peer = probes[0].ConcurrentPeer
	}
	adapters := [2]store.ExamSubmissionStore{primary, peer}
	inputs := [2]*store.ExamSubmissionAutomaticSeal{}
	candidate, err := ss.User().Get(ctx, target.CandidateUserID)
	require.NoError(t, err)
	for index := range inputs {
		audit := saveExamSittingSystemAudit(t, ctx, ss, target.SittingID, target.AcademicUnitID)
		preparation, preparationErr := adapters[index].PrepareAutomaticSeal(ctx, target)
		require.NoError(t, preparationErr)
		require.NotNil(t, preparation)
		require.False(t, preparation.Replayed)
		require.False(t, preparation.SealAt.IsZero())
		inputs[index] = &store.ExamSubmissionAutomaticSeal{Target: target, SubmissionID: model.NewSubmissionID(),
			AuditEventID: audit.ID, AuditAt: preparation.SealAt}
		attachAutomaticSubmissionReceipt(t, candidate, inputs[index])
	}
	start := make(chan struct{})
	type indexedAutomaticResult struct {
		index  int
		result *store.ExamSubmissionAutomaticSealResult
	}
	results := make(chan indexedAutomaticResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, err := adapters[index].SealForSittingClose(ctx, inputs[index])
			results <- indexedAutomaticResult{index: index, result: result}
			errorsFound <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	values := make([]*store.ExamSubmissionAutomaticSealResult, 0, 2)
	for indexed := range results {
		if indexed.result != nil && !indexed.result.Replayed {
			require.True(t, indexed.result.Receipt.SubmittedAt.Equal(inputs[indexed.index].Notice.Occurrence.CreatedAt), "automatic seal time=%s, receipt occurrence", indexed.result.Receipt.SubmittedAt)
		}
		values = append(values, indexed.result)
	}
	require.Len(t, values, 2)
	require.NotNil(t, values[0])
	require.NotNil(t, values[1])
	require.NotEqual(t, values[1].Replayed, values[0].Replayed)
	require.Equal(t, values[1].Receipt, values[0].Receipt)
	require.NotEqual(t, values[1].ConnectionClosed, values[0].ConnectionClosed)
	for _, input := range inputs {
		requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
		audit, err := ss.Audit().Get(ctx, input.AuditEventID)
		require.NoError(t, err)
		require.True(t, audit.ActorID.IsZero())
		require.False(t, audit.SessionID.IsValid())
		encoded := bytes.ToLower(audit.Result)
		for _, forbidden := range []string{"path", "content", "credential", "session", "private_reason"} {
			require.False(t, bytes.Contains(encoded, []byte(forbidden)), "automatic Submission audit contains %q: %s", forbidden, audit.Result)
		}
	}
	if values[0].Replayed {
		return values[1]
	}
	return values[0]
}

func validStoretestDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func testExamSubmissionRollback(t *testing.T, ctx context.Context, ss store.Store, submissions store.ExamSubmissionStore) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-rollback-connect")
	access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-rollback", "submission-rollback")
	_, err := submissions.Seal(ctx, input, command)
	require.Error(t, err, "Seal(with missing audit) succeeded")
	got, err := submissions.Get(ctx, input.SubmissionID)
	require.True(t, store.IsNotFound(err), "Get(rolled-back Submission)")
	require.Nil(t, got, "Get(rolled-back Submission)")
	delivery, err := ss.Mail().GetDelivery(ctx, input.Notice.Delivery.ID)
	require.True(t, store.IsNotFound(err), "GetDelivery(rolled-back voluntary receipt)")
	require.Nil(t, delivery, "GetDelivery(rolled-back voluntary receipt)")
	_, err = submissions.ResolveSealTarget(ctx, access)
	require.NoError(t, err)
	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptActive, manager.Attempt.State)
	require.NotNil(t, manager.LatestParticipation)
	require.Equal(t, model.AttemptParticipationActive, manager.LatestParticipation.State)
	require.NotNil(t, manager.CurrentConnection)
	require.Equal(t, model.AttemptConnectionOpen, manager.CurrentConnection.State)
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	retried, err := submissions.Seal(ctx, input, command)
	require.NoError(t, err)
	require.NotNil(t, retried)
	require.False(t, retried.Replayed)
	require.Equal(t, input.SubmissionID, retried.Receipt.SubmissionID)
}

func testExamSubmissionIntegrityGap(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore, probes ...ExamSubmissionSQLProbe,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-integrity-connect")
	gap := recordFocusLoss(t, ctx, ss, fixture, focusAccess, 2, 2000, model.FocusLossSourceWindowBlur)
	require.Equal(t, int64(2), gap.AcceptedSequence)
	require.Equal(t, int64(1), gap.MissingBefore)
	require.True(t, gap.Qualified)
	require.False(t, gap.ThresholdCrossed)
	stale := store.ExamSubmissionSealAccess(submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 1))
	staleInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: stale,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	if _, err := submissions.Seal(ctx, staleInput,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-stale-focus", "submission-stale-focus")); err == nil {
		require.FailNow(t, "Seal(with final Focus Loss sequence behind accepted high-water) succeeded")
	} else {
		requireConflictConstraint(t, err, "focus_loss_sequence")
	}
	access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 5)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	sealed, err := submissions.Seal(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-gapped", "submission-gapped"))
	require.NoError(t, err)
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, int64(5), header.FinalFocusLossSequence)
	require.Equal(t, model.SubmissionIntegrityGapped, header.IntegrityState)
	require.Equal(t, int64(4), header.UnresolvedIntegrityCount)
	if len(probes) != 0 && probes[0].IntegrityPersistence != nil {
		persisted := probes[0].IntegrityPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		require.Equal(t, int64(0), persisted.PendingQualifiers)
		require.Equal(t, int64(4), persisted.UnresolvedMissing)
	}
	late := newFocusLossInput(t, ctx, ss, fixture, focusAccess, 3, 2000, model.FocusLossSourceDocumentHidden)
	_, err = ss.ExamAttempt().RecordFocusLoss(ctx, late)
	require.Error(t, err, "RecordFocusLoss(after Submission) succeeded")
}

func testExamSubmissionHistoricalIntegrityGap(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore,
) {
	t.Helper()
	policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
	fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
	connected, firstAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-history-connect")
	crossed := recordFocusLoss(t, ctx, ss, fixture, firstAccess, 2, 500, model.FocusLossSourceWindowBlur)
	require.Equal(t, int64(1), crossed.MissingBefore)
	require.True(t, crossed.ThresholdCrossed)
	require.NotNil(t, crossed.Suspension)
	require.NotNil(t, crossed.Attempt)
	require.Equal(t, model.ExamAttemptSuspended, crossed.Attempt.State)
	reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, SuspensionID: crossed.Suspension.ID,
		ActorUserID: fixture.manager.ID, ExpectedRevision: crossed.Attempt.Revision,
		PrivateReason: "verify Submission retains uncertainty across Participation generations",
		AuditEventID:  saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "submission-history-reallow", "submission-history-reallow"))
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptReady, reallowed.Attempt.State)
	require.True(t, reallowed.FocusLossWindowReset)
	secondCredentialHash := model.HashToken(model.NewCredentialToken())
	reconnect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID:   model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: secondCredentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, reconnect)
	reconnected, err := ss.ExamAttempt().Connect(ctx, reconnect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "submission-history-reconnect", "submission-history-reconnect"))
	require.NoError(t, err)
	require.Equal(t, connected.Attempt.ID, reconnected.Attempt.ID)
	require.Equal(t, connected.Participation.Generation+1, reconnected.Participation.Generation)
	access := store.ExamSubmissionSealAccess{AttemptID: reconnected.Attempt.ID,
		ParticipationID: reconnected.Participation.ID, Generation: reconnected.Participation.Generation,
		ConnectionID: reconnected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: secondCredentialHash, ExpectedWorkspaceCursor: reconnected.Workspace.Cursor,
		ExpectedCurrentRevisionID: fixture.sitting.ExamRevisionID, FinalFocusLossSequence: 2,
	}
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	sealed, err := submissions.Seal(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-history-seal", "submission-history-seal"))
	require.NoError(t, err)
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionIntegrityGapped, header.IntegrityState)
	require.Equal(t, int64(3), header.UnresolvedIntegrityCount)
	require.Equal(t, int64(2), header.FinalFocusLossSequence)
}

func testExamSubmissionAccessFences(t *testing.T, ctx context.Context, ss store.Store, submissions store.ExamSubmissionStore) {
	t.Helper()

	t.Run("stale Workspace cursor", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-stale-cursor-connect")
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor+1, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err := submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-stale-cursor", "submission-stale-cursor"))
		requireConflictConstraint(t, err, "attempt_workspace_cursor")
	})

	t.Run("closed Connection", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-closed-connect")
		_, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{
			ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			Reason:       model.AttemptConnectionCloseTransport,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
		})
		require.NoError(t, err)
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-closed", "submission-closed"))
		requireConflictConstraint(t, err, "attempt_connection_closed")
	})

	t.Run("Paused Sitting", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-paused-connect")
		_, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			PrivateReason: "verify voluntary Submission rejects Paused Sitting", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.pause", "submission-pause", "submission-pause"))
		require.NoError(t, err)
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-paused", "submission-paused"))
		requireConflictConstraint(t, err, "exam_sitting_state")
	})

	t.Run("Closing Sitting", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-closing-connect")
		closeAt := model.NowUTC()
		_, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "verify voluntary Submission rejects Closing Sitting", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
			examCommand(fixture.manager.ID, "exam.sitting.close", "submission-close", "submission-close"))
		require.NoError(t, err)
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-closing", "submission-closing"))
		requireConflictConstraint(t, err, "exam_sitting_state")
		got, getErr := submissions.Get(ctx, input.SubmissionID)
		require.True(t, store.IsNotFound(getErr), "Get(Closing-denied Submission)")
		require.Nil(t, got, "Get(Closing-denied Submission)")
	})

	t.Run("Suspended Attempt", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-suspended-connect")
		crossed := recordFocusLoss(t, ctx, ss, fixture, focusAccess, 1, 500, model.FocusLossSourceWindowBlur)
		require.NotNil(t, crossed.Attempt)
		require.Equal(t, model.ExamAttemptSuspended, crossed.Attempt.State)
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 1)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err := submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-suspended", "submission-suspended"))
		require.Error(t, err, "Seal(Suspended Attempt) succeeded")
		got, getErr := submissions.Get(ctx, input.SubmissionID)
		require.True(t, store.IsNotFound(getErr), "Get(Suspended-denied Submission)")
		require.Nil(t, got, "Get(Suspended-denied Submission)")
		manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.ExamAttemptSuspended, manager.Attempt.State)
	})

	t.Run("lost exact Class membership", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-membership-connect")
		_, err := ss.ClassMember().SeedEnd(ctx, fixture.membership.ID, fixture.membership.Revision, model.NowUTC())
		require.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-membership", "submission-membership"))
		require.True(t, store.IsNotFound(err), "Seal(after membership loss) error")
	})
}

func testIndependentExamSubmissionReplay(t *testing.T, ctx context.Context, ss store.Store,
	first, second store.ExamSubmissionStore,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-peer-replay-connect")
	access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
		"submission-peer-replay", "submission-peer-replay")
	sealed, err := first.Seal(ctx, input, command)
	require.NoError(t, err)
	replay := *input
	replay.SubmissionID = model.NewSubmissionID()
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	replayed, err := second.Seal(ctx, &replay, command)
	require.NoError(t, err)
	require.NotNil(t, replayed, "Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	require.True(t, replayed.Replayed, "Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.Receipt, replayed.Receipt, "Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.ParticipationID, replayed.ParticipationID, "Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	require.Equal(t, sealed.ConnectionID, replayed.ConnectionID, "Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
}

func testConcurrentExamSubmission(t *testing.T, ctx context.Context, ss store.Store,
	first, second store.ExamSubmissionStore,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-concurrent-connect")
	access := submissionAccess(focusAccess, fixture.sitting.ExamRevisionID, connected.Workspace.Cursor, 0)
	inputs := [2]*store.ExamSubmissionSeal{}
	commands := [2]*store.CommandIdempotency{}
	for index := range inputs {
		inputs[index] = &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		attachSubmissionReceipt(t, ctx, ss, fixture.candidate, inputs[index])
		commands[index] = examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
			"submission-concurrent-"+string(rune('a'+index)), "submission-concurrent-"+string(rune('a'+index)))
	}
	adapters := [2]store.ExamSubmissionStore{first, second}
	start := make(chan struct{})
	results := make(chan *store.ExamSubmissionSealResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, err := adapters[index].Seal(ctx, inputs[index], commands[index])
			results <- result
			errorsFound <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	succeeded := make([]*store.ExamSubmissionSealResult, 0, 1)
	failed := 0
	for err := range errorsFound {
		if err != nil {
			failed++
		}
	}
	for result := range results {
		if result != nil {
			succeeded = append(succeeded, result)
		}
	}
	require.Len(t, succeeded, 1, "concurrent different-key Seals")
	require.Equal(t, 1, failed, "concurrent different-key Seals")
	require.False(t, succeeded[0].Replayed, "concurrent different-key Seals")
	losingID := inputs[0].SubmissionID
	if losingID == succeeded[0].Receipt.SubmissionID {
		losingID = inputs[1].SubmissionID
	}
	got, err := first.Get(ctx, losingID)
	require.True(t, store.IsNotFound(err), "Get(concurrent losing Submission)")
	require.Nil(t, got, "Get(concurrent losing Submission)")
}

func submissionAccess(access store.ExamAttemptFocusLossAccess, currentRevisionID model.ExamRevisionID,
	workspaceCursor, finalFocusLossSequence int64,
) store.ExamSubmissionSealAccess {
	return store.ExamSubmissionSealAccess{AttemptID: access.AttemptID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID,
		SessionID: access.SessionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
		ExpectedCurrentRevisionID: currentRevisionID, ExpectedWorkspaceCursor: workspaceCursor,
		FinalFocusLossSequence: finalFocusLossSequence,
	}
}
