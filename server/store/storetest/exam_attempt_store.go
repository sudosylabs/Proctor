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
	ExpireParticipation func(*testing.T, context.Context, model.AttemptParticipationID)
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

	access := store.CandidateAttemptAccess{AttemptID: input.AttemptID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, ConnectionID: input.ConnectionID, ContinuityCredentialHash: credentialHash}
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	requireNoError(t, err)
	if presentation.AttemptID != input.AttemptID || presentation.AdmissionRevisionID != fixture.revisionID ||
		presentation.CurrentRevisionID != fixture.revisionID {
		t.Fatalf("GetCandidatePresentation() = %#v", presentation)
	}
	page, err := ss.ExamAttempt().ListCandidateWorkspace(ctx, store.CandidateWorkspaceListOptions{Access: access, Limit: 200})
	requireNoError(t, err)
	if page.HasMore || len(page.Items) != 2 || page.Items[0].Path != "cmd" || page.Items[1].Path != "cmd/main.go" ||
		!page.Items[0].ContentVersion.IsZero() || !page.Items[1].ContentVersion.IsValid() {
		t.Fatalf("ListCandidateWorkspace() = %#v", page)
	}
	content, err := ss.ExamAttempt().ResolveCandidateWorkspaceFile(ctx, access, page.Items[1].EntryID)
	requireNoError(t, err)
	if content.StarterObjectID.IsZero() || content.AttemptObjectID.IsZero() || content.ContentVersion != page.Items[1].ContentVersion {
		t.Fatalf("ResolveCandidateWorkspaceFile() = %#v", content)
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
	if _, err = ss.ExamAttempt().ListCandidateWorkspace(ctx, store.CandidateWorkspaceListOptions{Access: access, Limit: 200}); err != nil {
		t.Fatalf("ListCandidateWorkspace(paused) error = %v", err)
	}
	if _, err = ss.ExamAttempt().ResolveCandidateWorkspaceFile(ctx, access, page.Items[1].EntryID); err != nil {
		t.Fatalf("ResolveCandidateWorkspaceFile(paused) error = %v", err)
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

	endedAt := model.GetMillis() - 1
	endedMembership, err := ss.ClassMember().End(ctx, fixture.membership.ID.String(), fixture.membership.Revision, endedAt)
	requireNoError(t, err)
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
	if len(probes) != 0 && probes[0].ExpireParticipation != nil {
		probes[0].ExpireParticipation(t, ctx, input.ParticipationID)
		expiredReplay := *input
		expiredReplay.AuditEventID, expiredReplay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), model.GetMillis()
		_, replayErr := ss.ExamAttempt().Connect(ctx, &expiredReplay, command)
		var conflict *store.ErrConflict
		if !errors.As(replayErr, &conflict) || conflict.Constraint != "attempt_participation_expired" {
			t.Fatalf("Connect(replay after lease expiry) error = %v", replayErr)
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
	closeEmpty, err := ss.ExamSitting().CloseIfNoAttempts(ctx, &store.ExamSittingCloseIfNoAttempts{SittingID: fixture.sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if closeEmpty.Changed || closeEmpty.Value.Sitting.State != model.ExamSittingClosing || closing.Value.Sitting.State != model.ExamSittingClosing {
		t.Fatalf("CloseIfNoAttempts(with Attempt) = %#v", closeEmpty)
	}
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
	t.Helper()
	unit, programme := saveProgrammeParents(t, ctx, ss, "attempt-unit")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "attempt-level")
	now := model.NowUTC()
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "attempt-period", model.MillisFromTime(now.Add(-time.Hour)))
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "attempt-class")
	manager := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, programme.AcademicUnitID, manager.ID, now, "attempt-exam")
	directory := starterWorkspaceMutation(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, 1,
		model.NewStarterWorkspaceEntryID(), "cmd", now.Add(time.Millisecond))
	_, err := ss.ExamStarterWorkspace().CreateDirectory(ctx, directory,
		examCommand(manager.ID, "exam.starter_workspace.directory.create.v1", "attempt-dir", "attempt-dir"))
	requireNoError(t, err)
	file := reserveStarterWorkspaceObject(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, 2,
		model.NewStarterWorkspaceEntryID(), "cmd/main.go", now.Add(2*time.Millisecond), 13)
	_, err = ss.ExamStarterWorkspace().CreateFile(ctx, file,
		examCommand(manager.ID, "exam.starter_workspace.file.create.v1", "attempt-file", "attempt-file"))
	requireNoError(t, err)
	publication := examRevisionPublication(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, 3, now.Add(3*time.Millisecond))
	published, err := ss.ExamRevision().Publish(ctx, publication, examCommand(manager.ID, "exam.revision.publish.v1", "attempt-revision", "attempt-revision"))
	requireNoError(t, err)
	start, end := model.NowUTC().Add(time.Second), model.NowUTC().Add(time.Hour)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), created.Value.Exam.ID, published.Revision.ID, class.ID, start, end, now)
	requireNoError(t, err)
	openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
	_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: manager.ID,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, manager.ID, created.Value.Exam.ID, programme.AcademicUnitID).ID.String(), AuditAt: model.GetMillis()},
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
