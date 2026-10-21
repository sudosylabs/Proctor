// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestCandidateSittingEventsUseRelationshipOnlyTargetAndSafePayload(t *testing.T) {
	t.Parallel()
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	previousRevisionID, revisionID := model.NewExamRevisionID(), model.NewExamRevisionID()
	at := time.Date(2026, time.August, 17, 9, 5, 0, 123, time.UTC)

	corrected, err := NewCandidateExamSittingContentCorrectedEvent(
		examID, sittingID, previousRevisionID, revisionID, 7, at,
	)
	require.NoError(t, err)
	assertCandidateSittingEvent(t, corrected, "exam_sitting_content_corrected", sittingID)
	for _, forbidden := range []string{"credential", "hash", "instructions", "resources", "private_reason"} {
		require.NotContains(t, string(corrected.Data), forbidden, "correction payload contains")
	}

	lifecycle, err := NewCandidateExamSittingLifecycleChangedEvent(
		examID, sittingID, model.ExamSittingPaused, 8, "manager_paused", at.Add(time.Hour), at,
	)
	require.NoError(t, err)
	assertCandidateSittingEvent(t, lifecycle, "exam_sitting_lifecycle_changed", sittingID)
	require.NotContains(t, string(lifecycle.Data), "private", "lifecycle payload exposes a private reason")
}

func TestManagerConnectionEventsAreContentAndCredentialFree(t *testing.T) {
	t.Parallel()
	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	candidateID, connectionID := model.NewUserID(), model.NewAttemptConnectionID()
	at := time.Date(2026, time.August, 17, 9, 5, 0, 123, time.UTC)

	opened, err := NewExamAttemptConnectionOpenedEvent(sittingID, attemptID, candidateID, connectionID, at)
	require.NoError(t, err)
	closed, err := NewExamAttemptConnectionClosedEvent(sittingID, attemptID, candidateID, connectionID,
		model.AttemptConnectionCloseTransport, at.Add(time.Minute))
	require.NoError(t, err)
	for _, event := range []RealtimeEvent{opened, closed} {
		require.Equal(t, model.ActionExamSittingView, event.Action)
		require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, event.Resource)
		for _, forbidden := range []string{"credential", "hash", "participation_id", "generation", "session_id", "private"} {
			require.NotContains(t, string(event.Data), forbidden, "event payload contains")
		}
		require.NoError(t, event.ValidateForPublish())
	}
}

func TestExamAttemptSuspensionAndReallowEventsSeparateCandidateAndManagerPayloads(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	connectionID, flagID, suspensionID := model.NewAttemptConnectionID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	privateReason := "manager verified secure continuity"
	at := time.Date(2026, time.August, 18, 9, 5, 0, 0, time.UTC)
	candidateSuspended, err := NewCandidateExamAttemptSuspendedEvent(sittingID, attemptID, candidateID,
		model.AttemptSuspensionSourcePolicy, model.AttemptSuspensionCandidateReasonSecureContinuityLost, at)
	require.NoError(t, err)
	managerSuspended, err := NewExamAttemptPolicySuspendedEvent(sittingID, attemptID, candidateID, connectionID, flagID, suspensionID, 2, at)
	require.NoError(t, err)
	candidateReallowed, err := NewCandidateExamAttemptReallowedEvent(sittingID, attemptID, candidateID, at.Add(time.Minute))
	require.NoError(t, err)
	managerReallowed, err := NewExamAttemptReallowedEvent(sittingID, attemptID, candidateID, suspensionID, 3, at.Add(time.Minute))
	require.NoError(t, err)
	for _, event := range []RealtimeEvent{candidateSuspended, candidateReallowed} {
		require.Equal(t, candidateID.String(), event.UserID)
		require.Equal(t, model.ActionExamSittingParticipate, event.Action)
		for _, forbidden := range []string{"integrity_flag_id", "suspension_id", "attempt_connection_id", "generation", "private_reason", "credential", "hash"} {
			require.NotContains(t, string(event.Data), forbidden, "candidate payload contains")
		}
		require.NotContains(t, string(event.Data), privateReason, "candidate payload contains private manager reason")
	}
	var suspendedData map[string]any
	err = json.Unmarshal(candidateSuspended.Data, &suspendedData)
	require.NoError(t, err)
	require.Equal(t, string(model.AttemptSuspensionCandidateReasonSecureContinuityLost), suspendedData["reason_code"])
	for key := range suspendedData {
		switch key {
		case "exam_sitting_id", "exam_attempt_id", "state", "reason_code", "changed_at":
		default:
			require.FailNow(t, fmt.Sprintf("candidate suspension payload contains unsafe field %q: %s", key, candidateSuspended.Data))
		}
	}
	for _, event := range []RealtimeEvent{managerSuspended, managerReallowed} {
		require.Empty(t, event.UserID)
		require.Equal(t, model.ActionExamSittingView, event.Action)
		for _, forbidden := range []string{"private_reason", "credential", "hash", "session_id", "evidence"} {
			require.NotContains(t, string(event.Data), forbidden, "manager payload contains")
		}
		require.NotContains(t, string(event.Data), privateReason, "manager payload contains private manager reason")
		require.NoError(t, event.ValidateForPublish())
	}
}

func TestCandidateExamAttemptSuspendedEventAcceptsFocusPolicyReviewReason(t *testing.T) {
	t.Parallel()
	event, err := NewCandidateExamAttemptSuspendedEvent(model.NewExamSittingID(), model.NewExamAttemptID(),
		model.NewUserID(), model.AttemptSuspensionSourcePolicy, model.AttemptSuspensionCandidateReasonFocusLossPolicy, time.Now().UTC())
	require.NoError(t, err)
	var data map[string]any
	err = json.Unmarshal(event.Data, &data)
	require.NoError(t, err)
	require.Equal(t, string(model.AttemptSuspensionCandidateReasonFocusLossPolicy), data["reason_code"], "reason_code=%#v data=%s", data["reason_code"], event.Data)
}

func TestCandidateExamAttemptWorkspaceChangedEventIsOnlyASafeRefetchHint(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	entryID := model.NewAttemptWorkspaceEntryID()
	at := time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC)
	event, err := NewCandidateExamAttemptWorkspaceChangedEvent(sittingID, attemptID, candidateID, entryID,
		model.AttemptWorkspaceMutationReplaceFile, 17, at)
	require.NoError(t, err)
	require.Equal(t, "exam_attempt_workspace_changed", event.Name)
	require.Equal(t, candidateID.String(), event.UserID)
	require.Equal(t, model.ActionExamSittingParticipate, event.Action)
	require.Equal(t, sittingID.String(), event.Resource.ID)
	data := string(event.Data)
	for _, forbidden := range []string{"path", "object", "content_version", "sha256", "credential", "session", "generation"} {
		require.NotContains(t, strings.ToLower(data), forbidden, "workspace hint contains")
	}
}

func TestExamAttemptSubmittedEventsSeparateManagerAndCandidateTargetsWithSafeReceiptData(t *testing.T) {
	t.Parallel()

	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	candidateID, submissionID := model.NewUserID(), model.NewSubmissionID()
	digest := strings.Repeat("d", 64)
	submittedAt := time.Date(2026, time.August, 21, 10, 15, 0, 123, time.UTC)
	manager, err := NewExamAttemptSubmittedEvent(sittingID, attemptID, candidateID, submissionID, 9, digest,
		model.ExamSubmissionManagerEndedAttempt, submittedAt)
	require.NoError(t, err)
	candidate, err := NewCandidateExamAttemptSubmittedEvent(sittingID, attemptID, candidateID, submissionID, 9, digest,
		model.ExamSubmissionManagerEndedAttempt, submittedAt)
	require.NoError(t, err)
	require.Equal(t, "exam_attempt_submitted", manager.Name)
	require.Empty(t, manager.UserID)
	require.Equal(t, model.ActionExamSittingView, manager.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, manager.Resource)
	require.Equal(t, "exam_attempt_submitted", candidate.Name)
	require.Equal(t, candidateID.String(), candidate.UserID)
	require.Equal(t, model.ActionExamSittingParticipate, candidate.Action)
	require.Equal(t, sittingID.String(), candidate.Resource.ID)
	var managerData, candidateData map[string]any
	err = json.Unmarshal(manager.Data, &managerData)
	require.NoError(t, err)
	err = json.Unmarshal(candidate.Data, &candidateData)
	require.NoError(t, err)
	require.Len(t, managerData, 9, "manager")
	require.Equal(t, candidateID.String(), managerData["candidate_user_id"], "manager")
	require.Equal(t, string(model.ExamSubmissionManagerEndedAttempt), managerData["provenance"], "manager")
	require.Len(t, candidateData, 8, "manager")
	require.Equal(t, string(model.ExamSubmissionManagerEndedAttempt), candidateData["provenance"], "manager")
	for _, event := range []RealtimeEvent{manager, candidate} {
		for _, forbidden := range []string{"path", "content", "source", "evidence", "gap", "sequence", "credential", "session", "private", "remark"} {
			require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "submitted event contains %q: %s", forbidden, event.Data)
		}
		err = event.ValidateForPublish()
		require.NoError(t, err)
	}
}

func TestFocusLossEventsSeparateNeutralCandidateWarningFromBoundedManagerFlag(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	flagID := model.NewIntegrityFlagID()
	at := time.Date(2026, time.August, 20, 9, 5, 0, 0, time.UTC)
	warning, err := NewCandidateExamAttemptFocusLossWarningEvent(sittingID, attemptID, candidateID, at)
	require.NoError(t, err)
	flagged, err := NewExamAttemptIntegrityFlaggedEvent(sittingID, attemptID, candidateID, flagID,
		model.IntegrityOutcomeFlagAndWarn, 100, 17, at)
	require.NoError(t, err)
	require.Equal(t, "exam_attempt_focus_warning", warning.Name)
	require.Equal(t, candidateID.String(), warning.UserID)
	require.Equal(t, model.ActionExamSittingParticipate, warning.Action)
	require.Equal(t, sittingID.String(), warning.Resource.ID)
	var warningData map[string]any
	err = json.Unmarshal(warning.Data, &warningData)
	require.NoError(t, err, "warning data")
	require.Equal(t, "focus_loss_policy_warning", warningData["warning_code"], "warning data")
	require.Len(t, warningData, 4, "warning data")
	require.Equal(t, "exam_attempt_integrity_flagged", flagged.Name)
	require.Empty(t, flagged.UserID)
	require.Equal(t, model.ActionExamSittingView, flagged.Action)
	require.Equal(t, sittingID.String(), flagged.Resource.ID)
	var managerData map[string]any
	err = json.Unmarshal(flagged.Data, &managerData)
	require.NoError(t, err, "manager data")
	require.Equal(t, "focus_loss", managerData["policy_kind"], "manager data")
	require.Equal(t, "flag_and_warn", managerData["outcome"], "manager data")
	require.Equal(t, float64(100), managerData["retained_evidence_count"], "manager data")
	require.Equal(t, float64(17), managerData["evidence_overflow_count"], "manager data")
	require.Equal(t, true, managerData["evidence_available"], "manager data")
	require.Len(t, managerData, 10, "manager data")
	for _, event := range []RealtimeEvent{warning, flagged} {
		for _, forbidden := range []string{"duration", "source", "sequence", "threshold", "credential", "hash", "session", "private", "severity", "guilt"} {
			require.NotContains(t, strings.ToLower(string(event.Data)), forbidden, "Focus Loss event contains %q: %s", forbidden, event.Data)
		}
		err = event.ValidateForPublish()
		require.NoError(t, err)
	}
}

func assertCandidateSittingEvent(t *testing.T, event RealtimeEvent, name string, sittingID model.ExamSittingID) {
	t.Helper()
	require.Equal(t, name, event.Name)
	require.Equal(t, model.ActionExamSittingParticipate, event.Action)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, event.Resource)
	require.NoError(t, event.ValidateForPublish())
}

func TestExamAttemptSuspendedEventsValidateProvenancePerSource(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	connectionID, flagID, suspensionID := model.NewAttemptConnectionID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	at := time.Date(2026, time.August, 18, 9, 5, 0, 0, time.UTC)

	_, err := NewExamAttemptPolicySuspendedEvent(sittingID, attemptID, candidateID, connectionID, "", suspensionID, 2, at)
	require.Error(t, err, "policy suspension without its Integrity Flag")
	_, err = NewExamAttemptPolicySuspendedEvent(sittingID, attemptID, candidateID, "", flagID, suspensionID, 2, at)
	require.Error(t, err, "policy suspension without its Connection")

	kicked, err := NewExamAttemptKickedEvent(sittingID, attemptID, candidateID, "", suspensionID, 2, at)
	require.NoError(t, err, "kick of a reconnecting candidate")
	var data map[string]any
	require.NoError(t, json.Unmarshal(kicked.Data, &data))
	require.NotContains(t, data, "integrity_flag_id")
	require.NotContains(t, data, "attempt_connection_id")
	kicked, err = NewExamAttemptKickedEvent(sittingID, attemptID, candidateID, connectionID, suspensionID, 2, at)
	require.NoError(t, err)
	data = nil
	require.NoError(t, json.Unmarshal(kicked.Data, &data))
	require.Equal(t, connectionID.String(), data["attempt_connection_id"])
	require.NotContains(t, data, "integrity_flag_id")

	for _, test := range []struct {
		name   string
		source model.AttemptSuspensionSource
		reason model.AttemptSuspensionCandidateReason
		valid  bool
	}{
		{"policy continuity", model.AttemptSuspensionSourcePolicy, model.AttemptSuspensionCandidateReasonSecureContinuityLost, true},
		{"policy focus", model.AttemptSuspensionSourcePolicy, model.AttemptSuspensionCandidateReasonFocusLossPolicy, true},
		{"manual kick", model.AttemptSuspensionSourceManual, model.AttemptSuspensionCandidateReasonManagerSuspended, true},
		{"policy claiming a kick", model.AttemptSuspensionSourcePolicy, model.AttemptSuspensionCandidateReasonManagerSuspended, false},
		{"kick claiming policy", model.AttemptSuspensionSourceManual, model.AttemptSuspensionCandidateReasonSecureContinuityLost, false},
		{"unknown source", "", model.AttemptSuspensionCandidateReasonManagerSuspended, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewCandidateExamAttemptSuspendedEvent(sittingID, attemptID, candidateID, test.source, test.reason, at)
			require.Equal(t, test.valid, err == nil, "error = %v", err)
		})
	}
}
