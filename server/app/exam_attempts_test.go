// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAttemptSubmissionAuthorizationResolvesCanonicalExamBeforeCurrentManagerCheck(t *testing.T) {
	t.Parallel()

	submissionID, examID := model.NewSubmissionID(), model.NewExamID()
	sittings := &submissionAuthorizationSittingFake{}
	adapter := examAttemptManagerAuthorizationAdapter{
		sittings: sittings,
		audit:    &submissionAuthorizationAuditFake{},
		submissions: &submissionAuthorizationStoreFake{authorization: &store.ExamSubmissionAuthorization{
			SubmissionID: submissionID, ExamID: examID, SittingID: model.NewExamSittingID(),
			AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID(),
		}},
	}
	principal := examAttemptPrincipal()
	call := NewInvocation(principal, model.RequestMetadata{RequestID: "submission-view"})
	if err := adapter.AuthorizeSubmissionView(context.Background(), call, submissionID); err != nil {
		t.Fatal(err)
	}
	if sittings.examID != examID || sittings.submissionID != submissionID ||
		sittings.invocation.Principal().UserID != principal.UserID {
		t.Fatalf("authorization call = exam %s submission %s principal %s", sittings.examID,
			sittings.submissionID, sittings.invocation.Principal().UserID)
	}
}

type submissionAuthorizationAuditFake struct {
	principal model.Principal
	action    model.Action
	resource  model.Resource
	scopeType model.RoleScopeType
	scopeID   string
	metadata  model.RequestMetadata
	allowed   bool
	err       error
}

func (fake *submissionAuthorizationAuditFake) RecordAuthorizationDecision(_ context.Context, principal model.Principal,
	action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string,
	metadata model.RequestMetadata, allowed bool,
) error {
	fake.principal, fake.action, fake.resource = principal, action, resource
	fake.scopeType, fake.scopeID, fake.metadata, fake.allowed = scopeType, scopeID, metadata, allowed
	return fake.err
}

func TestExamAttemptSubmissionAuthorizationAuditsSelfDenialFailClosed(t *testing.T) {
	t.Parallel()
	principal := examAttemptPrincipal()
	submissionID, unitID := model.NewSubmissionID(), model.NewAcademicUnitID()
	authorization := &store.ExamSubmissionAuthorization{
		SubmissionID: submissionID, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		AttemptID: model.NewExamAttemptID(), CandidateUserID: principal.UserID, AcademicUnitID: unitID,
	}
	metadata := model.RequestMetadata{RequestID: "self-submission-view"}
	t.Run("records denial before concealment", func(t *testing.T) {
		t.Parallel()
		audit := &submissionAuthorizationAuditFake{}
		adapter := examAttemptManagerAuthorizationAdapter{sittings: &submissionAuthorizationSittingFake{},
			submissions: &submissionAuthorizationStoreFake{authorization: authorization}, audit: audit}
		err := adapter.AuthorizeSubmissionView(context.Background(), NewInvocation(principal, metadata), submissionID)
		var fault *apperror.Error
		if !errors.As(err, &fault) || fault.Code() != "exam.attempt.not_found" {
			t.Fatalf("AuthorizeSubmissionView() error = %v, want concealed not found", err)
		}
		if audit.principal.UserID != principal.UserID || audit.action != model.ActionSubmissionView ||
			audit.resource != (model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}) ||
			audit.scopeType != model.RoleScopeAcademicUnit || audit.scopeID != unitID.String() ||
			audit.metadata.RequestID != metadata.RequestID || audit.allowed {
			t.Fatalf("denied authorization audit = %#v", audit)
		}
	})
	t.Run("audit failure fails closed", func(t *testing.T) {
		t.Parallel()
		auditErr := errors.New("audit unavailable")
		adapter := examAttemptManagerAuthorizationAdapter{sittings: &submissionAuthorizationSittingFake{},
			submissions: &submissionAuthorizationStoreFake{authorization: authorization},
			audit:       &submissionAuthorizationAuditFake{err: auditErr}}
		if err := adapter.AuthorizeSubmissionView(context.Background(), NewInvocation(principal, metadata), submissionID); !errors.Is(err, auditErr) {
			t.Fatalf("AuthorizeSubmissionView() error = %v, want audit failure", err)
		}
	})
}

type submissionAuthorizationStoreFake struct {
	store.ExamSubmissionStore
	authorization *store.ExamSubmissionAuthorization
}

func (fake *submissionAuthorizationStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	return fake.authorization, nil
}

type submissionAuthorizationSittingFake struct {
	examSittingUseCases
	invocation   Invocation
	examID       model.ExamID
	submissionID model.SubmissionID
}

func (fake *submissionAuthorizationSittingFake) AuthorizeSubmissionView(_ context.Context, invocation Invocation,
	examID model.ExamID, submissionID model.SubmissionID,
) error {
	fake.invocation, fake.examID, fake.submissionID = invocation, examID, submissionID
	return nil
}

func TestExamAttemptFacadeConcealsAccessDenialsAndPassesOtherFailures(t *testing.T) {
	t.Parallel()
	for _, childCode := range []apperror.Code{"exam.attempt.not_found", "exam.attempt.continuity_invalid", "authorization.denied"} {
		if err := examAttemptError(apperror.New(childCode), true); !apperror.Is(err, "resource.not_found") {
			t.Fatalf("concealed child=%s error=%v", childCode, err)
		}
		if err := examAttemptError(apperror.New(childCode), false); !apperror.Is(err, childCode) {
			t.Fatalf("unconcealed child=%s error=%v", childCode, err)
		}
	}
	if err := examAttemptError(apperror.New("idempotency.key_required"), true); !apperror.Is(err, "idempotency.key_required") {
		t.Fatalf("idempotency failure = %v", err)
	}
	if err := examAttemptError(errors.New("store offline"), true); !apperror.Is(err, "exam.attempt.unavailable") {
		t.Fatalf("unclassified failure = %v", err)
	}
	if err := examAttemptError(nil, true); err != nil {
		t.Fatalf("nil failure = %v", err)
	}
}

func TestParticipationExpiryEffectPublishesCloseManagerSuspensionAndCandidateSuspension(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := examattempt.ParticipationExpiry{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
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
	if len(events) != 6 || events[0].Name != "exam_attempt_connection_closed" || events[0].Action != model.ActionExamSittingView ||
		events[1].Name != "exam_attempt_suspended" || events[1].Action != model.ActionExamSittingView ||
		events[2].Name != "exam_attempt_access_suspended" || events[2].Action != model.ActionExamSittingParticipate ||
		events[2].UserID != result.CandidateUserID.String() || events[3].Name != "candidate.exam_activity.changed" ||
		events[4].Name != "manager.sitting_board.changed" || events[5].Name != "current_user.context.changed" {
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

func TestManualSuspensionEffectPublishesKickedCloseAndSafeCandidateReason(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := examattempt.ManualSuspensionResult{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		CandidateUserID: model.NewUserID(), Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 3},
		Connection: &store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionCloseKicked},
		ConnectionClosed: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), Source: model.AttemptSuspensionSourceManual,
			CandidateReason: model.AttemptSuspensionCandidateReasonManagerSuspended, StartedAt: at, SuspendedByUserID: model.NewUserID()}}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).AttemptSuspendedByManager(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 6 || events[0].Name != "exam_attempt_connection_closed" || events[1].Name != "exam_attempt_suspended" ||
		events[2].Name != "exam_attempt_access_suspended" || events[2].UserID != result.CandidateUserID.String() ||
		strings.Contains(string(events[1].Data), "integrity_flag_id") ||
		!strings.Contains(string(events[0].Data), `"kicked"`) ||
		!strings.Contains(string(events[2].Data), `"reason_code":"manager_suspended"`) {
		t.Fatalf("events = %#v", events)
	}
}

func TestSubmissionEffectPublishesSafeManagerAndCandidateFactsThenUnbindsExactConnection(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	result := examattempt.SubmissionResult{Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		SubmissionID: model.NewSubmissionID(), AttemptID: model.NewExamAttemptID(), State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 13, ManifestDigest: strings.Repeat("d", 64),
		SubmittedAt: time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC),
	}, Provenance: model.ExamSubmissionCandidateSubmitted, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		ConnectionID: model.NewAttemptConnectionID()}

	if err := (examAttemptRealtimeEffects{realtime: realtime}).AttemptSubmitted(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	if len(events) != 5 || events[0].Name != "exam_attempt_submitted" || events[0].UserID != "" ||
		events[1].Name != "exam_attempt_submitted" || events[1].UserID != result.CandidateUserID.String() ||
		events[2].Name != "candidate.exam_activity.changed" || events[3].Name != "manager.sitting_board.changed" ||
		events[4].Name != "current_user.context.changed" ||
		len(unbound) != 1 || unbound[0] != result.ConnectionID {
		t.Fatalf("events=%#v unbound=%#v", events, unbound)
	}
	for _, event := range events {
		encoded := strings.ToLower(string(event.Data))
		for _, forbidden := range []string{"path", "content", "source", "evidence", "sequence", "credential", "session", "private"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("Submission event contains %q: %s", forbidden, event.Data)
			}
		}
	}
}

func TestAutomaticSubmissionEffectPublishesSittingClosedFactsAndUnbindsExactConnection(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	result := examattempt.AutomaticSubmissionResult{SubmissionResult: examattempt.SubmissionResult{
		Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: model.NewSubmissionID(), AttemptID: model.NewExamAttemptID(),
			State: model.ExamAttemptSubmitted, WorkspaceCursor: 21, ManifestDigest: strings.Repeat("e", 64),
			SubmittedAt: time.Date(2026, time.August, 22, 11, 0, 0, 0, time.UTC)},
		Provenance: model.ExamSubmissionSittingClosed, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		CandidateUserID: model.NewUserID(), ConnectionID: model.NewAttemptConnectionID()},
		ConnectionClosed: true}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).AttemptSealedForSittingClose(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	if len(events) != 6 || events[0].Name != "exam_attempt_submitted" || events[0].UserID != "" ||
		events[1].Name != "exam_attempt_submitted" || events[1].UserID != result.CandidateUserID.String() ||
		events[2].Name != "exam_attempt_connection_closed" || events[3].Name != "candidate.exam_activity.changed" ||
		events[4].Name != "manager.sitting_board.changed" || events[5].Name != "current_user.context.changed" ||
		len(unbound) != 1 || unbound[0] != result.ConnectionID {
		t.Fatalf("events=%#v unbound=%#v", events, unbound)
	}
	if encoded := string(events[2].Data); !strings.Contains(encoded, `"close_reason":"sitting_closed"`) {
		t.Fatalf("automatic close event = %s", encoded)
	}
	for _, event := range events {
		encoded := strings.ToLower(string(event.Data))
		for _, forbidden := range []string{"path", "content", "source", "evidence", "sequence", "credential", "session", "private"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("automatic Submission event contains %q: %s", forbidden, event.Data)
			}
		}
	}
}

func TestFocusLossWarningEffectPublishesBoundedManagerFlagAndNeutralCandidateWarning(t *testing.T) {
	t.Parallel()
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	result := examattempt.FocusLossEvaluation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		AttemptID: model.NewExamAttemptID(), ReceivedAt: at, PolicyOutcome: model.IntegrityOutcomeFlagAndWarn,
		Flag: model.IntegrityFlag{ID: model.NewIntegrityFlagID()}, RetainedEvidenceCount: 3,
		FlagCreated: true, ManagerNotificationRequired: true, CandidateWarningCreated: true}
	if err := (examAttemptRealtimeEffects{realtime: realtime}).FocusLossEvaluated(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	events := append([]apprealtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	if len(events) != 3 || events[0].Name != "exam_attempt_integrity_flagged" ||
		events[1].Name != "exam_attempt_focus_warning" || events[1].UserID != result.CandidateUserID.String() ||
		events[2].Name != "manager.sitting_board.changed" {
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
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 20, 10, 5, 0, 0, time.UTC)
	flagID := model.NewIntegrityFlagID()
	result := examattempt.FocusLossEvaluation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
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
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	if len(events) != 6 || events[0].Name != "exam_attempt_connection_closed" ||
		events[1].Name != "exam_attempt_integrity_flagged" || events[2].Name != "exam_attempt_suspended" ||
		events[3].Name != "exam_attempt_access_suspended" || events[3].UserID != result.CandidateUserID.String() ||
		events[4].Name != "manager.sitting_board.changed" || events[5].Name != "candidate.exam_activity.changed" ||
		len(unbound) != 1 || unbound[0] != result.Connection.ID {
		t.Fatalf("events=%#v unbound=%#v", events, unbound)
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
	if err := realtime.AttachRealtimeSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := examattempt.ParticipationExpiry{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
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
	if len(events) != 5 || events[0].Name != "exam_attempt_suspended" || events[1].Name != "exam_attempt_access_suspended" ||
		events[2].Name != "candidate.exam_activity.changed" || events[3].Name != "manager.sitting_board.changed" ||
		events[4].Name != "current_user.context.changed" {
		t.Fatalf("events = %#v", events)
	}
}

func examAttemptPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
}
