// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestConnectExamAttemptFingerprintBindsSessionAndNeverStoresRawCredential(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	credential := model.NewCredentialToken()
	principal := examAttemptPrincipal()
	invocation := NewInvocation(principal, model.RequestMetadata{RequestID: "connect-one"})
	command := ConnectExamAttemptCommand{SittingID: model.NewExamSittingID(), ContinuityCredential: credential, IdempotencyKey: "retry-key"}
	if _, err := application.ConnectExamAttempt(context.Background(), invocation, command); err != nil {
		t.Fatal(err)
	}
	first := *fake.connects[0].Idempotency
	if first.Operation != store.ExamAttemptConnectOperation || fake.connects[0].ContinuityCredential != credential {
		t.Fatalf("connect command = %#v idempotency=%#v", fake.connects[0], first)
	}

	principal.SessionID = model.NewSessionID()
	if _, err := application.ConnectExamAttempt(context.Background(), NewInvocation(principal, model.RequestMetadata{RequestID: "connect-two"}), command); err != nil {
		t.Fatal(err)
	}
	second := fake.connects[1].Idempotency
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("Connect fingerprint did not bind the authenticated Session")
	}
}

func TestWorkspaceMutationFingerprintSurvivesReconnectWhileChildReceivesCurrentAccess(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	principal := examAttemptPrincipal()
	access := examattempt.WorkspaceMutationAccess{CandidateAccess: examattempt.CandidateAccess{AttemptID: model.NewExamAttemptID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredential: model.NewCredentialToken()},
		ParticipationID: model.NewAttemptParticipationID(), Generation: 1}
	command := CreateCandidateExamWorkspaceDirectoryCommand{Access: access, Path: "src", IdempotencyKey: "mkdir-once"}
	if _, err := application.CreateCandidateExamWorkspaceDirectory(context.Background(), NewInvocation(principal, model.RequestMetadata{}), command); err != nil {
		t.Fatal(err)
	}
	first := fake.workspaceDirectories[0].Idempotency.Fingerprint
	principal.SessionID = model.NewSessionID()
	command.Access.ConnectionID, command.Access.ParticipationID, command.Access.Generation = model.NewAttemptConnectionID(), model.NewAttemptParticipationID(), 2
	command.Access.ContinuityCredential = model.NewCredentialToken()
	if _, err := application.CreateCandidateExamWorkspaceDirectory(context.Background(), NewInvocation(principal, model.RequestMetadata{}), command); err != nil {
		t.Fatal(err)
	}
	second := fake.workspaceDirectories[1]
	if first != second.Idempotency.Fingerprint || second.Access.ConnectionID != command.Access.ConnectionID ||
		second.Access.ParticipationID != command.Access.ParticipationID || second.Access.Generation != 2 {
		t.Fatalf("first=%x second=%#v", first, second)
	}
}

func TestCandidateExamAttemptFacadeConcealsAccessDenials(t *testing.T) {
	t.Parallel()
	for _, childCode := range []string{"exam.attempt.not_found", "exam.attempt.continuity_invalid"} {
		fake := &examAttemptUseCasesFake{err: &examattempt.Fault{Code: childCode}}
		application := &App{examAttempts: fake}
		_, err := application.GetCandidateExamPresentation(context.Background(),
			NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), CandidateExamAttemptAccess{})
		if !Is(err, "resource.not_found") {
			t.Fatalf("child=%s error=%v", childCode, err)
		}
	}
}

func TestTrustedConnectionCloseFacadeConcealsOwnershipMismatch(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{err: &examattempt.Fault{Code: "exam.attempt.not_found"}}
	application := &App{examAttempts: fake}
	_, err := application.CloseExamAttemptConnection(context.Background(),
		NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), CloseExamAttemptConnectionCommand{})
	if !Is(err, "resource.not_found") {
		t.Fatalf("error=%v", err)
	}
}

func TestConnectExamAttemptRequiresAnIdempotencyKeyBeforeChildCall(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	_, err := application.ConnectExamAttempt(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}),
		ConnectExamAttemptCommand{SittingID: model.NewExamSittingID(), ContinuityCredential: model.NewCredentialToken()})
	if !Is(err, "idempotency.key_required") || len(fake.connects) != 0 {
		t.Fatalf("error=%v calls=%d", err, len(fake.connects))
	}
}

func TestRenewExamAttemptParticipationDelegatesWithoutInventingTransportPingState(t *testing.T) {
	t.Parallel()
	want := examattempt.ParticipationRenewal{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, AcceptedSequence: 8, DatabaseTime: time.Now(), LeaseExpiresAt: time.Now().Add(20 * time.Second)}
	fake := &examAttemptUseCasesFake{renewResult: want}
	application := &App{examAttempts: fake}
	command := RenewExamAttemptParticipationCommand{AttemptID: want.AttemptID, ParticipationID: want.ParticipationID,
		ConnectionID: model.NewAttemptConnectionID(), Generation: 2, Sequence: 8, ContinuityCredential: model.NewCredentialToken()}
	got, err := application.RenewExamAttemptParticipation(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
	if err != nil || got != want || len(fake.renewals) != 1 || fake.renewals[0] != examattempt.RenewParticipationCommand(command) {
		t.Fatalf("result=%#v error=%v calls=%#v", got, err, fake.renewals)
	}
}

func TestEvaluateExamAttemptFocusLossDelegatesExactTrustedClaim(t *testing.T) {
	t.Parallel()
	want := examattempt.FocusLossEvaluation{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, AcceptedSequence: 8, ReceivedAt: time.Now(), GapDetected: true, Qualified: true}
	fake := &examAttemptUseCasesFake{focusResult: want}
	application := &App{examAttempts: fake}
	command := EvaluateExamAttemptFocusLossCommand{SchemaVersion: model.FocusLossSignalSchemaVersion,
		AttemptID: want.AttemptID, ParticipationID: want.ParticipationID,
		ConnectionID: model.NewAttemptConnectionID(), Generation: 2, Sequence: 8, DurationMilliseconds: 2500,
		Source: model.FocusLossSourceWindowBlur, ContinuityCredential: model.NewCredentialToken()}
	got, err := application.EvaluateExamAttemptFocusLoss(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
	if err != nil || got != want || len(fake.focusLoss) != 1 || fake.focusLoss[0] != examattempt.FocusLossCommand(command) {
		t.Fatalf("result=%#v error=%v calls=%#v", got, err, fake.focusLoss)
	}
}

func TestReallowExamAttemptBuildsExactPrivateReasonFingerprintAndDelegates(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	command := ReallowExamAttemptCommand{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		AttemptID: model.NewExamAttemptID(), SuspensionID: model.NewAttemptSuspensionID(), ExpectedAttemptRevision: 4,
		PrivateReason: "manager verified continuity", IdempotencyKey: "reallow-once"}
	if _, err := application.ReallowExamAttempt(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command); err != nil {
		t.Fatal(err)
	}
	if len(fake.reallows) != 1 || fake.reallows[0].ExamID != command.ExamID || fake.reallows[0].SuspensionID != command.SuspensionID ||
		fake.reallows[0].PrivateReason != command.PrivateReason || fake.reallows[0].Idempotency == nil ||
		fake.reallows[0].Idempotency.Operation != store.ExamAttemptReallowOperation {
		t.Fatalf("reallow calls = %#v", fake.reallows)
	}
}

func TestParticipationExpiryEffectPublishesCloseManagerSuspensionAndCandidateSuspension(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := examattempt.ParticipationExpiry{SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
		Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 2},
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionCloseLeaseExpired},
		ConnectionClosed: true,
		Flag:             model.IntegrityFlag{ID: model.NewIntegrityFlagID()},
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(),
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost}}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).ParticipationExpired(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 3 || events[0].Name != "exam_attempt_connection_closed" || events[0].Action != model.ActionExamSittingView ||
		events[1].Name != "exam_attempt_suspended" || events[1].Action != model.ActionExamSittingView ||
		events[2].Name != "exam_attempt_access_suspended" || events[2].Action != model.ActionExamSittingParticipate ||
		events[2].UserID != result.CandidateUserID.String() {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		for _, forbidden := range []string{"continuity_credential", "credential_hash", "private_reason"} {
			if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
				t.Fatalf("expiry event contains %q: %s", forbidden, event.Data)
			}
		}
	}
	if got := string(events[2].Data); !strings.Contains(got, `"reason_code":"secure_connectivity_lost"`) {
		t.Fatalf("candidate expiry event lacks safe reason: %s", got)
	}
}

func TestFocusLossWarningEffectPublishesBoundedManagerFlagAndNeutralCandidateWarning(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	result := examattempt.FocusLossEvaluation{SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		AttemptID: model.NewExamAttemptID(), ReceivedAt: at, PolicyOutcome: model.IntegrityOutcomeFlagAndWarn,
		Flag: model.IntegrityFlag{ID: model.NewIntegrityFlagID()}, RetainedEvidenceCount: 3,
		FlagCreated: true, ManagerNotificationRequired: true, CandidateWarningCreated: true}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).FocusLossEvaluated(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 2 || events[0].Name != "exam_attempt_integrity_flagged" ||
		events[1].Name != "exam_attempt_focus_warning" || events[1].UserID != result.CandidateUserID.String() {
		t.Fatalf("events=%#v", events)
	}
	for _, event := range events {
		for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "threshold"} {
			if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
				t.Fatalf("Focus Loss effect contains %q: %s", forbidden, event.Data)
			}
		}
	}
}

func TestFocusLossSuspensionEffectPublishesCloseFlagAndSeparatedSuspensions(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 20, 10, 5, 0, 0, time.UTC)
	flagID := model.NewIntegrityFlagID()
	result := examattempt.FocusLossEvaluation{SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		AttemptID: model.NewExamAttemptID(), ReceivedAt: at, PolicyOutcome: model.IntegrityOutcomeFlagAndSuspend,
		Flag: model.IntegrityFlag{ID: flagID}, RetainedEvidenceCount: 3, FlagCreated: true,
		ManagerNotificationRequired: true, ConnectionClosed: true,
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionClosePolicySuspended},
		Attempt: model.ExamAttempt{Revision: 2}, SuspensionCreated: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), FlagID: flagID,
			CandidateReason: model.AttemptSuspensionCandidateReasonFocusLossPolicy}}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).FocusLossEvaluated(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 4 || events[0].Name != "exam_attempt_connection_closed" ||
		events[1].Name != "exam_attempt_integrity_flagged" || events[2].Name != "exam_attempt_suspended" ||
		events[3].Name != "exam_attempt_access_suspended" || events[3].UserID != result.CandidateUserID.String() {
		t.Fatalf("events=%#v", events)
	}
	if encoded := string(events[3].Data); !strings.Contains(encoded, `"reason_code":"focus_policy_review_required"`) {
		t.Fatalf("candidate suspension lacks neutral reason: %s", encoded)
	}
	for _, event := range events {
		for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "threshold", "private"} {
			if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
				t.Fatalf("Focus Loss suspension effect contains %q: %s", forbidden, event.Data)
			}
		}
	}
}

func TestParticipationExpiryEffectOmitsDuplicateCloseAfterTransportTeardown(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := examattempt.ParticipationExpiry{SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
		Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 2},
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at.Add(-time.Second)), CloseReason: model.AttemptConnectionCloseTransport},
		Flag: model.IntegrityFlag{ID: model.NewIntegrityFlagID()},
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(),
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost}}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).ParticipationExpired(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 2 || events[0].Name != "exam_attempt_suspended" || events[1].Name != "exam_attempt_access_suspended" {
		t.Fatalf("events = %#v", events)
	}
}

func examAttemptPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
}

type examAttemptUseCasesFake struct {
	connects             []examattempt.ConnectCommand
	renewals             []examattempt.RenewParticipationCommand
	renewResult          examattempt.ParticipationRenewal
	focusLoss            []examattempt.FocusLossCommand
	focusResult          examattempt.FocusLossEvaluation
	reallows             []examattempt.ReallowCommand
	err                  error
	workspaceDirectories []examattempt.CreateWorkspaceDirectoryCommand
}

func (fake *examAttemptUseCasesFake) Connect(_ context.Context, _ examattempt.Call, command examattempt.ConnectCommand) (examattempt.ConnectionResult, error) {
	fake.connects = append(fake.connects, command)
	return examattempt.ConnectionResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) RenewParticipation(_ context.Context, _ examattempt.Call, command examattempt.RenewParticipationCommand) (examattempt.ParticipationRenewal, error) {
	fake.renewals = append(fake.renewals, command)
	return fake.renewResult, fake.err
}

func (fake *examAttemptUseCasesFake) EvaluateFocusLoss(_ context.Context, _ examattempt.Call,
	command examattempt.FocusLossCommand,
) (examattempt.FocusLossEvaluation, error) {
	fake.focusLoss = append(fake.focusLoss, command)
	return fake.focusResult, fake.err
}

func (fake *examAttemptUseCasesFake) Reallow(_ context.Context, _ examattempt.Call, command examattempt.ReallowCommand) (examattempt.ReallowResult, error) {
	fake.reallows = append(fake.reallows, command)
	return examattempt.ReallowResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) ScanExpiredParticipations(context.Context, int) (examattempt.ExpiryScanResult, error) {
	return examattempt.ExpiryScanResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) CloseConnection(context.Context, examattempt.Call, examattempt.CloseConnectionCommand) (examattempt.ConnectionClosedResult, error) {
	return examattempt.ConnectionClosedResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error) {
	return examattempt.Presentation{}, fake.err
}

func (fake *examAttemptUseCasesFake) ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error) {
	return examattempt.WorkspacePage{}, fake.err
}

func (fake *examAttemptUseCasesFake) ListWorkspaceJournal(context.Context, examattempt.Call, examattempt.WorkspaceJournalQuery) (examattempt.WorkspaceJournalPage, error) {
	return examattempt.WorkspaceJournalPage{}, fake.err
}

func (fake *examAttemptUseCasesFake) CreateWorkspaceDirectory(_ context.Context, _ examattempt.Call, command examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error) {
	fake.workspaceDirectories = append(fake.workspaceDirectories, command)
	return examattempt.WorkspaceMutationResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) CreateWorkspaceFile(context.Context, examattempt.Call, examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) ReplaceWorkspaceFile(context.Context, examattempt.Call, examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) MoveWorkspaceEntry(context.Context, examattempt.Call, examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) DeleteWorkspaceEntry(context.Context, examattempt.Call, examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) OpenResource(context.Context, examattempt.Call, examattempt.CandidateAccess, model.ExamResourceID) (*examattempt.OpenedContent, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) OpenWorkspaceFile(context.Context, examattempt.Call, examattempt.CandidateAccess, model.AttemptWorkspaceEntryID) (*examattempt.OpenedContent, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) GetManaged(context.Context, examattempt.Call, examattempt.GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) ListManaged(context.Context, examattempt.Call, examattempt.ListManagedAttemptsQuery) (examattempt.ManagedAttemptPage, error) {
	return examattempt.ManagedAttemptPage{}, fake.err
}

var _ examAttemptUseCases = (*examAttemptUseCasesFake)(nil)
