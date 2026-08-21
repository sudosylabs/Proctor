// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamAttemptSQLProbe struct {
	SetParticipationLeaseExpired func(*testing.T, context.Context, model.AttemptParticipationID)
	FenceRenewalPastDeadline     func(*testing.T, context.Context, model.AttemptParticipationID, func() error) error
	UnresolvedFocusLossMissing   func(*testing.T, context.Context, model.ExamAttemptID, int64) int64
	AgeFocusLossPending          func(*testing.T, context.Context, model.ExamAttemptID, int64, int64, time.Duration)
	FocusLossPersistence         func(*testing.T, context.Context, model.ExamAttemptID, int64) FocusLossPersistenceProbe
	ConcurrentExamAttempt        store.ExamAttemptStore
	AssertFocusLossSchema        func(*testing.T, context.Context)
}

// FocusLossPersistenceProbe exposes bounded persistence totals that cannot be
// observed through the candidate seam. It is used only by SQL conformance to
// prove the evidence cap, overflow summary, bucket consumption, and diagnostic
// bound without making those private details part of the production Store.
type FocusLossPersistenceProbe struct {
	Flags, Evidence, Pending int
	OverflowCount            int64
	DiagnosticCount          int64
}

func TestExamAttemptStore(t *testing.T, ss store.Store, probes ...ExamAttemptSQLProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	ineligible := saveUser(t, ctx, ss)
	ineligibleSession, _, _ := saveSession(t, ctx, ss, ineligible.ID.String(), 10)
	ineligibleFixture := fixture
	ineligibleFixture.candidate, ineligibleFixture.session = ineligible, ineligibleSession
	ineligibleInput := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: ineligible.ID,
		SessionID: ineligibleSession.ID, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, ineligibleFixture).ID.String(), AuditAt: model.GetMillis()}
	if _, err := ss.ExamAttempt().Connect(ctx, ineligibleInput,
		examCommand(ineligible.ID, store.ExamAttemptConnectOperation, "attempt-ineligible", "attempt-ineligible")); !store.IsNotFound(err) {
		t.Fatalf("Connect(without current membership) error = %v", err)
	}
	if _, err := ss.ExamAttempt().Get(ctx, fixture.examID, ineligibleInput.AttemptID); !store.IsNotFound(err) {
		t.Fatalf("Get(ineligible proposed Attempt) error = %v", err)
	}
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash}
	input.AuditEventID, input.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	command := examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-connect", "attempt-connect")

	connected, err := ss.ExamAttempt().Connect(ctx, input, command)
	requireNoError(t, err)
	if connected.Replayed || !connected.FirstAdmission || !connected.ConnectionOpened || connected.Attempt.ID != input.AttemptID ||
		connected.Workspace.ID != input.WorkspaceID || connected.Participation.ID != input.ParticipationID ||
		connected.Connection.ID != input.ConnectionID || connected.ClassID != fixture.class.ID ||
		connected.Participation.Generation != 1 || connected.Participation.RenewalSequence != 0 ||
		connected.Participation.LeaseExpiresAt.Sub(connected.Participation.StartedAt) != model.AttemptParticipationInitialLease {
		t.Fatalf("Connect() = %#v", connected)
	}
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	connectedAudit, err := ss.Audit().Get(ctx, input.AuditEventID)
	requireNoError(t, err)
	if bytes.Contains(connectedAudit.Result, []byte(credentialHash)) {
		t.Fatal("Connect audit exposed the continuity credential hash")
	}
	replayInput := *input
	replayInput.AuditEventID, replayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	replayed, err := ss.ExamAttempt().Connect(ctx, &replayInput, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.ConnectionOpened || replayed.Attempt.ID != connected.Attempt.ID || replayed.Connection.ID != connected.Connection.ID {
		t.Fatalf("Connect(replay) = %#v", replayed)
	}
	testExamAttemptParticipationRenewal(t, ctx, ss, fixture, connected, credentialHash)

	access := store.CandidateAttemptAccess{AttemptID: input.AttemptID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, ConnectionID: input.ConnectionID, ContinuityCredentialHash: credentialHash}
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	requireNoError(t, err)
	if presentation.AttemptID != input.AttemptID || presentation.AdmissionRevisionID != fixture.revisionID ||
		presentation.CurrentRevisionID != fixture.revisionID || !presentation.FocusLossCollectionEnabled {
		t.Fatalf("GetCandidatePresentation() = %#v", presentation)
	}
	pausedFocusInput := testExamAttemptFocusLoss(t, ctx, ss, fixture, connected, credentialHash, probes...)
	page, err := ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: 200})
	requireNoError(t, err)
	if page.HasMore || len(page.Items) != 2 || page.Items[0].Path != "cmd" || page.Items[1].Path != "cmd/main.go" ||
		!page.Items[0].ContentVersion.IsZero() || !page.Items[1].ContentVersion.IsValid() {
		t.Fatalf("ExamAttemptWorkspace.List() = %#v", page)
	}
	content, err := ss.ExamAttemptWorkspace().ResolveFile(ctx, access, page.Items[1].EntryID)
	requireNoError(t, err)
	if content.StarterObjectID.IsZero() || content.AttemptObjectID.IsZero() || content.ContentVersion != page.Items[1].ContentVersion {
		t.Fatalf("ExamAttemptWorkspace.ResolveFile() = %#v", content)
	}
	paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
		PrivateReason: "verify established candidate read access while paused", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, "exam.sitting.pause.v1", "attempt-pause", "attempt-pause"))
	requireNoError(t, err)
	if _, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access); err != nil {
		t.Fatalf("GetCandidatePresentation(paused) error = %v", err)
	}
	pausedFocusInput.AuditEventID, pausedFocusInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	pausedFocus, err := ss.ExamAttempt().RecordFocusLoss(ctx, pausedFocusInput)
	requireNoError(t, err)
	if pausedFocus.AcceptedSequence != pausedFocusInput.Sequence || !pausedFocus.Qualified || pausedFocus.WindowIncidentCount != 1 {
		t.Fatalf("RecordFocusLoss(paused) = %#v", pausedFocus)
	}
	if _, err = ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: 200}); err != nil {
		t.Fatalf("ExamAttemptWorkspace.List(paused) error = %v", err)
	}
	if _, err = ss.ExamAttemptWorkspace().ResolveFile(ctx, access, page.Items[1].EntryID); err != nil {
		t.Fatalf("ExamAttemptWorkspace.ResolveFile(paused) error = %v", err)
	}
	pausedReplayInput := *input
	pausedReplayInput.AuditEventID, pausedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	pausedReplay, err := ss.ExamAttempt().Connect(ctx, &pausedReplayInput, command)
	requireNoError(t, err)
	if !pausedReplay.Replayed || pausedReplay.ConnectionOpened || pausedReplay.Connection.ID != connected.Connection.ID {
		t.Fatalf("Connect(exact replay while paused) = %#v", pausedReplay)
	}
	freshWhilePaused := *input
	freshWhilePaused.AttemptID, freshWhilePaused.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	freshWhilePaused.ParticipationID, freshWhilePaused.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	freshWhilePaused.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	freshWhilePaused.AuditEventID, freshWhilePaused.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	_, err = ss.ExamAttempt().Connect(ctx, &freshWhilePaused,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-connect-paused", "attempt-connect-paused"))
	var pausedConflict *store.ErrConflict
	if !errors.As(err, &pausedConflict) || pausedConflict.Constraint != "exam_sitting_state" {
		t.Fatalf("Connect(fresh while paused) error = %v", err)
	}
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision,
		PrivateReason: "continue Attempt persistence conformance", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, "exam.sitting.resume.v1", "attempt-resume", "attempt-resume"))
	requireNoError(t, err)
	if resumed.Value.Sitting.State != model.ExamSittingOpen {
		t.Fatalf("Resume() = %#v", resumed)
	}
	openConnectionInput := *input
	openConnectionInput.AttemptID, openConnectionInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	openConnectionInput.ParticipationID, openConnectionInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	openConnectionInput.AuditEventID, openConnectionInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	convergedOpen, err := ss.ExamAttempt().Connect(ctx, &openConnectionInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-open-connection", "attempt-open-connection"))
	requireNoError(t, err)
	if convergedOpen.FirstAdmission || convergedOpen.ConnectionOpened || convergedOpen.Connection.ID != connected.Connection.ID ||
		convergedOpen.Participation.ID != connected.Participation.ID {
		t.Fatalf("Connect(with matching open Connection) = %#v", convergedOpen)
	}
	alternateSession, _, _ := saveSession(t, ctx, ss, fixture.candidate.ID.String(), 10)
	alternateSessionInput := openConnectionInput
	alternateSessionInput.SessionID = alternateSession.ID
	alternateSessionInput.AuditEventID, alternateSessionInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	_, err = ss.ExamAttempt().Connect(ctx, &alternateSessionInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-open-other-session", "attempt-open-other-session"))
	assertExamAttemptConflict(t, err, "attempt_connection_open")
	wrongSession := access
	wrongSession.SessionID = model.NewSessionID()
	if _, err = ss.ExamAttempt().GetCandidatePresentation(ctx, wrongSession); !store.IsNotFound(err) {
		t.Fatalf("candidate access from another Session error = %v", err)
	}

	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, input.AttemptID)
	requireNoError(t, err)
	if manager.Attempt.ID != input.AttemptID || manager.LatestParticipation == nil || manager.CurrentConnection == nil {
		t.Fatalf("Get() = %#v", manager)
	}
	listed, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: fixture.examID, SittingID: fixture.sitting.ID, Limit: 201})
	requireNoError(t, err)
	if len(listed) != 1 || listed[0].Attempt.ID != input.AttemptID {
		t.Fatalf("List() = %#v", listed)
	}
	testConcurrentExamAttemptAdmission(t, ctx, ss, fixture)

	closeInput := &store.ExamAttemptConnectionClose{ConnectionID: input.ConnectionID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, Reason: model.AttemptConnectionCloseTransport,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	foreignClose := *closeInput
	foreignClose.SessionID = model.NewSessionID()
	foreignClose.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	if _, err = ss.ExamAttempt().CloseConnection(ctx, &foreignClose); !store.IsNotFound(err) {
		t.Fatalf("CloseConnection(foreign Session) error = %v", err)
	}
	closed, err := ss.ExamAttempt().CloseConnection(ctx, closeInput)
	requireNoError(t, err)
	if !closed.Changed || closed.Connection.State != model.AttemptConnectionClosed {
		t.Fatalf("CloseConnection() = %#v", closed)
	}
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	closedAgain, err := ss.ExamAttempt().CloseConnection(ctx, closeInput)
	requireNoError(t, err)
	if closedAgain.Changed || closedAgain.Connection.CloseReason != model.AttemptConnectionCloseTransport {
		t.Fatalf("CloseConnection(repeat) = %#v", closedAgain)
	}
	wrongCredentialInput := *input
	wrongCredentialInput.AttemptID, wrongCredentialInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	wrongCredentialInput.ParticipationID, wrongCredentialInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	wrongCredentialInput.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	wrongCredentialInput.AuditEventID, wrongCredentialInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	_, err = ss.ExamAttempt().Connect(ctx, &wrongCredentialInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-wrong-credential", "attempt-wrong-credential"))
	assertExamAttemptConflict(t, err, "attempt_participation_credential")

	wrongReconnectSession := *input
	wrongReconnectSession.AttemptID, wrongReconnectSession.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	wrongReconnectSession.ParticipationID, wrongReconnectSession.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	wrongReconnectSession.SessionID = model.NewSessionID()
	wrongReconnectSession.AuditEventID, wrongReconnectSession.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	if _, err = ss.ExamAttempt().Connect(ctx, &wrongReconnectSession,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-wrong-session", "attempt-wrong-session")); !store.IsNotFound(err) {
		t.Fatalf("Connect(reconnect with foreign Session) error = %v", err)
	}

	reconnectInput := *input
	reconnectInput.AttemptID, reconnectInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	reconnectInput.ParticipationID, reconnectInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	reconnectInput.AuditEventID, reconnectInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	reconnected, err := ss.ExamAttempt().Connect(ctx, &reconnectInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-reconnect", "attempt-reconnect"))
	requireNoError(t, err)
	if reconnected.FirstAdmission || !reconnected.ConnectionOpened || reconnected.Attempt.ID != connected.Attempt.ID || reconnected.Workspace.ID != connected.Workspace.ID ||
		reconnected.Participation.ID != connected.Participation.ID || reconnected.Participation.Generation != connected.Participation.Generation ||
		reconnected.Connection.ID != reconnectInput.ConnectionID {
		t.Fatalf("Connect(reconnect) = %#v", reconnected)
	}
	closeInput.ConnectionID = reconnected.Connection.ID
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	if _, err = ss.ExamAttempt().CloseConnection(ctx, closeInput); err != nil {
		t.Fatalf("CloseConnection(reconnected) error = %v", err)
	}

	endedAt := model.GetMillis()
	endedMembership, err := ss.ClassMember().End(ctx, fixture.membership.ID.String(), fixture.membership.Revision, endedAt)
	requireNoError(t, err)
	// Membership timestamps have millisecond precision while PostgreSQL decides
	// current eligibility with a higher-precision clock. Cross the millisecond
	// boundary before asserting that the just-ended relationship is inactive.
	time.Sleep(100 * time.Millisecond)
	revokedReplayInput := *input
	revokedReplayInput.AuditEventID, revokedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	if _, err = ss.ExamAttempt().Connect(ctx, &revokedReplayInput, command); !store.IsNotFound(err) {
		t.Fatalf("Connect(exact replay after membership revoke) error = %v", err)
	}
	revokedFreshInput := reconnectInput
	revokedFreshInput.ConnectionID = model.NewAttemptConnectionID()
	revokedFreshInput.AuditEventID, revokedFreshInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	if _, err = ss.ExamAttempt().Connect(ctx, &revokedFreshInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-revoked", "attempt-revoked")); !store.IsNotFound(err) {
		t.Fatalf("Connect(fresh after membership revoke) error = %v", err)
	}
	restored, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: fixture.candidate.ID,
		StartsAt: model.TimeFromMillis(endedAt)})
	requireNoError(t, err)
	fixture.membership = restored.Membership
	restoredInput := reconnectInput
	restoredInput.ConnectionID = model.NewAttemptConnectionID()
	restoredInput.AuditEventID, restoredInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	restoredConnection, err := ss.ExamAttempt().Connect(ctx, &restoredInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-restored", "attempt-restored"))
	requireNoError(t, err)
	if restoredConnection.FirstAdmission || !restoredConnection.ConnectionOpened || restoredConnection.Participation.Generation != connected.Participation.Generation {
		t.Fatalf("Connect(after membership restore) = %#v; ended membership=%#v", restoredConnection, endedMembership)
	}
	closeInput.ConnectionID = restoredConnection.Connection.ID
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	if _, err = ss.ExamAttempt().CloseConnection(ctx, closeInput); err != nil {
		t.Fatalf("CloseConnection(restored reconnect) error = %v", err)
	}
	closedReplayInput := *input
	closedReplayInput.AuditEventID, closedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	closedReplay, err := ss.ExamAttempt().Connect(ctx, &closedReplayInput, command)
	requireNoError(t, err)
	if !closedReplay.Replayed || closedReplay.ConnectionOpened || closedReplay.Connection.State != model.AttemptConnectionClosed ||
		closedReplay.Connection.CloseReason != model.AttemptConnectionCloseTransport {
		t.Fatalf("Connect(replay after Connection close) = %#v", closedReplay)
	}
	if _, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access); !store.IsNotFound(err) {
		t.Fatalf("candidate access after close error = %v", err)
	}
	if len(probes) != 0 && probes[0].SetParticipationLeaseExpired != nil {
		probes[0].SetParticipationLeaseExpired(t, ctx, input.ParticipationID)
		expiredReplay := *input
		expiredReplay.AuditEventID, expiredReplay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
		_, replayErr := ss.ExamAttempt().Connect(ctx, &expiredReplay, command)
		var conflict *store.ErrConflict
		if !errors.As(replayErr, &conflict) || conflict.Constraint != "attempt_participation_expired" {
			t.Fatalf("Connect(replay after lease expiry) error = %v", replayErr)
		}
		otherExpired := testExamAttemptParticipationExpiry(t, ctx, ss, fixture, probes[0])
		batchAudit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
		batchInput := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
			Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
			SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: batchAudit.ID.String(), AuditAt: model.GetMillis()}
		batchExpired, batchErr := ss.ExamAttempt().ExpireParticipation(ctx, batchInput)
		requireNoError(t, batchErr)
		if batchExpired.Evidence.ID == otherExpired.Evidence.ID || batchExpired.Flag.ID == otherExpired.Flag.ID ||
			batchExpired.Suspension.ID == otherExpired.Suspension.ID || batchExpired.Participation.Generation != connected.Participation.Generation ||
			batchExpired.ConnectionClosed || batchExpired.Connection.CloseReason != model.AttemptConnectionCloseTransport {
			t.Fatalf("installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		}
	}
	currentSitting, err := ss.ExamSitting().Get(ctx, fixture.examID, fixture.sitting.ID)
	requireNoError(t, err)
	closeAt := model.NowUTC()
	closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: currentSitting.Sitting.Revision,
		FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, currentSitting.Sitting.Revision+1, closeAt),
		PrivateReason: "finish Attempt persistence conformance", ChangedAt: closeAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, "exam.sitting.close.v1", "attempt-close-sitting", "attempt-close-sitting"))
	requireNoError(t, err)
	closingReplayInput := *input
	closingReplayInput.AuditEventID, closingReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
	_, err = ss.ExamAttempt().Connect(ctx, &closingReplayInput, command)
	assertExamAttemptConflict(t, err, "exam_sitting_state")
	unfinished, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if unfinished.Changed || unfinished.Value.Sitting.State != model.ExamSittingClosing || closing.Value.Sitting.State != model.ExamSittingClosing {
		t.Fatalf("FinishSealing(with unfinished Attempt) = %#v", unfinished)
	}
	if len(probes) != 0 && probes[0].FocusLossPersistence != nil {
		testExamAttemptFocusLossPolicies(t, ctx, ss, probes[0])
	}
}

func testExamAttemptParticipationExpiry(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture, probe ExamAttemptSQLProbe) *store.ExamAttemptParticipationExpiryResult {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, candidate.ID.String(), 10)
	expiryFixture := fixture
	expiryFixture.candidate, expiryFixture.session = candidate, session
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, expiryFixture).ID.String(), AuditAt: model.GetMillis()}
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(candidate.ID, store.ExamAttemptConnectOperation, "attempt-expiry-connect", "attempt-expiry-connect"))
	requireNoError(t, err)
	if probe.FenceRenewalPastDeadline != nil {
		renewal := &store.ExamAttemptParticipationRenewal{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
			ConnectionID: connected.Connection.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
			Generation: connected.Participation.Generation, Sequence: 1, ContinuityCredentialHash: credentialHash}
		renewErr := probe.FenceRenewalPastDeadline(t, ctx, connected.Participation.ID, func() error {
			_, contenderErr := ss.ExamAttempt().RenewParticipation(ctx, renewal)
			return contenderErr
		})
		assertExamAttemptConflict(t, renewErr, "attempt_participation_expired")
	}
	probe.SetParticipationLeaseExpired(t, ctx, connected.Participation.ID)

	due, err := ss.ExamAttempt().ListExpiredParticipations(ctx, 200)
	requireNoError(t, err)
	if len(due) < 2 {
		t.Fatalf("installation-wide expiry batch = %#v, want multiple Attempts", due)
	}
	var selected *store.ExamAttemptParticipationExpiryDue
	for index := range due {
		if due[index].ParticipationID == connected.Participation.ID {
			selected = &due[index]
			break
		}
	}
	if selected == nil || selected.AttemptID != connected.Attempt.ID || selected.Generation != connected.Participation.Generation ||
		selected.ExamID != fixture.examID || selected.SittingID != fixture.sitting.ID || selected.ClassID != fixture.class.ID ||
		selected.CandidateUserID != candidate.ID || selected.LeaseExpiresAt.IsZero() {
		t.Fatalf("ListExpiredParticipations() = %#v", due)
	}
	resolved, err := ss.ExamAttempt().ResolveParticipationExpiry(ctx, connected.Attempt.ID, connected.Participation.ID,
		connected.Participation.Generation)
	requireNoError(t, err)
	if resolved.AttemptID != selected.AttemptID || resolved.ParticipationID != selected.ParticipationID ||
		resolved.Generation != selected.Generation || !resolved.LeaseExpiresAt.Equal(selected.LeaseExpiresAt) ||
		resolved.ExamID != selected.ExamID || resolved.SittingID != selected.SittingID || resolved.ClassID != selected.ClassID ||
		resolved.CandidateUserID != selected.CandidateUserID {
		t.Fatalf("ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	}
	failedInput := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	if _, err = ss.ExamAttempt().ExpireParticipation(ctx, failedInput); err == nil {
		t.Fatal("ExpireParticipation(with missing Audit) succeeded")
	}
	unchanged, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if unchanged.Attempt.State != model.ExamAttemptActive || unchanged.Attempt.Revision != connected.Attempt.Revision ||
		unchanged.LatestParticipation == nil || unchanged.LatestParticipation.State != model.AttemptParticipationActive ||
		unchanged.ActiveSuspension != nil {
		t.Fatalf("failed expiry left partial state: %#v", unchanged)
	}

	audit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	input := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
	concurrentAudit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	concurrentInput := *input
	concurrentInput.EvidenceID, concurrentInput.FlagID, concurrentInput.SuspensionID = model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	concurrentInput.AuditEventID, concurrentInput.AuditAt = concurrentAudit.ID.String(), model.GetMillis()
	results := make(chan *store.ExamAttemptParticipationExpiryResult, 2)
	errorsFound := make(chan error, 2)
	start := make(chan struct{})
	for _, proposal := range []*store.ExamAttemptParticipationExpiry{input, &concurrentInput} {
		go func(proposal *store.ExamAttemptParticipationExpiry) {
			<-start
			result, expireErr := ss.ExamAttempt().ExpireParticipation(ctx, proposal)
			results <- result
			errorsFound <- expireErr
		}(proposal)
	}
	close(start)
	first, second := <-results, <-results
	requireNoError(t, <-errorsFound)
	requireNoError(t, <-errorsFound)
	if first.Replayed == second.Replayed || first.Evidence.ID != second.Evidence.ID || first.Flag.ID != second.Flag.ID ||
		first.Suspension.ID != second.Suspension.ID || !first.DatabaseTime.Equal(second.DatabaseTime) {
		t.Fatalf("ExpireParticipation(concurrent) = %#v, %#v", first, second)
	}
	expired := first
	if expired.Replayed {
		expired = second
	}
	if expired == nil || expired.Replayed || expired.Attempt == nil || expired.Participation == nil || expired.Connection == nil ||
		expired.Evidence == nil || expired.Flag == nil || expired.Suspension == nil || expired.ExamID != fixture.examID ||
		expired.SittingID != fixture.sitting.ID || expired.ClassID != fixture.class.ID || expired.CandidateUserID != candidate.ID ||
		expired.Attempt.State != model.ExamAttemptSuspended || expired.Attempt.Revision != 2 ||
		expired.Participation.State != model.AttemptParticipationEnded || expired.Participation.EndReason != model.AttemptParticipationEndLeaseExpired ||
		expired.Connection.State != model.AttemptConnectionClosed || expired.Connection.CloseReason != model.AttemptConnectionCloseLeaseExpired || !expired.ConnectionClosed ||
		expired.Evidence.ObservedAt != selected.LeaseExpiresAt || expired.Flag.State != model.IntegrityFlagOpen ||
		expired.Suspension.State != model.AttemptSuspensionActive ||
		expired.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonSecureContinuityLost || expired.DatabaseTime.IsZero() {
		t.Fatalf("ExpireParticipation() = %#v", expired)
	}
	firstProposalWon := expired.Evidence.ID == input.EvidenceID && expired.Flag.ID == input.FlagID && expired.Suspension.ID == input.SuspensionID
	secondProposalWon := expired.Evidence.ID == concurrentInput.EvidenceID && expired.Flag.ID == concurrentInput.FlagID && expired.Suspension.ID == concurrentInput.SuspensionID
	if firstProposalWon == secondProposalWon {
		t.Fatalf("ExpireParticipation retained mixed or unknown proposal IDs: %#v", expired)
	}
	requireSuccessfulAudit(t, ctx, ss, audit.ID.String())
	requireSuccessfulAudit(t, ctx, ss, concurrentAudit.ID.String())
	managerSnapshot, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if managerSnapshot.ActiveSuspension == nil || managerSnapshot.ActiveSuspension.ID != expired.Suspension.ID {
		t.Fatalf("Get(active Suspension) = %#v", managerSnapshot)
	}
	managerList, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, Limit: 201})
	requireNoError(t, err)
	foundActiveSuspension := false
	for _, snapshot := range managerList {
		if snapshot.Attempt.ID == connected.Attempt.ID && snapshot.ActiveSuspension != nil && snapshot.ActiveSuspension.ID == expired.Suspension.ID {
			foundActiveSuspension = true
		}
	}
	if !foundActiveSuspension {
		t.Fatalf("List(active Suspension) = %#v", managerList)
	}

	replayAudit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	replayInput := *input
	replayInput.EvidenceID, replayInput.FlagID, replayInput.SuspensionID = model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	replayInput.AuditEventID, replayInput.AuditAt = replayAudit.ID.String(), model.GetMillis()
	replayed, err := ss.ExamAttempt().ExpireParticipation(ctx, &replayInput)
	requireNoError(t, err)
	if replayed == nil || !replayed.Replayed || replayed.Evidence == nil || replayed.Flag == nil || replayed.Suspension == nil ||
		replayed.Evidence.ID != expired.Evidence.ID || replayed.Flag.ID != expired.Flag.ID || replayed.Suspension.ID != expired.Suspension.ID ||
		!replayed.DatabaseTime.Equal(expired.DatabaseTime) {
		t.Fatalf("ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	}
	requireSuccessfulAudit(t, ctx, ss, replayAudit.ID.String())

	reallowAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowInput := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
		AttemptID: expired.Attempt.ID, SuspensionID: expired.Suspension.ID, ActorUserID: fixture.manager.ID,
		ExpectedAttemptRevision: expired.Attempt.Revision, PrivateReason: "verified connectivity recovery",
		ChangedAt: model.NowUTC(), AuditEventID: reallowAudit.ID.String(), AuditAt: model.GetMillis()}
	reallowCommand := examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow", "attempt-reallow")
	competingAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	competingInput := *reallowInput
	competingInput.PrivateReason = "second manager recovery decision"
	competingInput.AuditEventID, competingInput.AuditAt = competingAudit.ID.String(), model.GetMillis()
	competingCommand := examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow-competing", "attempt-reallow-competing")
	type reallowOutcome struct {
		index  int
		result *store.ExamAttemptReallowResult
		err    error
	}
	reallowOutcomes := make(chan reallowOutcome, 2)
	reallowStart := make(chan struct{})
	for index, proposal := range []*store.ExamAttemptReallow{reallowInput, &competingInput} {
		command := []*store.CommandIdempotency{reallowCommand, competingCommand}[index]
		go func(index int, proposal *store.ExamAttemptReallow, command *store.CommandIdempotency) {
			<-reallowStart
			result, reallowErr := ss.ExamAttempt().ReallowAttempt(ctx, proposal, command)
			reallowOutcomes <- reallowOutcome{index: index, result: result, err: reallowErr}
		}(index, proposal, command)
	}
	close(reallowStart)
	var reallowed *store.ExamAttemptReallowResult
	winner := -1
	for range 2 {
		outcome := <-reallowOutcomes
		if outcome.err == nil {
			if winner != -1 {
				t.Fatal("both concurrent ReallowAttempt calls succeeded")
			}
			winner, reallowed = outcome.index, outcome.result
			continue
		}
		var conflict *store.ErrConflict
		if !errors.As(outcome.err, &conflict) || (conflict.Constraint != "attempt_suspension_active" && conflict.Constraint != "exam_attempt_revision") {
			t.Fatalf("concurrent ReallowAttempt loser error = %v", outcome.err)
		}
	}
	if winner < 0 {
		t.Fatal("neither concurrent ReallowAttempt call succeeded")
	}
	if winner == 1 {
		reallowAudit, reallowInput, reallowCommand = competingAudit, &competingInput, competingCommand
	}
	if reallowed == nil || reallowed.Replayed || reallowed.Attempt == nil || reallowed.Suspension == nil ||
		reallowed.Attempt.State != model.ExamAttemptActive || reallowed.Attempt.Revision != expired.Attempt.Revision+1 ||
		reallowed.Suspension.State != model.AttemptSuspensionClosed || !reallowed.Suspension.EndedAt.Valid ||
		reallowed.Suspension.ReallowedByUserID != fixture.manager.ID || reallowed.CandidateUserID != candidate.ID {
		t.Fatalf("ReallowAttempt() = %#v", reallowed)
	}
	managerSnapshot, err = ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if managerSnapshot.ActiveSuspension != nil {
		t.Fatalf("Get(after re-allow) retained active Suspension = %#v", managerSnapshot.ActiveSuspension)
	}
	reallowEvent, err := ss.Audit().Get(ctx, reallowAudit.ID.String())
	requireNoError(t, err)
	if bytes.Contains(reallowEvent.Result, []byte(reallowInput.PrivateReason)) {
		t.Fatal("ReallowAttempt audit exposed the private reason")
	}
	reallowReplayAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowReplay := *reallowInput
	reallowReplay.AuditEventID, reallowReplay.AuditAt = reallowReplayAudit.ID.String(), model.GetMillis()
	replayedReallow, err := ss.ExamAttempt().ReallowAttempt(ctx, &reallowReplay, reallowCommand)
	requireNoError(t, err)
	if replayedReallow == nil || !replayedReallow.Replayed || replayedReallow.Attempt.Revision != reallowed.Attempt.Revision ||
		replayedReallow.Suspension.ID != reallowed.Suspension.ID {
		t.Fatalf("ReallowAttempt(replay) = %#v", replayedReallow)
	}
	expiryAfterReallowAudit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
	expiryAfterReallow := *input
	expiryAfterReallow.EvidenceID, expiryAfterReallow.FlagID, expiryAfterReallow.SuspensionID =
		model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	expiryAfterReallow.AuditEventID, expiryAfterReallow.AuditAt = expiryAfterReallowAudit.ID.String(), model.GetMillis()
	retainedExpiry, err := ss.ExamAttempt().ExpireParticipation(ctx, &expiryAfterReallow)
	requireNoError(t, err)
	if retainedExpiry == nil || !retainedExpiry.Replayed || retainedExpiry.Attempt.State != model.ExamAttemptSuspended ||
		retainedExpiry.Attempt.Revision != expired.Attempt.Revision || retainedExpiry.Suspension.State != model.AttemptSuspensionActive ||
		retainedExpiry.Suspension.EndedAt.Valid || !retainedExpiry.Suspension.ReallowedByUserID.IsZero() ||
		retainedExpiry.Evidence.ID != expired.Evidence.ID || retainedExpiry.Flag.ID != expired.Flag.ID ||
		retainedExpiry.Suspension.ID != expired.Suspension.ID || !retainedExpiry.DatabaseTime.Equal(expired.DatabaseTime) {
		t.Fatalf("ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	}
	requireSuccessfulAudit(t, ctx, ss, expiryAfterReallowAudit.ID.String())
	reallowDifferentAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowDifferent := reallowReplay
	reallowDifferent.AuditEventID = reallowDifferentAudit.ID.String()
	_, err = ss.ExamAttempt().ReallowAttempt(ctx, &reallowDifferent,
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow-different", "attempt-reallow-different"))
	assertExamAttemptConflict(t, err, "attempt_suspension_active")

	nextConnect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, expiryFixture).ID.String(), AuditAt: model.GetMillis()}
	next, err := ss.ExamAttempt().Connect(ctx, nextConnect,
		examCommand(candidate.ID, store.ExamAttemptConnectOperation, "attempt-next-generation", "attempt-next-generation"))
	requireNoError(t, err)
	if next.Participation.Generation != expired.Participation.Generation+1 || next.Attempt.ID != expired.Attempt.ID ||
		next.Workspace.ID != connected.Workspace.ID || !next.ConnectionOpened {
		t.Fatalf("Connect(next generation) = %#v", next)
	}

	due, err = ss.ExamAttempt().ListExpiredParticipations(ctx, 200)
	requireNoError(t, err)
	for _, candidate := range due {
		if candidate.ParticipationID == connected.Participation.ID {
			t.Fatalf("expired Participation remained due: %#v", due)
		}
	}
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	if _, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access); !store.IsNotFound(err) {
		t.Fatalf("candidate access after expiry error = %v", err)
	}
	return expired
}

func saveExamAttemptSystemAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExamSittingParticipate),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}

func saveExamAttemptReallowAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: fixture.manager.ID, Action: string(model.ActionExamSittingManage),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}

func testExamAttemptParticipationRenewal(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	connected *store.ExamAttemptConnectResult, credentialHash string,
) {
	t.Helper()
	input := &store.ExamAttemptParticipationRenewal{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Generation: connected.Participation.Generation,
		Sequence: 1, ContinuityCredentialHash: credentialHash,
	}
	renewed, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	requireNoError(t, err)
	if renewed == nil || renewed.AttemptID != input.AttemptID || renewed.ParticipationID != input.ParticipationID ||
		renewed.Generation != input.Generation || renewed.AcceptedSequence != input.Sequence || renewed.Duplicate ||
		renewed.DatabaseTime.IsZero() || renewed.LeaseExpiresAt.Sub(renewed.DatabaseTime) != model.AttemptParticipationInitialLease {
		t.Fatalf("RenewParticipation() = %#v", renewed)
	}

	duplicate, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	requireNoError(t, err)
	if duplicate == nil || !duplicate.Duplicate || duplicate.AcceptedSequence != renewed.AcceptedSequence ||
		!duplicate.DatabaseTime.Equal(renewed.DatabaseTime) || !duplicate.LeaseExpiresAt.Equal(renewed.LeaseExpiresAt) {
		t.Fatalf("RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)
	}

	input.Sequence = 2
	advanced, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	requireNoError(t, err)
	if advanced.Duplicate || advanced.AcceptedSequence != 2 || advanced.DatabaseTime.Before(renewed.DatabaseTime) ||
		advanced.LeaseExpiresAt.Sub(advanced.DatabaseTime) != model.AttemptParticipationInitialLease {
		t.Fatalf("RenewParticipation(advanced) = %#v, first = %#v", advanced, renewed)
	}
	input.Sequence = 1
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	assertExamAttemptConflict(t, err, "attempt_participation_sequence")
	input.Sequence = 3
	input.Generation++
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	assertExamAttemptConflict(t, err, "attempt_participation_generation")
	input.Generation--
	input.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	assertExamAttemptConflict(t, err, "attempt_participation_credential")
}

type examAttemptFixture struct {
	candidate  *model.User
	manager    *model.User
	session    *model.Session
	sitting    *model.ExamSitting
	class      *model.Class
	examID     model.ExamID
	revisionID model.ExamRevisionID
	unitID     model.AcademicUnitID
	membership *model.ClassMember
}

func newExamAttemptFixture(t *testing.T, ctx context.Context, ss store.Store) examAttemptFixture {
	return newExamAttemptFixtureWithFocusLoss(t, ctx, ss, nil)
}

func newExamAttemptFixtureWithFocusLoss(t *testing.T, ctx context.Context, ss store.Store, focus *model.FocusLossPolicy) examAttemptFixture {
	t.Helper()
	unit, programme := saveProgrammeParents(t, ctx, ss, "attempt-unit")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "attempt-level")
	now := model.NowUTC()
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "attempt-period", model.MillisFromTime(now.Add(-time.Hour)))
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "attempt-class")
	manager := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, programme.AcademicUnitID, manager.ID, now, "attempt-exam")
	draftRevision := int64(1)
	if focus != nil {
		updated, updateErr := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx,
			newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, manager.ID, draftRevision, *focus, now.Add(time.Millisecond)),
			examCommand(manager.ID, "exam.draft.focus_loss.configure.v1", "attempt-focus-policy", "attempt-focus-policy"))
		requireNoError(t, updateErr)
		draftRevision = updated.Value.Draft.Revision
	}
	directory := starterWorkspaceMutation(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision,
		model.NewStarterWorkspaceEntryID(), "cmd", now.Add(time.Millisecond))
	_, err := ss.ExamStarterWorkspace().CreateDirectory(ctx, directory,
		examCommand(manager.ID, "exam.starter_workspace.directory.create.v1", "attempt-dir", "attempt-dir"))
	requireNoError(t, err)
	draftRevision++
	file := reserveStarterWorkspaceObject(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision,
		model.NewStarterWorkspaceEntryID(), "cmd/main.go", now.Add(2*time.Millisecond), 13)
	_, err = ss.ExamStarterWorkspace().CreateFile(ctx, file,
		examCommand(manager.ID, "exam.starter_workspace.file.create.v1", "attempt-file", "attempt-file"))
	requireNoError(t, err)
	draftRevision++
	publication := examRevisionPublication(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision, now.Add(3*time.Millisecond))
	published, err := ss.ExamRevision().Publish(ctx, publication, examCommand(manager.ID, "exam.revision.publish.v1", "attempt-revision", "attempt-revision"))
	requireNoError(t, err)
	start, end := model.NowUTC().Add(time.Second), model.NowUTC().Add(time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), created.Value.Exam.ID, published.Revision.ID, class.ID, start, end, now)
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: manager.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, manager.ID, created.Value.Exam.ID, programme.AcademicUnitID).ID.String(), AuditAt: model.GetMillis(),
		Mail: newExamSittingMailFanout(t, manager.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
		examCommand(manager.ID, "exam.sitting.schedule.v1", "attempt-sitting", "attempt-sitting"))
	requireNoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	opened, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: sitting.ID, AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, sitting.ID, programme.AcademicUnitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if opened.Value.Sitting.State != model.ExamSittingOpen {
		t.Fatalf("AdvanceDue()=%#v", opened)
	}
	candidate := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: now.Add(-time.Hour)})
	requireNoError(t, err)
	enrollment, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: candidate.ID, StartsAt: now.Add(-time.Minute)})
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, candidate.ID.String(), 10)
	return examAttemptFixture{candidate: candidate, manager: manager, session: session, sitting: opened.Value.Sitting, class: class,
		examID: created.Value.Exam.ID, revisionID: published.Revision.ID, unitID: programme.AcademicUnitID, membership: enrollment.Membership}
}

func testConcurrentExamAttemptAdmission(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID, StartsAt: model.NowUTC().Add(-time.Minute)})
	requireNoError(t, err)
	session, _, _ := saveSession(t, ctx, ss, candidate.ID.String(), 10)
	concurrentFixture := fixture
	concurrentFixture.candidate, concurrentFixture.session = candidate, session
	credentialHash := model.HashToken(model.NewCredentialToken())
	inputs := [2]*store.ExamAttemptConnect{}
	commands := [2]*store.CommandIdempotency{}
	for index := range inputs {
		inputs[index] = &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
			AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
			ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, concurrentFixture).ID.String(), AuditAt: model.GetMillis()}
		commands[index] = examCommand(candidate.ID, store.ExamAttemptConnectOperation,
			fmt.Sprintf("attempt-concurrent-%d", index), fmt.Sprintf("attempt-concurrent-%d", index))
	}
	start := make(chan struct{})
	results := make(chan *store.ExamAttemptConnectResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, connectErr := ss.ExamAttempt().Connect(ctx, inputs[index], commands[index])
			results <- result
			errorsFound <- connectErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for connectErr := range errorsFound {
		requireNoError(t, connectErr)
	}
	connected := make([]*store.ExamAttemptConnectResult, 0, 2)
	for result := range results {
		connected = append(connected, result)
	}
	if len(connected) != 2 || connected[0] == nil || connected[1] == nil || connected[0].Attempt.ID != connected[1].Attempt.ID ||
		connected[0].Workspace.ID != connected[1].Workspace.ID || connected[0].Participation.ID != connected[1].Participation.ID ||
		connected[0].Connection.ID != connected[1].Connection.ID || connected[0].FirstAdmission == connected[1].FirstAdmission ||
		connected[0].ConnectionOpened == connected[1].ConnectionOpened {
		t.Fatalf("concurrent Connect results = %#v", connected)
	}
	closeResult, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{ConnectionID: connected[0].Connection.ID,
		CandidateUserID: candidate.ID, SessionID: session.ID, Reason: model.AttemptConnectionCloseTransport,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, concurrentFixture).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if !closeResult.Changed {
		t.Fatalf("CloseConnection(concurrent admission) = %#v", closeResult)
	}
}

func assertExamAttemptConflict(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != constraint {
		t.Fatalf("conflict = %v, want %s", err, constraint)
	}
}

func saveExamAttemptAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: fixture.candidate.ID, Action: string(model.ActionExamSittingParticipate),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass, ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}
