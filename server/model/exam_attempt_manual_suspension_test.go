// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"
)

func manualSuspensionAggregate(t *testing.T, at time.Time) (*ExamAttempt, *AttemptParticipation, *AttemptConnection) {
	t.Helper()
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), at)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	return attempt, participation, connection
}

func TestSuspendExamAttemptByManagerEndsLiveContinuityAsKicked(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0).UTC()
	kickedAt := at.Add(time.Minute)

	attempt, participation, connection := manualSuspensionAggregate(t, at)
	if err := SuspendExamAttemptByManager(attempt, participation, connection, kickedAt); err != nil {
		t.Fatal(err)
	}
	if attempt.State != ExamAttemptSuspended || attempt.Revision != 2 || participation.State != AttemptParticipationEnded ||
		participation.EndReason != AttemptParticipationEndKicked || connection.State != AttemptConnectionClosed ||
		connection.CloseReason != AttemptConnectionCloseKicked || !connection.ClosedAt.Time.Equal(kickedAt) {
		t.Fatalf("kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	}

	// A reconnecting candidate has an active generation but no open Connection.
	attempt, participation, _ = manualSuspensionAggregate(t, at)
	if err := SuspendExamAttemptByManager(attempt, participation, nil, kickedAt); err != nil ||
		participation.EndReason != AttemptParticipationEndKicked {
		t.Fatalf("reconnecting kick = %v / %#v", err, participation)
	}

	attempt, participation, connection = manualSuspensionAggregate(t, at)
	if err := SuspendExamAttemptByManager(attempt, participation, connection, at.Add(-time.Second)); err == nil ||
		attempt.State != ExamAttemptActive || participation.State != AttemptParticipationActive || connection.State != AttemptConnectionOpen {
		t.Fatalf("kick before the live records mutated them: %v", err)
	}
	if err := attempt.Suspend(kickedAt); err != nil {
		t.Fatal(err)
	}
	if err := SuspendExamAttemptByManager(attempt, participation, connection, kickedAt); err == nil {
		t.Fatal("already suspended Attempt was kicked again")
	}
}

func TestManualAttemptSuspensionProvenance(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0).UTC()
	actor := NewUserID()
	suspension, err := NewManualAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(), 1,
		actor, "candidate left the room", at)
	if err != nil {
		t.Fatal(err)
	}
	if suspension.Source != AttemptSuspensionSourceManual || suspension.CandidateReason != AttemptSuspensionCandidateReasonManagerSuspended ||
		!suspension.FlagID.IsZero() || suspension.SuspendedByUserID != actor {
		t.Fatalf("manual suspension = %#v", suspension)
	}
	if err = suspension.Reallow(NewUserID(), "returned to the room", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"", " padded", strings.Repeat("a", AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		if _, err = NewManualAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(), 1,
			actor, reason, at); err == nil {
			t.Fatalf("manual suspension accepted private reason %q", reason)
		}
	}
	invalid := *suspension
	invalid.FlagID = NewIntegrityFlagID()
	if invalid.Validate() == nil {
		t.Fatal("manual suspension linked an Integrity Flag")
	}
	policy, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(),
		NewIntegrityFlagID(), 1, AttemptSuspensionCandidateReasonManagerSuspended, at)
	if err == nil || policy != nil {
		t.Fatal("policy suspension accepted the manual candidate reason")
	}
}
