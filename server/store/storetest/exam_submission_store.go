// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

func attachSubmissionReceipt(t *testing.T, candidate *model.User, input *store.ExamSubmissionSeal) {
	t.Helper()
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

func submissionReceiptNotice(t *testing.T, candidate *model.User, submissionID model.SubmissionID, auditAt int64,
	key model.MailTemplateKey,
) *store.PreparedMail {
	t.Helper()
	at := model.TimeFromMillis(auditAt)
	occurrenceID := model.MailOccurrenceID(submissionID.String())
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	job, err = job.RequestCancellation(at)
	requireNoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSubmissionReceipt,
		TemplateKey: key, ActorUserID: candidate.ID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: candidate.ID,
		TemplateKey: key, TemplateDigest: strings.Repeat("0", 64), MaskedRecipient: "c***@example.edu",
		State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at, MessageDate: at,
		Deadline: at.Add(72 * time.Hour), MessageID: "<receipt." + deliveryID.String() + "@example.test>",
		PublicFailureCode: model.MailDeliveryDisabledCode, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = delivery.Validate(); err != nil {
		t.Fatal(err)
	}
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}
}

// TestExamSubmissionStore verifies voluntary terminal sealing and protected
// manager inspection through the public Submission Store contract.
func TestExamSubmissionStore(t *testing.T, ss store.Store, submissions store.ExamSubmissionStore, probes ...ExamSubmissionSQLProbe) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	credentialHash := model.HashToken(model.NewCredentialToken())
	connected, err := ss.ExamAttempt().Connect(ctx, &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
	}, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "submission-connect", "submission-connect"))
	requireNoError(t, err)

	workspaceAccess := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	object, err := ss.ExamAttemptWorkspace().ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: workspaceAccess, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	requireNoError(t, err)
	object, err = ss.ExamAttemptWorkspace().MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{
		Access: workspaceAccess, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(),
		Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 7, SHA256: strings.Repeat("b", 64)},
	})
	requireNoError(t, err)
	attemptEntryID := model.NewAttemptWorkspaceEntryID()
	mutation, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{
		Access: workspaceAccess, Operation: model.AttemptWorkspaceMutationCreateFile, EntryID: attemptEntryID,
		DestinationPath: "answer.txt", ObjectID: object.ID,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
	}, examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "submission-file", "submission-file"))
	requireNoError(t, err)

	access := store.ExamSubmissionSealAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: credentialHash, ExpectedWorkspaceCursor: mutation.Change.Cursor,
	}
	target, err := submissions.ResolveSealTarget(ctx, access)
	requireNoError(t, err)
	if target == nil || target.ExamID != fixture.examID || target.SittingID != fixture.sitting.ID ||
		target.ClassID != fixture.class.ID || target.CandidateUserID != fixture.candidate.ID ||
		target.WorkspaceID != connected.Workspace.ID || target.SealAt.IsZero() {
		t.Fatalf("ResolveSealTarget() = %#v", target)
	}

	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.MillisFromTime(target.SealAt)}
	attachSubmissionReceipt(t, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-seal", "submission-seal")
	sealed, err := submissions.Seal(ctx, input, command)
	requireNoError(t, err)
	if sealed == nil || sealed.Replayed || sealed.Receipt.SubmissionID != input.SubmissionID ||
		sealed.Receipt.AttemptID != connected.Attempt.ID || sealed.Receipt.State != model.ExamAttemptSubmitted ||
		sealed.Receipt.WorkspaceCursor != mutation.Change.Cursor || !validStoretestDigest(sealed.Receipt.ManifestDigest) ||
		!sealed.Receipt.SubmittedAt.Equal(input.Notice.Occurrence.CreatedAt) ||
		sealed.ExamID != fixture.examID || sealed.SittingID != fixture.sitting.ID ||
		sealed.ClassID != fixture.class.ID || sealed.CandidateUserID != fixture.candidate.ID ||
		sealed.ParticipationID != connected.Participation.ID || sealed.Generation != connected.Participation.Generation ||
		sealed.ConnectionID != connected.Connection.ID {
		t.Fatalf("Seal() = %#v", sealed)
	}
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	replayTarget, err := submissions.ResolveSealTarget(ctx, access)
	requireNoError(t, err)
	if replayTarget == nil || !replayTarget.Replayed {
		t.Fatalf("ResolveSealTarget(after seal) = %#v", replayTarget)
	}
	receiptDelivery, err := ss.Mail().GetDelivery(ctx, input.Notice.Delivery.ID)
	requireNoError(t, err)
	if receiptDelivery.OccurrenceID != model.MailOccurrenceID(input.SubmissionID.String()) ||
		receiptDelivery.TemplateKey != model.MailTemplateExamSubmissionReceived || receiptDelivery.TargetUserID != fixture.candidate.ID {
		t.Fatalf("voluntary Submission receipt delivery = %#v", receiptDelivery)
	}
	audit, err := ss.Audit().Get(ctx, input.AuditEventID)
	requireNoError(t, err)
	for _, secret := range []string{credentialHash, fixture.session.ID.String(), "cmd/main.go"} {
		if bytes.Contains(audit.Result, []byte(secret)) {
			t.Fatalf("Submission audit exposed protected value %q: %s", secret, audit.Result)
		}
	}

	authorization, err := submissions.Resolve(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if authorization == nil || authorization.SubmissionID != sealed.Receipt.SubmissionID ||
		authorization.ExamID != fixture.examID || authorization.SittingID != fixture.sitting.ID ||
		authorization.AttemptID != connected.Attempt.ID || authorization.AcademicUnitID != fixture.unitID {
		t.Fatalf("Resolve() = %#v", authorization)
	}
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header == nil || header.ID != sealed.Receipt.SubmissionID || header.AttemptID != connected.Attempt.ID ||
		header.WorkspaceID != connected.Workspace.ID || header.WorkspaceCursor != mutation.Change.Cursor ||
		header.ManifestDigest != sealed.Receipt.ManifestDigest || header.ManifestEntryCount != 3 || header.ManifestTotalFileBytes != 20 ||
		header.IntegrityState != model.SubmissionIntegritySettled || header.UnresolvedIntegrityCount != 0 ||
		!header.SubmittedAt.Equal(sealed.Receipt.SubmittedAt) {
		t.Fatalf("Get() = %#v", header)
	}
	page, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
		SubmissionID: header.ID, Limit: 200,
	})
	requireNoError(t, err)
	if page == nil || page.SubmissionID != header.ID || page.WorkspaceCursor != header.WorkspaceCursor ||
		page.ManifestDigest != header.ManifestDigest || page.HasMore || len(page.Items) != 3 ||
		page.Items[0].EntryID.String() >= page.Items[1].EntryID.String() ||
		page.Items[1].EntryID.String() >= page.Items[2].EntryID.String() {
		t.Fatalf("ListManifest() = %#v", page)
	}
	firstPage, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{SubmissionID: header.ID, Limit: 1})
	requireNoError(t, err)
	if firstPage == nil || !firstPage.HasMore || len(firstPage.Items) != 1 || firstPage.Items[0] != page.Items[0] {
		t.Fatalf("ListManifest(first bounded page) = %#v", firstPage)
	}
	secondPage, err := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
		SubmissionID: header.ID, AfterEntryID: firstPage.Items[0].EntryID, Limit: 1,
	})
	requireNoError(t, err)
	if secondPage == nil || !secondPage.HasMore || len(secondPage.Items) != 1 || secondPage.Items[0] != page.Items[1] {
		t.Fatalf("ListManifest(second bounded page) = %#v", secondPage)
	}
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
	requireNoError(t, err)
	if selector == nil || selector.Entry != starterFile || selector.StorageOrigin != model.AttemptWorkspaceStorageStarter ||
		!selector.StarterObjectID.IsValid() || !selector.AttemptObjectID.IsZero() || selector.ContentVersion != starterFile.ContentVersion {
		t.Fatalf("ResolveFile() = %#v", selector)
	}
	attemptSelector, err := submissions.ResolveFile(ctx, header.ID, attemptFile.EntryID)
	requireNoError(t, err)
	if attemptSelector == nil || attemptSelector.Entry != attemptFile ||
		attemptSelector.StorageOrigin != model.AttemptWorkspaceStorageAttempt ||
		!attemptSelector.AttemptObjectID.IsValid() || !attemptSelector.StarterObjectID.IsZero() ||
		attemptSelector.AttemptObjectID != object.ID || attemptSelector.ContentVersion != object.ContentVersion {
		t.Fatalf("ResolveFile(attempt origin) = %#v", attemptSelector)
	}
	if _, err = submissions.ResolveFile(ctx, header.ID, directory.EntryID); !store.IsNotFound(err) {
		t.Fatalf("ResolveFile(directory) error = %v", err)
	}

	if _, err = ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: store.CandidateAttemptAccess{
		AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
		ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
	}, ExpectedCursor: -1, Limit: 200}); !store.IsNotFound(err) {
		t.Fatalf("candidate Workspace read after Submission error = %v", err)
	}
	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if manager.Attempt.State != model.ExamAttemptSubmitted || manager.LatestParticipation == nil ||
		manager.LatestParticipation.State != model.AttemptParticipationEnded ||
		manager.LatestParticipation.EndReason != model.AttemptParticipationEndSubmitted || manager.CurrentConnection != nil {
		t.Fatalf("terminal Attempt aggregate = %#v", manager)
	}

	replay := *input
	replay.SubmissionID = model.NewSubmissionID()
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replayed, err := submissions.Seal(ctx, &replay, command)
	requireNoError(t, err)
	if replayed == nil || !replayed.Replayed || replayed.Receipt != sealed.Receipt ||
		replayed.ParticipationID != sealed.ParticipationID || replayed.ConnectionID != sealed.ConnectionID {
		t.Fatalf("Seal(exact replay) = %#v, first = %#v", replayed, sealed)
	}
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	receipts, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamSubmissionReceived}, Limit: 200})
	requireNoError(t, err)
	matchingReceipts := 0
	for _, delivery := range receipts {
		if delivery.OccurrenceID == model.MailOccurrenceID(input.SubmissionID.String()) {
			matchingReceipts++
		}
		if delivery.OccurrenceID == model.MailOccurrenceID(replay.SubmissionID.String()) {
			t.Fatalf("replay created a second Submission receipt: %#v", delivery)
		}
	}
	if matchingReceipts != 1 {
		t.Fatalf("voluntary receipt count=%d, deliveries=%#v", matchingReceipts, receipts)
	}

	different := *input
	different.SubmissionID = model.NewSubmissionID()
	different.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	if _, err = submissions.Seal(ctx, &different,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-second", "submission-second")); err == nil {
		t.Fatal("Seal(different key after terminal state) succeeded")
	}
	if got, getErr := submissions.Get(ctx, different.SubmissionID); !store.IsNotFound(getErr) || got != nil {
		t.Fatalf("Get(uncommitted second Submission) = %#v, %v", got, getErr)
	}

	testExamSubmissionRollback(t, ctx, ss, submissions)
	testExamSubmissionIntegrityGap(t, ctx, ss, submissions, probes...)
	testExamSubmissionHistoricalIntegrityGap(t, ctx, ss, submissions)
	testExamSubmissionAccessFences(t, ctx, ss, submissions)
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
			if _, getErr := submissions.Get(ctx, header.ID); getErr == nil {
				t.Fatal("Get(corrupt immutable manifest) succeeded")
			}
		}
	}
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
		if suspension.Attempt == nil || suspension.Attempt.State != model.ExamAttemptSuspended ||
			suspension.Participation == nil || suspension.Participation.EndReason != model.AttemptParticipationEndPolicySuspended {
			t.Fatalf("Focus Loss suspension fixture = %#v", suspension)
		}

		submittedFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(-time.Minute))
		submitted, submittedAccess := connectFocusLossFixture(t, ctx, ss, submittedFixture, "automatic-submitted-connect")
		submittedSealAccess := submissionAccess(submittedAccess, submitted.Workspace.Cursor, 0)
		submittedInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: submittedSealAccess,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, submittedFixture).ID.String(), AuditAt: model.GetMillis()}
		attachSubmissionReceipt(t, submittedFixture.candidate, submittedInput)
		alreadySubmitted, err := submissions.Seal(ctx, submittedInput,
			examCommand(submittedFixture.candidate.ID, store.ExamSubmissionSealOperation,
				"automatic-existing-submission", "automatic-existing-submission"))
		requireNoError(t, err)

		noShowFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(-time.Minute))
		lateFixture := addExamAttemptCandidate(t, ctx, ss, fixture, fixture.sitting.OpenedAt.Time.Add(time.Minute))
		closeAt := model.NowUTC()
		closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "finish every acknowledged Attempt", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis()},
			examCommand(fixture.manager.ID, "exam.sitting.close.v1", "automatic-close", "automatic-close"))
		requireNoError(t, err)
		if !closing.Changed || closing.Value.Sitting.State != model.ExamSittingClosing {
			t.Fatalf("EarlyClose() = %#v", closing)
		}
		_, err = ss.ExamAttempt().RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{
			AttemptID: active.Attempt.ID, ParticipationID: active.Participation.ID, ConnectionID: active.Connection.ID,
			CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Generation: active.Participation.Generation,
			Sequence: 1, ContinuityCredentialHash: activeAccess.ContinuityCredentialHash,
		})
		assertExamAttemptConflict(t, err, "exam_sitting_state")

		unfinished, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
			AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis()})
		requireNoError(t, err)
		if unfinished.Changed || unfinished.Value.Sitting.State != model.ExamSittingClosing {
			t.Fatalf("FinishSealing(with unfinished Attempts) = %#v", unfinished)
		}

		page, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, Limit: 1})
		requireNoError(t, err)
		if len(page) != 1 {
			t.Fatalf("ListAutomaticSealTargets(first page) = %#v", page)
		}
		secondPage, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, AfterAttemptID: page[0].AttemptID, Limit: 2})
		requireNoError(t, err)
		allTargets := append(append([]store.ExamSubmissionAutomaticSealTarget(nil), page...), secondPage...)
		if len(allTargets) != 2 || allTargets[0].AttemptID.String() >= allTargets[1].AttemptID.String() {
			t.Fatalf("automatic seal target pages = %#v / %#v", page, secondPage)
		}
		wantAttempts := map[model.ExamAttemptID]bool{active.Attempt.ID: true, suspended.Attempt.ID: true}
		for _, target := range allTargets {
			if !wantAttempts[target.AttemptID] || target.AttemptID == submitted.Attempt.ID {
				t.Fatalf("automatic seal target = %#v, submitted=%s", target, submitted.Attempt.ID)
			}
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
		if !activeResult.ConnectionClosed {
			t.Fatalf("active automatic seal did not close Connection: %#v", activeResult)
		}
		automaticPreparation, replayErr := submissions.PrepareAutomaticSeal(ctx, activeTarget)
		requireNoError(t, replayErr)
		if automaticPreparation == nil || !automaticPreparation.Replayed || automaticPreparation.SealAt.IsZero() {
			t.Fatalf("PrepareAutomaticSeal(after seal) = %#v", automaticPreparation)
		}
		suspendedAudit := saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID)
		suspendedPreparation, preparationErr := submissions.PrepareAutomaticSeal(ctx, suspendedTarget)
		requireNoError(t, preparationErr)
		if suspendedPreparation == nil || suspendedPreparation.Replayed || suspendedPreparation.SealAt.IsZero() {
			t.Fatalf("PrepareAutomaticSeal(suspended) = %#v", suspendedPreparation)
		}
		rollbackInput := &store.ExamSubmissionAutomaticSeal{Target: suspendedTarget, SubmissionID: model.NewSubmissionID(),
			AuditEventID: model.NewAuditEventID().String(), AuditAt: model.MillisFromTime(suspendedPreparation.SealAt)}
		attachAutomaticSubmissionReceipt(t, suspendedFixture.candidate, rollbackInput)
		if _, rollbackErr := submissions.SealForSittingClose(ctx, rollbackInput); rollbackErr == nil {
			t.Fatal("automatic Seal(with missing audit) succeeded")
		}
		if delivery, deliveryErr := ss.Mail().GetDelivery(ctx, rollbackInput.Notice.Delivery.ID); !store.IsNotFound(deliveryErr) || delivery != nil {
			t.Fatalf("automatic rolled-back receipt = %#v, %v", delivery, deliveryErr)
		}
		suspendedInput := &store.ExamSubmissionAutomaticSeal{
			Target: suspendedTarget, SubmissionID: model.NewSubmissionID(), AuditEventID: suspendedAudit.ID.String(),
			AuditAt: model.MillisFromTime(suspendedPreparation.SealAt)}
		attachAutomaticSubmissionReceipt(t, suspendedFixture.candidate, suspendedInput)
		suspendedResult, err := submissions.SealForSittingClose(ctx, suspendedInput)
		requireNoError(t, err)
		if suspendedResult.Replayed || suspendedResult.ConnectionClosed {
			t.Fatalf("suspended automatic seal = %#v", suspendedResult)
		}
		automaticReceipts, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
			TemplateKeys: []model.MailTemplateKey{model.MailTemplateExamSubmissionAutomaticallySealed}, Limit: 200})
		requireNoError(t, listErr)
		if len(automaticReceipts) != 2 {
			t.Fatalf("automatic receipt deliveries=%#v", automaticReceipts)
		}
		requireSuccessfulAudit(t, ctx, ss, suspendedAudit.ID.String())

		for _, result := range []*store.ExamSubmissionAutomaticSealResult{activeResult, suspendedResult} {
			header, getErr := submissions.Get(ctx, result.Receipt.SubmissionID)
			requireNoError(t, getErr)
			if header.IntegrityState != model.SubmissionIntegrityGapped || header.UnresolvedIntegrityCount < 1 ||
				header.WorkspaceCursor != result.Receipt.WorkspaceCursor || header.ManifestDigest != result.Receipt.ManifestDigest {
				t.Fatalf("automatic Submission header = %#v", header)
			}
			manifest, listErr := submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
				SubmissionID: header.ID, Limit: model.ExamSubmissionManifestReadMaximum})
			requireNoError(t, listErr)
			if len(manifest.Items) != 2 || manifest.ManifestDigest != header.ManifestDigest {
				t.Fatalf("automatic Submission manifest = %#v", manifest)
			}
		}

		activeManager, err := ss.ExamAttempt().Get(ctx, fixture.examID, active.Attempt.ID)
		requireNoError(t, err)
		if activeManager.Attempt.State != model.ExamAttemptSubmitted || activeManager.LatestParticipation == nil ||
			activeManager.LatestParticipation.EndReason != model.AttemptParticipationEndSittingClosed {
			t.Fatalf("active automatically sealed Attempt = %#v", activeManager)
		}
		suspendedManager, err := ss.ExamAttempt().Get(ctx, fixture.examID, suspended.Attempt.ID)
		requireNoError(t, err)
		if suspendedManager.Attempt.State != model.ExamAttemptSubmitted || suspendedManager.LatestParticipation == nil ||
			suspendedManager.LatestParticipation.EndReason != model.AttemptParticipationEndPolicySuspended {
			t.Fatalf("suspended automatically sealed Attempt = %#v", suspendedManager)
		}

		remaining, err := submissions.ListAutomaticSealTargets(ctx, store.ExamSubmissionAutomaticSealListOptions{
			SittingID: fixture.sitting.ID, Limit: 200})
		requireNoError(t, err)
		if len(remaining) != 0 {
			t.Fatalf("remaining automatic seal targets = %#v", remaining)
		}
		closed, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
			AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID.String(),
			AuditAt:      model.GetMillis()})
		requireNoError(t, err)
		if !closed.Changed || closed.Transition != store.ExamSittingTransitionSealingCompleted ||
			closed.Value.Sitting.State != model.ExamSittingClosed {
			t.Fatalf("FinishSealing() = %#v", closed)
		}

		noShows, err := ss.ExamSitting().ListNoShows(ctx, store.ExamSittingNoShowListOptions{
			SittingID: fixture.sitting.ID, Limit: 200})
		requireNoError(t, err)
		if len(noShows) != 1 || noShows[0].CandidateUserID != noShowFixture.candidate.ID {
			t.Fatalf("ListNoShows() = %#v; want=%s late=%s", noShows, noShowFixture.candidate.ID, lateFixture.candidate.ID)
		}
		if existing, getErr := submissions.Get(ctx, alreadySubmitted.Receipt.SubmissionID); getErr != nil ||
			existing.ID != alreadySubmitted.Receipt.SubmissionID {
			t.Fatalf("existing Submission after automatic sealing = %#v, %v", existing, getErr)
		}
	})
}

func addExamAttemptCandidate(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	startsAt time.Time,
) examAttemptFixture {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: startsAt.Add(-time.Hour)})
	requireNoError(t, err)
	enrollment, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: fixture.class.ID,
		UserID: candidate.ID, StartsAt: startsAt})
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, candidate.ID.String(), 10)
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
	candidate, err := ss.User().Get(ctx, target.CandidateUserID.String())
	requireNoError(t, err)
	for index := range inputs {
		audit := saveExamSittingSystemAudit(t, ctx, ss, target.SittingID, target.AcademicUnitID)
		preparation, preparationErr := adapters[index].PrepareAutomaticSeal(ctx, target)
		requireNoError(t, preparationErr)
		if preparation == nil || preparation.Replayed || preparation.SealAt.IsZero() {
			t.Fatalf("PrepareAutomaticSeal(fresh) = %#v", preparation)
		}
		inputs[index] = &store.ExamSubmissionAutomaticSeal{Target: target, SubmissionID: model.NewSubmissionID(),
			AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(preparation.SealAt)}
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
		requireNoError(t, err)
	}
	values := make([]*store.ExamSubmissionAutomaticSealResult, 0, 2)
	for indexed := range results {
		if indexed.result != nil && !indexed.result.Replayed &&
			!indexed.result.Receipt.SubmittedAt.Equal(inputs[indexed.index].Notice.Occurrence.CreatedAt) {
			t.Fatalf("automatic seal time=%s, receipt occurrence=%s", indexed.result.Receipt.SubmittedAt,
				inputs[indexed.index].Notice.Occurrence.CreatedAt)
		}
		values = append(values, indexed.result)
	}
	if len(values) != 2 || values[0] == nil || values[1] == nil || values[0].Replayed == values[1].Replayed ||
		values[0].Receipt != values[1].Receipt || values[0].ConnectionClosed == values[1].ConnectionClosed {
		t.Fatalf("concurrent automatic Seals = %#v", values)
	}
	for _, input := range inputs {
		requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
		audit, err := ss.Audit().Get(ctx, input.AuditEventID)
		requireNoError(t, err)
		if !audit.ActorID.IsZero() || audit.SessionID.IsValid() {
			t.Fatalf("automatic Submission audit invented an actor or Session: %#v", audit)
		}
		encoded := bytes.ToLower(audit.Result)
		for _, forbidden := range []string{"path", "content", "credential", "session", "private_reason"} {
			if bytes.Contains(encoded, []byte(forbidden)) {
				t.Fatalf("automatic Submission audit contains %q: %s", forbidden, audit.Result)
			}
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

func assertSubmissionConflict(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != constraint {
		t.Fatalf("conflict = %v, want %s", err, constraint)
	}
}

func testExamSubmissionRollback(t *testing.T, ctx context.Context, ss store.Store, submissions store.ExamSubmissionStore) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-rollback-connect")
	access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: model.NewAuditEventID().String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-rollback", "submission-rollback")
	if _, err := submissions.Seal(ctx, input, command); err == nil {
		t.Fatal("Seal(with missing audit) succeeded")
	}
	if got, err := submissions.Get(ctx, input.SubmissionID); !store.IsNotFound(err) || got != nil {
		t.Fatalf("Get(rolled-back Submission) = %#v, %v", got, err)
	}
	if delivery, err := ss.Mail().GetDelivery(ctx, input.Notice.Delivery.ID); !store.IsNotFound(err) || delivery != nil {
		t.Fatalf("GetDelivery(rolled-back voluntary receipt) = %#v, %v", delivery, err)
	}
	if _, err := submissions.ResolveSealTarget(ctx, access); err != nil {
		t.Fatalf("ResolveSealTarget(after rollback) error = %v", err)
	}
	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if manager.Attempt.State != model.ExamAttemptActive || manager.LatestParticipation == nil ||
		manager.LatestParticipation.State != model.AttemptParticipationActive || manager.CurrentConnection == nil ||
		manager.CurrentConnection.State != model.AttemptConnectionOpen {
		t.Fatalf("Attempt changed despite rolled-back seal = %#v", manager)
	}
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	retried, err := submissions.Seal(ctx, input, command)
	requireNoError(t, err)
	if retried == nil || retried.Replayed || retried.Receipt.SubmissionID != input.SubmissionID {
		t.Fatalf("Seal(retry after rollback) = %#v", retried)
	}
}

func testExamSubmissionIntegrityGap(t *testing.T, ctx context.Context, ss store.Store,
	submissions store.ExamSubmissionStore, probes ...ExamSubmissionSQLProbe,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-integrity-connect")
	gap := recordFocusLoss(t, ctx, ss, fixture, focusAccess, 2, 2000, model.FocusLossSourceWindowBlur)
	if gap.AcceptedSequence != 2 || gap.MissingBefore != 1 || !gap.Qualified || gap.ThresholdCrossed {
		t.Fatalf("Focus Loss pre-seal gap = %#v", gap)
	}
	stale := store.ExamSubmissionSealAccess(submissionAccess(focusAccess, connected.Workspace.Cursor, 1))
	staleInput := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: stale,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	if _, err := submissions.Seal(ctx, staleInput,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-stale-focus", "submission-stale-focus")); err == nil {
		t.Fatal("Seal(with final Focus Loss sequence behind accepted high-water) succeeded")
	} else {
		assertSubmissionConflict(t, err, "focus_loss_sequence")
	}
	access := submissionAccess(focusAccess, connected.Workspace.Cursor, 5)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, input)
	sealed, err := submissions.Seal(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-gapped", "submission-gapped"))
	requireNoError(t, err)
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.FinalFocusLossSequence != 5 || header.IntegrityState != model.SubmissionIntegrityGapped ||
		header.UnresolvedIntegrityCount != 4 {
		t.Fatalf("gapped Submission = %#v", header)
	}
	if len(probes) != 0 && probes[0].IntegrityPersistence != nil {
		persisted := probes[0].IntegrityPersistence(t, ctx, connected.Attempt.ID, connected.Participation.Generation)
		if persisted.PendingQualifiers != 0 || persisted.UnresolvedMissing != 4 {
			t.Fatalf("terminal integrity persistence = %#v", persisted)
		}
	}
	late := newFocusLossInput(t, ctx, ss, fixture, focusAccess, 3, 2000, model.FocusLossSourceDocumentHidden)
	if _, err = ss.ExamAttempt().RecordFocusLoss(ctx, late); err == nil {
		t.Fatal("RecordFocusLoss(after Submission) succeeded")
	}
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
	if crossed.MissingBefore != 1 || !crossed.ThresholdCrossed || crossed.Suspension == nil || crossed.Attempt == nil ||
		crossed.Attempt.State != model.ExamAttemptSuspended {
		t.Fatalf("historical Focus Loss suspension = %#v", crossed)
	}
	reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, SuspensionID: crossed.Suspension.ID,
		ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: crossed.Attempt.Revision,
		PrivateReason: "verify Submission retains uncertainty across Participation generations", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "submission-history-reallow", "submission-history-reallow"))
	requireNoError(t, err)
	if reallowed.Attempt.State != model.ExamAttemptActive || !reallowed.FocusLossWindowReset {
		t.Fatalf("historical gap ReallowAttempt() = %#v", reallowed)
	}
	secondCredentialHash := model.HashToken(model.NewCredentialToken())
	reconnected, err := ss.ExamAttempt().Connect(ctx, &store.ExamAttemptConnect{SittingID: fixture.sitting.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: secondCredentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "submission-history-reconnect", "submission-history-reconnect"))
	requireNoError(t, err)
	if reconnected.Attempt.ID != connected.Attempt.ID || reconnected.Participation.Generation != connected.Participation.Generation+1 {
		t.Fatalf("historical gap reconnect = %#v", reconnected)
	}
	access := store.ExamSubmissionSealAccess{AttemptID: reconnected.Attempt.ID,
		ParticipationID: reconnected.Participation.ID, Generation: reconnected.Participation.Generation,
		ConnectionID: reconnected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		ContinuityCredentialHash: secondCredentialHash, ExpectedWorkspaceCursor: reconnected.Workspace.Cursor,
		FinalFocusLossSequence: 2}
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, input)
	sealed, err := submissions.Seal(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-history-seal", "submission-history-seal"))
	requireNoError(t, err)
	header, err := submissions.Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if header.IntegrityState != model.SubmissionIntegrityGapped || header.UnresolvedIntegrityCount != 3 ||
		header.FinalFocusLossSequence != 2 {
		t.Fatalf("Submission omitted historical/current-generation uncertainty = %#v", header)
	}
}

func testExamSubmissionAccessFences(t *testing.T, ctx context.Context, ss store.Store, submissions store.ExamSubmissionStore) {
	t.Helper()

	t.Run("stale Workspace cursor", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-stale-cursor-connect")
		access := submissionAccess(focusAccess, connected.Workspace.Cursor+1, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		_, err := submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-stale-cursor", "submission-stale-cursor"))
		assertSubmissionConflict(t, err, "attempt_workspace_cursor")
	})

	t.Run("closed Connection", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-closed-connect")
		_, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{
			ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			Reason:       model.AttemptConnectionCloseTransport,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-closed", "submission-closed"))
		assertSubmissionConflict(t, err, "attempt_connection_closed")
	})

	t.Run("Paused Sitting", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-paused-connect")
		_, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			PrivateReason: "verify voluntary Submission rejects Paused Sitting", ChangedAt: model.NowUTC(),
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
			examCommand(fixture.manager.ID, "exam.sitting.pause.v1", "submission-pause", "submission-pause"))
		requireNoError(t, err)
		access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-paused", "submission-paused"))
		assertSubmissionConflict(t, err, "exam_sitting_state")
	})

	t.Run("Closing Sitting", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-closing-connect")
		closeAt := model.NowUTC()
		_, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
			SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
			FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, fixture.sitting.Revision+1, closeAt),
			PrivateReason: "verify voluntary Submission rejects Closing Sitting", ChangedAt: closeAt,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
			examCommand(fixture.manager.ID, "exam.sitting.close.v1", "submission-close", "submission-close"))
		requireNoError(t, err)
		access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		_, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-closing", "submission-closing"))
		assertSubmissionConflict(t, err, "exam_sitting_state")
		if got, getErr := submissions.Get(ctx, input.SubmissionID); !store.IsNotFound(getErr) || got != nil {
			t.Fatalf("Get(Closing-denied Submission) = %#v, %v", got, getErr)
		}
	})

	t.Run("Suspended Attempt", func(t *testing.T) {
		policy := model.FocusLossPolicy{Enabled: true, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
			Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlagAndSuspend}
		fixture := newExamAttemptFixtureWithFocusLoss(t, ctx, ss, &policy)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-suspended-connect")
		crossed := recordFocusLoss(t, ctx, ss, fixture, focusAccess, 1, 500, model.FocusLossSourceWindowBlur)
		if crossed.Attempt == nil || crossed.Attempt.State != model.ExamAttemptSuspended {
			t.Fatalf("Focus Loss did not suspend Submission fixture = %#v", crossed)
		}
		access := submissionAccess(focusAccess, connected.Workspace.Cursor, 1)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		if _, err := submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-suspended", "submission-suspended")); err == nil {
			t.Fatal("Seal(Suspended Attempt) succeeded")
		}
		if got, getErr := submissions.Get(ctx, input.SubmissionID); !store.IsNotFound(getErr) || got != nil {
			t.Fatalf("Get(Suspended-denied Submission) = %#v, %v", got, getErr)
		}
		manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
		requireNoError(t, err)
		if manager.Attempt.State != model.ExamAttemptSuspended {
			t.Fatalf("Suspended Attempt changed after denied seal = %#v", manager)
		}
	})

	t.Run("lost exact Class membership", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-membership-connect")
		_, err := ss.ClassMember().End(ctx, fixture.membership.ID.String(), fixture.membership.Revision, model.GetMillis())
		requireNoError(t, err)
		time.Sleep(100 * time.Millisecond)
		access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
		input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		if _, err = submissions.Seal(ctx, input,
			examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "submission-membership", "submission-membership")); !store.IsNotFound(err) {
			t.Fatalf("Seal(after membership loss) error = %v", err)
		}
	})
}

func testIndependentExamSubmissionReplay(t *testing.T, ctx context.Context, ss store.Store,
	first, second store.ExamSubmissionStore,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-peer-replay-connect")
	access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, input)
	command := examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation,
		"submission-peer-replay", "submission-peer-replay")
	sealed, err := first.Seal(ctx, input, command)
	requireNoError(t, err)
	replay := *input
	replay.SubmissionID = model.NewSubmissionID()
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replayed, err := second.Seal(ctx, &replay, command)
	requireNoError(t, err)
	if replayed == nil || !replayed.Replayed || replayed.Receipt != sealed.Receipt ||
		replayed.ParticipationID != sealed.ParticipationID || replayed.ConnectionID != sealed.ConnectionID {
		t.Fatalf("Seal(independent unknown-commit replay) = %#v, first = %#v", replayed, sealed)
	}
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
}

func testConcurrentExamSubmission(t *testing.T, ctx context.Context, ss store.Store,
	first, second store.ExamSubmissionStore,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connected, focusAccess := connectFocusLossFixture(t, ctx, ss, fixture, "submission-concurrent-connect")
	access := submissionAccess(focusAccess, connected.Workspace.Cursor, 0)
	inputs := [2]*store.ExamSubmissionSeal{}
	commands := [2]*store.CommandIdempotency{}
	for index := range inputs {
		inputs[index] = &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: access,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		attachSubmissionReceipt(t, fixture.candidate, inputs[index])
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
	if len(succeeded) != 1 || failed != 1 || succeeded[0].Replayed {
		t.Fatalf("concurrent different-key Seals = %#v, failures=%d", succeeded, failed)
	}
	losingID := inputs[0].SubmissionID
	if losingID == succeeded[0].Receipt.SubmissionID {
		losingID = inputs[1].SubmissionID
	}
	if got, err := first.Get(ctx, losingID); !store.IsNotFound(err) || got != nil {
		t.Fatalf("Get(concurrent losing Submission) = %#v, %v", got, err)
	}
}

func submissionAccess(access store.ExamAttemptFocusLossAccess, workspaceCursor, finalFocusLossSequence int64) store.ExamSubmissionSealAccess {
	return store.ExamSubmissionSealAccess{AttemptID: access.AttemptID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID,
		SessionID: access.SessionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
		ExpectedWorkspaceCursor: workspaceCursor, FinalFocusLossSequence: finalFocusLossSequence}
}
