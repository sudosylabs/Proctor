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

	"github.com/stretchr/testify/require"
)

func manualSuspensionAggregate(t *testing.T, at time.Time) (*ExamAttempt, *AttemptParticipation, *AttemptConnection) {
	t.Helper()
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), at)
	require.NoError(t, err)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), at)
	require.NoError(t, err)
	return attempt, participation, connection
}

func TestSuspendExamAttemptByManagerEndsLiveContinuityAsKicked(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0).UTC()
	kickedAt := at.Add(time.Minute)

	attempt, participation, connection := manualSuspensionAggregate(t, at)
	require.NoError(t, SuspendExamAttemptByManager(attempt, participation, connection, kickedAt))
	require.Equal(t, ExamAttemptSuspended, attempt.State, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, int64(2), attempt.Revision, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEnded, participation.State, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEndKicked, participation.EndReason, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionClosed, connection.State, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionCloseKicked, connection.CloseReason, "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.True(t, connection.ClosedAt.Time.Equal(kickedAt), "kicked aggregate = %#v / %#v / %#v", attempt, participation, connection)

	// A reconnecting candidate has an active generation but no open Connection.
	attempt, participation, _ = manualSuspensionAggregate(t, at)
	err := SuspendExamAttemptByManager(attempt, participation, nil, kickedAt)
	require.NoError(t, err, "reconnecting kick = %v / %#v", err, participation)
	require.Equal(t, AttemptParticipationEndKicked, participation.EndReason, "reconnecting kick = %v / %#v", err, participation)

	attempt, participation, connection = manualSuspensionAggregate(t, at)
	require.Error(t, SuspendExamAttemptByManager(attempt, participation, connection, at.Add(-time.Second)))
	require.Equal(t, ExamAttemptActive, attempt.State)
	require.Equal(t, AttemptParticipationActive, participation.State)
	require.Equal(t, AttemptConnectionOpen, connection.State)
	require.NoError(t, attempt.Suspend(kickedAt))
	require.Error(t, SuspendExamAttemptByManager(attempt, participation, connection, kickedAt), "already suspended Attempt was kicked again")
}

func TestManualAttemptSuspensionProvenance(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0).UTC()
	actor := NewUserID()
	suspension, err := NewManualAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(), 1,
		actor, "candidate left the room", at)
	require.NoError(t, err)
	require.Equal(t, AttemptSuspensionSourceManual, suspension.Source)
	require.Equal(t, AttemptSuspensionCandidateReasonManagerSuspended, suspension.CandidateReason)
	require.True(t, suspension.FlagID.IsZero())
	require.Equal(t, actor, suspension.SuspendedByUserID)
	err = suspension.Reallow(NewUserID(), "returned to the room", at.Add(time.Minute))
	require.NoError(t, err)
	for _, reason := range []string{"", " padded", strings.Repeat("a", AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		_, err = NewManualAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(), 1,
			actor, reason, at)
		require.Error(t, err, "manual suspension accepted private reason %q", reason)
	}
	invalid := *suspension
	invalid.FlagID = NewIntegrityFlagID()
	require.Error(t, invalid.Validate(), "manual suspension linked an Integrity Flag")
	policy, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(),
		NewIntegrityFlagID(), 1, AttemptSuspensionCandidateReasonManagerSuspended, at)
	require.Error(t, err, "policy suspension accepted the manual candidate reason")
	require.Nil(t, policy, "policy suspension accepted the manual candidate reason")
}
