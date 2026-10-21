// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestExpiryProjectionHelpersRejectEachInconsistency runs every named check
// projectExpiry combines against a consistent outcome and against one
// mutation that only that check rejects.
func TestExpiryProjectionHelpersRejectEachInconsistency(t *testing.T) {
	t.Parallel()
	type check func(*store.ExamAttemptParticipationExpiryResult, store.ExamAttemptParticipationExpiryDue) bool
	checks := map[string]check{
		"complete": func(r *store.ExamAttemptParticipationExpiryResult, _ store.ExamAttemptParticipationExpiryDue) bool {
			return completeExpiryOutcome(r)
		},
		"scope": expiryScopeMatches,
		"attempt": func(r *store.ExamAttemptParticipationExpiryResult, d store.ExamAttemptParticipationExpiryDue) bool {
			return expiredAttemptMatches(r.Attempt, d)
		},
		"participation": func(r *store.ExamAttemptParticipationExpiryResult, d store.ExamAttemptParticipationExpiryDue) bool {
			return expiredParticipationMatches(r.Participation, d)
		},
		"connection": func(r *store.ExamAttemptParticipationExpiryResult, _ store.ExamAttemptParticipationExpiryDue) bool {
			return expiryConnectionClosed(r.Connection, r.ConnectionClosed)
		},
		"records": func(r *store.ExamAttemptParticipationExpiryResult, d store.ExamAttemptParticipationExpiryDue) bool {
			return connectionLossRecordsMatch(r.Evidence, r.Flag, d)
		},
		"suspension": func(r *store.ExamAttemptParticipationExpiryResult, d store.ExamAttemptParticipationExpiryDue) bool {
			return connectionLossSuspensionMatches(r.Suspension, r.Flag, d)
		},
		"decided": func(r *store.ExamAttemptParticipationExpiryResult, d store.ExamAttemptParticipationExpiryDue) bool {
			return expiryDecidedAfterLease(r.DatabaseTime, d)
		},
	}
	f := newFixture(t)
	due := expiryDueFixture(f)
	for name, accept := range checks {
		require.True(t, accept(expiryResultFixture(t, f, due, false), due), "%s rejected a consistent outcome", name)
	}
	for _, test := range []struct {
		check  string
		mutate func(*store.ExamAttemptParticipationExpiryResult)
	}{
		{"complete", func(r *store.ExamAttemptParticipationExpiryResult) { r.Evidence = nil }},
		{"scope", func(r *store.ExamAttemptParticipationExpiryResult) { r.ClassID = model.NewClassID() }},
		{"attempt", func(r *store.ExamAttemptParticipationExpiryResult) { r.Attempt.State = model.ExamAttemptActive }},
		{"participation", func(r *store.ExamAttemptParticipationExpiryResult) {
			r.Participation.EndReason = model.AttemptParticipationEndKicked
		}},
		{"connection", func(r *store.ExamAttemptParticipationExpiryResult) { r.ConnectionClosed = false }},
		{"records", func(r *store.ExamAttemptParticipationExpiryResult) { r.Evidence.FlagID = model.NewIntegrityFlagID() }},
		{"suspension", func(r *store.ExamAttemptParticipationExpiryResult) {
			r.Suspension.Source = model.AttemptSuspensionSourceManual
		}},
		{"decided", func(r *store.ExamAttemptParticipationExpiryResult) {
			r.DatabaseTime = due.LeaseExpiresAt.Add(-time.Millisecond)
		}},
	} {
		t.Run(test.check, func(t *testing.T) {
			t.Parallel()
			result := expiryResultFixture(t, f, due, false)
			test.mutate(result)
			require.False(t, checks[test.check](result, due), "%s accepted the inconsistency", test.check)
			_, err := projectExpiry(result, due)
			require.Error(t, err, "projectExpiry accepted the inconsistency")
		})
	}
}
