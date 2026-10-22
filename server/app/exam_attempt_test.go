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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
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
	call := invocation.New(principal, model.RequestMetadata{RequestID: "submission-view"})
	require.NoError(t, adapter.AuthorizeSubmissionView(context.Background(), call, submissionID))
	require.Equal(t, examID, sittings.examID, "authorization call = exam")
	require.Equal(t, submissionID, sittings.submissionID, "authorization call = exam")
	require.Equal(t, principal.UserID, sittings.invocation.Principal().UserID, "authorization call = exam")
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

func (f *submissionAuthorizationAuditFake) RecordAuthorizationDecision(_ context.Context, principal model.Principal,
	action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string,
	metadata model.RequestMetadata, allowed bool,
) error {
	f.principal, f.action, f.resource = principal, action, resource
	f.scopeType, f.scopeID, f.metadata, f.allowed = scopeType, scopeID, metadata, allowed
	return f.err
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
		err := adapter.AuthorizeSubmissionView(context.Background(), invocation.New(principal, metadata), submissionID)
		var fault *apperror.Error
		require.ErrorAs(t, err, &fault, "AuthorizeSubmissionView() error")
		require.Equal(t, "exam.attempt.not_found", fault.Code(), "AuthorizeSubmissionView() error")
		require.Equal(t, principal.UserID, audit.principal.UserID)
		require.Equal(t, model.ActionSubmissionView, audit.action)
		require.Equal(t, model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, audit.resource)
		require.Equal(t, model.RoleScopeAcademicUnit, audit.scopeType)
		require.Equal(t, unitID.String(), audit.scopeID)
		require.Equal(t, metadata.RequestID, audit.metadata.RequestID)
		require.False(t, audit.allowed)
	})
	t.Run("audit failure fails closed", func(t *testing.T) {
		t.Parallel()
		auditErr := errors.New("audit unavailable")
		adapter := examAttemptManagerAuthorizationAdapter{sittings: &submissionAuthorizationSittingFake{},
			submissions: &submissionAuthorizationStoreFake{authorization: authorization},
			audit:       &submissionAuthorizationAuditFake{err: auditErr}}
		require.ErrorIs(t, adapter.AuthorizeSubmissionView(context.Background(), invocation.New(principal, metadata), submissionID), auditErr, "AuthorizeSubmissionView() error")
	})
}

type submissionAuthorizationStoreFake struct {
	store.ExamSubmissionStore
	authorization *store.ExamSubmissionAuthorization
}

func (f *submissionAuthorizationStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	return f.authorization, nil
}

type submissionAuthorizationSittingFake struct {
	examAttemptSittingAuthorizer
	invocation   invocation.Invocation
	examID       model.ExamID
	submissionID model.SubmissionID
}

func (f *submissionAuthorizationSittingFake) AuthorizeSubmissionView(_ context.Context, invocation invocation.Invocation,
	examID model.ExamID, submissionID model.SubmissionID,
) error {
	f.invocation, f.examID, f.submissionID = invocation, examID, submissionID
	return nil
}

func TestParticipationExpiryEffectPublishesCloseManagerSuspensionAndCandidateSuspension(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := attempt.ParticipationExpiry{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
		Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 2},
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionCloseLeaseExpired},
		ConnectionClosed: true,
		Flag:             model.IntegrityFlag{ID: model.NewIntegrityFlagID()},
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), Source: model.AttemptSuspensionSourcePolicy,
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost}}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).ParticipationExpired(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 6)
	require.Equal(t, "exam_attempt_connection_closed", events[0].Name)
	require.Equal(t, model.ActionExamSittingView, events[0].Action)
	require.Equal(t, "exam_attempt_suspended", events[1].Name)
	require.Equal(t, model.ActionExamSittingView, events[1].Action)
	require.Equal(t, "exam_attempt_access_suspended", events[2].Name)
	require.Equal(t, model.ActionExamSittingParticipate, events[2].Action)
	require.Equal(t, result.CandidateUserID, events[2].UserID)
	require.Equal(t, "candidate.exam_activity.changed", events[3].Name)
	require.Equal(t, "manager.sitting_board.changed", events[4].Name)
	require.Equal(t, "current_user.context.changed", events[5].Name)
	for _, event := range events {
		for _, forbidden := range []string{"continuity_credential", "credential_hash", "private_reason"} {
			require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "expiry event contains %q: %s", forbidden, event.Data)
		}
	}
	require.Contains(t, string(events[2].Data), `"reason_code":"secure_connectivity_lost"`)
}

func TestManualSuspensionEffectPublishesKickedCloseAndSafeCandidateReason(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := attempt.ManualSuspensionResult{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		CandidateUserID: model.NewUserID(), Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 3},
		Connection: &store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionCloseKicked},
		ConnectionClosed: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), Source: model.AttemptSuspensionSourceManual,
			CandidateReason: model.AttemptSuspensionCandidateReasonManagerSuspended, StartedAt: at, SuspendedByUserID: model.NewUserID()}}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).AttemptSuspendedByManager(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 6)
	require.Equal(t, "exam_attempt_connection_closed", events[0].Name)
	require.Equal(t, "exam_attempt_suspended", events[1].Name)
	require.Equal(t, "exam_attempt_access_suspended", events[2].Name)
	require.Equal(t, result.CandidateUserID, events[2].UserID)
	require.NotContains(t, string(events[1].Data), "integrity_flag_id")
	require.Contains(t, string(events[0].Data), `"kicked"`)
	require.Contains(t, string(events[2].Data), `"reason_code":"manager_suspended"`)
}

func TestSubmissionEffectPublishesSafeManagerAndCandidateFactsThenUnbindsExactConnection(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	result := attempt.SubmissionResult{Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		SubmissionID: model.NewSubmissionID(), AttemptID: model.NewExamAttemptID(), State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 13, ManifestDigest: strings.Repeat("d", 64),
		SubmittedAt: time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC),
	}, Provenance: model.ExamSubmissionCandidateSubmitted, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		ConnectionID: model.NewAttemptConnectionID()}

	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).AttemptSubmitted(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	require.Len(t, events, 5, "events")
	require.Equal(t, "exam_attempt_submitted", events[0].Name, "events")
	require.Empty(t, events[0].UserID, "events")
	require.Equal(t, "exam_attempt_submitted", events[1].Name, "events")
	require.Equal(t, result.CandidateUserID, events[1].UserID, "events")
	require.Equal(t, "candidate.exam_activity.changed", events[2].Name, "events")
	require.Equal(t, "manager.sitting_board.changed", events[3].Name, "events")
	require.Equal(t, "current_user.context.changed", events[4].Name, "events")
	require.Len(t, unbound, 1, "events")
	require.Equal(t, result.ConnectionID, unbound[0], "events")
	for _, event := range events {
		encoded := strings.ToLower(string(event.Data))
		for _, forbidden := range []string{"path", "content", "source", "evidence", "sequence", "credential", "session", "private"} {
			require.NotContains(t, encoded, forbidden, "Submission event contains %q: %s", forbidden, event.Data)
		}
	}
}

func TestAutomaticSubmissionEffectPublishesSittingClosedFactsAndUnbindsExactConnection(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	result := attempt.AutomaticSubmissionResult{SubmissionResult: attempt.SubmissionResult{
		Receipt: store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: model.NewSubmissionID(), AttemptID: model.NewExamAttemptID(),
			State: model.ExamAttemptSubmitted, WorkspaceCursor: 21, ManifestDigest: strings.Repeat("e", 64),
			SubmittedAt: time.Date(2026, time.August, 22, 11, 0, 0, 0, time.UTC)},
		Provenance: model.ExamSubmissionSittingClosed, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		CandidateUserID: model.NewUserID(), ConnectionID: model.NewAttemptConnectionID()},
		ConnectionClosed: true}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).AttemptSealedForSittingClose(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	require.Len(t, events, 6, "events")
	require.Equal(t, "exam_attempt_submitted", events[0].Name, "events")
	require.Empty(t, events[0].UserID, "events")
	require.Equal(t, "exam_attempt_submitted", events[1].Name, "events")
	require.Equal(t, result.CandidateUserID, events[1].UserID, "events")
	require.Equal(t, "exam_attempt_connection_closed", events[2].Name, "events")
	require.Equal(t, "candidate.exam_activity.changed", events[3].Name, "events")
	require.Equal(t, "manager.sitting_board.changed", events[4].Name, "events")
	require.Equal(t, "current_user.context.changed", events[5].Name, "events")
	require.Len(t, unbound, 1, "events")
	require.Equal(t, result.ConnectionID, unbound[0], "events")
	require.Contains(t, string(events[2].Data), `"close_reason":"sitting_closed"`)
	for _, event := range events {
		encoded := strings.ToLower(string(event.Data))
		for _, forbidden := range []string{"path", "content", "source", "evidence", "sequence", "credential", "session", "private"} {
			require.NotContains(t, encoded, forbidden, "automatic Submission event contains %q: %s", forbidden, event.Data)
		}
	}
}

func TestFocusLossWarningEffectPublishesBoundedManagerFlagAndNeutralCandidateWarning(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	at := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	result := attempt.FocusLossEvaluation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		AttemptID: model.NewExamAttemptID(), ReceivedAt: at, PolicyOutcome: model.IntegrityOutcomeFlagAndWarn,
		Flag: model.IntegrityFlag{ID: model.NewIntegrityFlagID()}, RetainedEvidenceCount: 3,
		FlagCreated: true, ManagerNotificationRequired: true, CandidateWarningCreated: true}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).FocusLossEvaluated(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 3)
	require.Equal(t, "exam_attempt_integrity_flagged", events[0].Name)
	require.Equal(t, "exam_attempt_focus_warning", events[1].Name)
	require.Equal(t, result.CandidateUserID, events[1].UserID)
	require.Equal(t, "manager.sitting_board.changed", events[2].Name)
	for _, event := range events {
		for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "threshold"} {
			require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "Focus Loss effect contains %q: %s", forbidden, event.Data)
		}
	}
}

func TestFocusLossSuspensionEffectPublishesCloseFlagAndSeparatedSuspensions(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	at := time.Date(2026, time.August, 20, 10, 5, 0, 0, time.UTC)
	flagID := model.NewIntegrityFlagID()
	result := attempt.FocusLossEvaluation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(),
		AttemptID: model.NewExamAttemptID(), ReceivedAt: at, PolicyOutcome: model.IntegrityOutcomeFlagAndSuspend,
		Flag: model.IntegrityFlag{ID: flagID}, RetainedEvidenceCount: 3, FlagCreated: true,
		ManagerNotificationRequired: true, ConnectionClosed: true,
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at), CloseReason: model.AttemptConnectionClosePolicySuspended},
		Attempt: model.ExamAttempt{Revision: 2}, SuspensionCreated: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), Source: model.AttemptSuspensionSourcePolicy, FlagID: flagID,
			CandidateReason: model.AttemptSuspensionCandidateReasonFocusLossPolicy}}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).FocusLossEvaluated(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	unbound := append([]model.AttemptConnectionID(nil), sink.attemptUnbinds...)
	sink.mu.Unlock()
	require.Len(t, events, 6, "events")
	require.Equal(t, "exam_attempt_connection_closed", events[0].Name, "events")
	require.Equal(t, "exam_attempt_integrity_flagged", events[1].Name, "events")
	require.Equal(t, "exam_attempt_suspended", events[2].Name, "events")
	require.Equal(t, "exam_attempt_access_suspended", events[3].Name, "events")
	require.Equal(t, result.CandidateUserID, events[3].UserID, "events")
	require.Equal(t, "manager.sitting_board.changed", events[4].Name, "events")
	require.Equal(t, "candidate.exam_activity.changed", events[5].Name, "events")
	require.Len(t, unbound, 1, "events")
	require.Equal(t, result.Connection.ID, unbound[0], "events")
	require.Contains(t, string(events[3].Data), `"reason_code":"focus_policy_review_required"`)
	for _, event := range events {
		for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "threshold", "private"} {
			require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "Focus Loss suspension effect contains %q: %s", forbidden, event.Data)
		}
	}
}

func TestParticipationExpiryEffectOmitsDuplicateCloseAfterTransportTeardown(t *testing.T) {
	t.Parallel()
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	result := attempt.ParticipationExpiry{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), CandidateUserID: model.NewUserID(), DatabaseTime: at,
		Attempt: model.ExamAttempt{ID: model.NewExamAttemptID(), Revision: 2},
		Connection: store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(at.Add(-time.Second)), CloseReason: model.AttemptConnectionCloseTransport},
		Flag: model.IntegrityFlag{ID: model.NewIntegrityFlagID()},
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), Source: model.AttemptSuspensionSourcePolicy,
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost}}
	require.NoError(t, (examAttemptRealtimeEffects{realtime: service}).ParticipationExpired(context.Background(), result))
	sink.mu.Lock()
	events := append([]realtime.RealtimeEvent(nil), sink.events...)
	sink.mu.Unlock()
	require.Len(t, events, 5)
	require.Equal(t, "exam_attempt_suspended", events[0].Name)
	require.Equal(t, "exam_attempt_access_suspended", events[1].Name)
	require.Equal(t, "candidate.exam_activity.changed", events[2].Name)
	require.Equal(t, "manager.sitting_board.changed", events[3].Name)
	require.Equal(t, "current_user.context.changed", events[4].Name)
}

func examAttemptPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
}
