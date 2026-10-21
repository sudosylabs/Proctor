// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExamSittingRecordsCompletionLifecycle(t *testing.T) {
	at := TimeUTC(time.Now())
	c := NewExamSittingRecordsCompletion(NewExamSittingID())
	actor := NewUserID()
	require.False(t, c.IsCurrent())
	require.NoError(t, c.Validate())
	changed, err := c.Complete(1, 0, actor, at)
	require.NoError(t, err, "first completion = %#v", c)
	require.True(t, changed, "first completion = %#v", c)
	require.True(t, c.IsCurrent(), "first completion = %#v", c)
	require.Equal(t, int64(2), c.Revision, "first completion = %#v", c)
	require.Equal(t, at, c.CompletedAt.Time, "first completion = %#v", c)
	original := *c
	changed, err = c.Complete(2, 0, actor, at.Add(time.Hour))
	require.NoError(t, err, "current completion restarted its retention clock: %#v", c)
	require.False(t, changed, "current completion restarted its retention clock: %#v", c)
	require.Equal(t, original, *c, "current completion restarted its retention clock: %#v", c)
	err = c.ObserveIntegrity(at.Add(2 * time.Hour))
	require.NoError(t, err, "new accepted integrity did not stale completion: %#v", c)
	require.False(t, c.IsCurrent(), "new accepted integrity did not stale completion: %#v", c)
	require.Equal(t, int64(1), c.EvidenceRevision, "new accepted integrity did not stale completion: %#v", c)
	require.Equal(t, int64(3), c.Revision, "new accepted integrity did not stale completion: %#v", c)
	staleAt := c.StaleAt
	err = c.ObserveIntegrity(at.Add(3 * time.Hour))
	require.NoError(t, err, "second integrity change lost original staleness: %#v", c)
	require.Equal(t, staleAt, c.StaleAt, "second integrity change lost original staleness: %#v", c)
	require.Equal(t, int64(2), c.EvidenceRevision, "second integrity change lost original staleness: %#v", c)
	before := *c
	_, err = c.Complete(c.Revision, 1, actor, at.Add(4*time.Hour))
	require.Error(t, err, "completion accepted an unacknowledged integrity revision")
	require.Equal(t, before, *c, "completion accepted an unacknowledged integrity revision")
	changed, err = c.Complete(c.Revision, 2, actor, at.Add(4*time.Hour))
	require.NoError(t, err, "renewed completion did not restart retention: %#v", c)
	require.True(t, changed, "renewed completion did not restart retention: %#v", c)
	require.True(t, c.IsCurrent(), "renewed completion did not restart retention: %#v", c)
	require.Equal(t, at.Add(4*time.Hour), c.CompletedAt.Time, "renewed completion did not restart retention: %#v", c)
	require.Equal(t, int64(2), c.CompletedEvidenceRevision, "renewed completion did not restart retention: %#v", c)
}

func TestExamSittingRecordsCompletionInvalidTransitionsDoNotMutate(t *testing.T) {
	at := TimeUTC(time.Now())
	actor := NewUserID()
	for _, tc := range []struct {
		name   string
		setup  func(*ExamSittingRecordsCompletion)
		mutate func(*ExamSittingRecordsCompletion) error
	}{
		{"wrong revision", nil, func(c *ExamSittingRecordsCompletion) error { _, err := c.Complete(2, 0, actor, at); return err }},
		{"invalid actor", nil, func(c *ExamSittingRecordsCompletion) error { _, err := c.Complete(1, 0, "", at); return err }},
		{"completion overflow", func(c *ExamSittingRecordsCompletion) { c.Revision = math.MaxInt64 }, func(c *ExamSittingRecordsCompletion) error {
			_, err := c.Complete(c.Revision, 0, actor, at)
			return err
		}},
		{"integrity overflow", func(c *ExamSittingRecordsCompletion) { c.EvidenceRevision = math.MaxInt64 }, func(c *ExamSittingRecordsCompletion) error { return c.ObserveIntegrity(at) }},
		{"time regression", func(c *ExamSittingRecordsCompletion) { _, _ = c.Complete(1, 0, actor, at) }, func(c *ExamSittingRecordsCompletion) error { return c.ObserveIntegrity(at.Add(-time.Second)) }},
		{"zero time", nil, func(c *ExamSittingRecordsCompletion) error { return c.ObserveIntegrity(time.Time{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewExamSittingRecordsCompletion(NewExamSittingID())
			if tc.setup != nil {
				tc.setup(c)
			}
			before := *c
			require.Error(t, tc.mutate(c), "invalid transition changed state: %#v", c)
			require.Equal(t, before, *c, "invalid transition changed state: %#v", c)
		})
	}
	c := NewExamSittingRecordsCompletion(NewExamSittingID())
	_, _ = c.Complete(1, 0, actor, at)
	c.EvidenceRevision++
	require.Error(t, c.Validate(), "unacknowledged evidence accepted without stale marker")
	require.False(t, c.IsCurrent(), "unacknowledged evidence accepted without stale marker")
}

func TestRetentionHoldScopeAndExplicitRelease(t *testing.T) {
	examID, sittingID, submissionID := NewExamID(), NewExamSittingID(), NewSubmissionID()
	for _, scope := range []RetentionHoldScope{{ExamID: examID}, {ExamID: examID, SittingID: sittingID}, {ExamID: examID, SittingID: sittingID, SubmissionID: submissionID}} {
		require.NoError(t, scope.Validate(), "valid scope rejected: %#v", scope)
	}
	for _, scope := range []RetentionHoldScope{{}, {ExamID: examID, SubmissionID: submissionID}, {ExamID: examID, SittingID: "invalid"}} {
		require.Error(t, scope.Validate())
	}
	at := TimeUTC(time.Now())
	hold := &RetentionHold{ID: NewRetentionHoldID(), Scope: RetentionHoldScope{ExamID: examID, SittingID: sittingID, SubmissionID: submissionID},
		Revision: 1, CreatedAt: at, CreatedByUserID: NewUserID(), ReasonCode: "integrity_review", PrivateReason: "An institution case requires preservation."}
	require.NoError(t, hold.Validate())
	require.Equal(t, ResourceSubmission, hold.Scope.Resource().Type)
	require.False(t, hold.ReleasedAt.Valid)
	original := *hold
	require.Error(t, hold.Release(2, NewUserID(), "case_closed", "Release approved.", at.Add(time.Hour)), "hold release ignored revision guard")
	require.Equal(t, original, *hold, "hold release ignored revision guard")
	actor := NewUserID()
	require.NoError(t, hold.Release(1, actor, "case_closed", "Release approved.", at.Add(time.Hour)), "manual release = %#v", hold)
	require.NoError(t, hold.Validate(), "manual release = %#v", hold)
	require.Equal(t, int64(2), hold.Revision, "manual release = %#v", hold)
	require.Equal(t, actor, hold.ReleasedByUserID, "manual release = %#v", hold)
	require.True(t, hold.ReleasedAt.Valid, "manual release = %#v", hold)
	released := *hold
	require.Error(t, hold.Release(2, actor, "case_closed", "Release approved.", at.Add(2*time.Hour)), "released hold accepted a second transition")
	require.Equal(t, released, *hold, "released hold accepted a second transition")
}

func TestRecordsReasonBounds(t *testing.T) {
	require.NoError(t, ValidateRecordsReason("institution_request", strings.Repeat("文", 1000)))
	for _, tc := range []struct{ code, private string }{
		{"", "reason"}, {"email@example.test", "reason"}, {strings.Repeat("x", 65), "reason"},
		{"other", ""}, {"other", " reason"}, {"other", "reason\n"}, {"other", strings.Repeat("x", 1001)}, {"other", string([]byte{0xff})},
	} {
		t.Run(tc.code, func(t *testing.T) {
			require.Error(t, ValidateRecordsReason(tc.code, tc.private), "unbounded or unsafe records reason accepted: %q", tc.code)
		})
	}
}
