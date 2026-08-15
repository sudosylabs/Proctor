// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateSittingEvent(t, corrected, "exam_sitting_content_corrected", sittingID)
	for _, forbidden := range []string{"credential", "hash", "instructions", "resources", "private_reason"} {
		if strings.Contains(string(corrected.Data), forbidden) {
			t.Fatalf("correction payload contains %q: %s", forbidden, corrected.Data)
		}
	}

	lifecycle, err := NewCandidateExamSittingLifecycleChangedEvent(
		examID, sittingID, model.ExamSittingPaused, 8, "manager_paused", at.Add(time.Hour), at,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateSittingEvent(t, lifecycle, "exam_sitting_lifecycle_changed", sittingID)
	if strings.Contains(string(lifecycle.Data), "private") {
		t.Fatalf("lifecycle payload exposes a private reason: %s", lifecycle.Data)
	}
}

func TestManagerConnectionEventsAreContentAndCredentialFree(t *testing.T) {
	t.Parallel()
	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	candidateID, connectionID := model.NewUserID(), model.NewAttemptConnectionID()
	at := time.Date(2026, time.August, 17, 9, 5, 0, 123, time.UTC)

	opened, err := NewExamAttemptConnectionOpenedEvent(sittingID, attemptID, candidateID, connectionID, at)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := NewExamAttemptConnectionClosedEvent(sittingID, attemptID, candidateID, connectionID,
		model.AttemptConnectionCloseTransport, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []RealtimeEvent{opened, closed} {
		if event.Action != model.ActionExamSittingView ||
			event.Resource != (model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}) {
			t.Fatalf("manager event = %#v", event)
		}
		for _, forbidden := range []string{"credential", "hash", "participation_id", "generation", "session_id", "private"} {
			if strings.Contains(string(event.Data), forbidden) {
				t.Fatalf("event payload contains %q: %s", forbidden, event.Data)
			}
		}
		if err := event.ValidateForPublish(); err != nil {
			t.Fatalf("manager event is not publishable: %v", err)
		}
	}
}

func TestExamAttemptSuspensionAndReallowEventsSeparateCandidateAndManagerPayloads(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	connectionID, flagID, suspensionID := model.NewAttemptConnectionID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	privateReason := "manager verified secure continuity"
	at := time.Date(2026, time.August, 18, 9, 5, 0, 0, time.UTC)
	candidateSuspended, err := NewCandidateExamAttemptSuspendedEvent(sittingID, attemptID, candidateID,
		model.AttemptSuspensionCandidateReasonSecureContinuityLost, at)
	if err != nil {
		t.Fatal(err)
	}
	managerSuspended, err := NewExamAttemptSuspendedEvent(sittingID, attemptID, candidateID, connectionID, flagID, suspensionID, 2, at)
	if err != nil {
		t.Fatal(err)
	}
	candidateReallowed, err := NewCandidateExamAttemptReallowedEvent(sittingID, attemptID, candidateID, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	managerReallowed, err := NewExamAttemptReallowedEvent(sittingID, attemptID, candidateID, suspensionID, 3, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []RealtimeEvent{candidateSuspended, candidateReallowed} {
		if event.UserID != candidateID.String() || event.Action != model.ActionExamSittingParticipate {
			t.Fatalf("candidate event = %#v", event)
		}
		for _, forbidden := range []string{"integrity_flag_id", "suspension_id", "attempt_connection_id", "generation", "private_reason", "credential", "hash"} {
			if strings.Contains(string(event.Data), forbidden) {
				t.Fatalf("candidate payload contains %q: %s", forbidden, event.Data)
			}
		}
		if strings.Contains(string(event.Data), privateReason) {
			t.Fatalf("candidate payload contains private manager reason: %s", event.Data)
		}
	}
	var suspendedData map[string]any
	if err = json.Unmarshal(candidateSuspended.Data, &suspendedData); err != nil {
		t.Fatal(err)
	}
	if got := suspendedData["reason_code"]; got != string(model.AttemptSuspensionCandidateReasonSecureContinuityLost) {
		t.Fatalf("candidate suspension reason = %#v", got)
	}
	for key := range suspendedData {
		switch key {
		case "exam_sitting_id", "exam_attempt_id", "state", "reason_code", "changed_at":
		default:
			t.Fatalf("candidate suspension payload contains unsafe field %q: %s", key, candidateSuspended.Data)
		}
	}
	for _, event := range []RealtimeEvent{managerSuspended, managerReallowed} {
		if event.UserID != "" || event.Action != model.ActionExamSittingView {
			t.Fatalf("manager event = %#v", event)
		}
		for _, forbidden := range []string{"private_reason", "credential", "hash", "session_id", "evidence"} {
			if strings.Contains(string(event.Data), forbidden) {
				t.Fatalf("manager payload contains %q: %s", forbidden, event.Data)
			}
		}
		if strings.Contains(string(event.Data), privateReason) {
			t.Fatalf("manager payload contains private manager reason: %s", event.Data)
		}
		if err := event.ValidateForPublish(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCandidateExamAttemptSuspendedEventAcceptsFocusPolicyReviewReason(t *testing.T) {
	t.Parallel()
	event, err := NewCandidateExamAttemptSuspendedEvent(model.NewExamSittingID(), model.NewExamAttemptID(),
		model.NewUserID(), model.AttemptSuspensionCandidateReasonFocusLossPolicy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err = json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["reason_code"] != string(model.AttemptSuspensionCandidateReasonFocusLossPolicy) {
		t.Fatalf("reason_code=%#v data=%s", data["reason_code"], event.Data)
	}
}

func TestCandidateExamAttemptWorkspaceChangedEventIsOnlyASafeRefetchHint(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	entryID := model.NewAttemptWorkspaceEntryID()
	at := time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC)
	event, err := NewCandidateExamAttemptWorkspaceChangedEvent(sittingID, attemptID, candidateID, entryID,
		model.AttemptWorkspaceMutationReplaceFile, 17, at)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "exam_attempt_workspace_changed" || event.UserID != candidateID.String() ||
		event.Action != model.ActionExamSittingParticipate || event.Resource.ID != sittingID.String() {
		t.Fatalf("event=%#v", event)
	}
	data := string(event.Data)
	for _, forbidden := range []string{"path", "object", "content_version", "sha256", "credential", "session", "generation"} {
		if strings.Contains(strings.ToLower(data), forbidden) {
			t.Fatalf("workspace hint contains %q: %s", forbidden, data)
		}
	}
}

func TestExamAttemptSubmittedEventsSeparateManagerAndCandidateTargetsWithSafeReceiptData(t *testing.T) {
	t.Parallel()

	sittingID, attemptID := model.NewExamSittingID(), model.NewExamAttemptID()
	candidateID, submissionID := model.NewUserID(), model.NewSubmissionID()
	digest := strings.Repeat("d", 64)
	submittedAt := time.Date(2026, time.August, 21, 10, 15, 0, 123, time.UTC)
	manager, err := NewExamAttemptSubmittedEvent(sittingID, attemptID, candidateID, submissionID, 9, digest, submittedAt)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewCandidateExamAttemptSubmittedEvent(sittingID, attemptID, candidateID, submissionID, 9, digest, submittedAt)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Name != "exam_attempt_submitted" || manager.UserID != "" || manager.Action != model.ActionExamSittingView ||
		manager.Resource != (model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}) {
		t.Fatalf("manager event=%#v", manager)
	}
	if candidate.Name != "exam_attempt_submitted" || candidate.UserID != candidateID.String() ||
		candidate.Action != model.ActionExamSittingParticipate || candidate.Resource.ID != sittingID.String() {
		t.Fatalf("candidate event=%#v", candidate)
	}
	var managerData, candidateData map[string]any
	if err = json.Unmarshal(manager.Data, &managerData); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(candidate.Data, &candidateData); err != nil {
		t.Fatal(err)
	}
	if len(managerData) != 8 || managerData["candidate_user_id"] != candidateID.String() || len(candidateData) != 7 {
		t.Fatalf("manager=%v candidate=%v", managerData, candidateData)
	}
	for _, event := range []RealtimeEvent{manager, candidate} {
		for _, forbidden := range []string{"path", "content", "source", "evidence", "gap", "sequence", "credential", "session", "private", "remark"} {
			if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
				t.Fatalf("submitted event contains %q: %s", forbidden, event.Data)
			}
		}
		if err = event.ValidateForPublish(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFocusLossEventsSeparateNeutralCandidateWarningFromBoundedManagerFlag(t *testing.T) {
	t.Parallel()
	sittingID, attemptID, candidateID := model.NewExamSittingID(), model.NewExamAttemptID(), model.NewUserID()
	flagID := model.NewIntegrityFlagID()
	at := time.Date(2026, time.August, 20, 9, 5, 0, 0, time.UTC)
	warning, err := NewCandidateExamAttemptFocusLossWarningEvent(sittingID, attemptID, candidateID, at)
	if err != nil {
		t.Fatal(err)
	}
	flagged, err := NewExamAttemptIntegrityFlaggedEvent(sittingID, attemptID, candidateID, flagID,
		model.IntegrityOutcomeFlagAndWarn, 100, 17, at)
	if err != nil {
		t.Fatal(err)
	}
	if warning.Name != "exam_attempt_focus_warning" || warning.UserID != candidateID.String() ||
		warning.Action != model.ActionExamSittingParticipate || warning.Resource.ID != sittingID.String() {
		t.Fatalf("warning=%#v", warning)
	}
	var warningData map[string]any
	if err = json.Unmarshal(warning.Data, &warningData); err != nil ||
		warningData["warning_code"] != "focus_loss_policy_warning" || len(warningData) != 4 {
		t.Fatalf("warning data=%v error=%v", warningData, err)
	}
	if flagged.Name != "exam_attempt_integrity_flagged" || flagged.UserID != "" ||
		flagged.Action != model.ActionExamSittingView || flagged.Resource.ID != sittingID.String() {
		t.Fatalf("flagged=%#v", flagged)
	}
	var managerData map[string]any
	if err = json.Unmarshal(flagged.Data, &managerData); err != nil || managerData["policy_kind"] != "focus_loss" ||
		managerData["outcome"] != "flag_and_warn" || managerData["retained_evidence_count"] != float64(100) ||
		managerData["evidence_overflow_count"] != float64(17) || managerData["evidence_available"] != true || len(managerData) != 10 {
		t.Fatalf("manager data=%v error=%v", managerData, err)
	}
	for _, event := range []RealtimeEvent{warning, flagged} {
		for _, forbidden := range []string{"duration", "source", "sequence", "threshold", "credential", "hash", "session", "private", "severity", "guilt"} {
			if strings.Contains(strings.ToLower(string(event.Data)), forbidden) {
				t.Fatalf("Focus Loss event contains %q: %s", forbidden, event.Data)
			}
		}
		if err = event.ValidateForPublish(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCandidateSittingEvent(t *testing.T, event RealtimeEvent, name string, sittingID model.ExamSittingID) {
	t.Helper()
	if event.Name != name || event.Action != model.ActionExamSittingParticipate ||
		event.Resource != (model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}) {
		t.Fatalf("candidate event = %#v", event)
	}
	if err := event.ValidateForPublish(); err != nil {
		t.Fatalf("candidate event is not publishable: %v", err)
	}
}
