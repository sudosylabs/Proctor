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
)

func TestExamSittingRecordsCompletionLifecycle(t *testing.T) {
	at := TimeUTC(time.Now())
	c := NewExamSittingRecordsCompletion(NewExamSittingID())
	actor := NewUserID()
	if c.IsCurrent() || c.Validate() != nil {
		t.Fatalf("new completion = %#v", c)
	}
	changed, err := c.Complete(1, 0, actor, at)
	if err != nil || !changed || !c.IsCurrent() || c.Revision != 2 || c.CompletedAt.Time != at {
		t.Fatalf("first completion = %#v, %v, %v", c, changed, err)
	}
	original := *c
	changed, err = c.Complete(2, 0, actor, at.Add(time.Hour))
	if err != nil || changed || *c != original {
		t.Fatalf("current completion restarted its retention clock: %#v, %v", c, err)
	}
	if err = c.ObserveIntegrity(at.Add(2 * time.Hour)); err != nil || c.IsCurrent() || c.EvidenceRevision != 1 || c.Revision != 3 {
		t.Fatalf("new accepted integrity did not stale completion: %#v, %v", c, err)
	}
	staleAt := c.StaleAt
	if err = c.ObserveIntegrity(at.Add(3 * time.Hour)); err != nil || c.StaleAt != staleAt || c.EvidenceRevision != 2 {
		t.Fatalf("second integrity change lost original staleness: %#v, %v", c, err)
	}
	before := *c
	if _, err = c.Complete(c.Revision, 1, actor, at.Add(4*time.Hour)); err == nil || *c != before {
		t.Fatal("completion accepted an unacknowledged integrity revision")
	}
	changed, err = c.Complete(c.Revision, 2, actor, at.Add(4*time.Hour))
	if err != nil || !changed || !c.IsCurrent() || c.CompletedAt.Time != at.Add(4*time.Hour) || c.CompletedEvidenceRevision != 2 {
		t.Fatalf("renewed completion did not restart retention: %#v, %v", c, err)
	}
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
			if err := tc.mutate(c); err == nil || *c != before {
				t.Fatalf("invalid transition changed state: %#v, %v", c, err)
			}
		})
	}
	c := NewExamSittingRecordsCompletion(NewExamSittingID())
	_, _ = c.Complete(1, 0, actor, at)
	c.EvidenceRevision++
	if c.Validate() == nil || c.IsCurrent() {
		t.Fatal("unacknowledged evidence accepted without stale marker")
	}
}

func TestRetentionHoldScopeAndExplicitRelease(t *testing.T) {
	examID, sittingID, submissionID := NewExamID(), NewExamSittingID(), NewSubmissionID()
	for _, scope := range []RetentionHoldScope{{ExamID: examID}, {ExamID: examID, SittingID: sittingID}, {ExamID: examID, SittingID: sittingID, SubmissionID: submissionID}} {
		if err := scope.Validate(); err != nil {
			t.Fatalf("valid scope rejected: %#v, %v", scope, err)
		}
	}
	for _, scope := range []RetentionHoldScope{{}, {ExamID: examID, SubmissionID: submissionID}, {ExamID: examID, SittingID: "invalid"}} {
		if scope.Validate() == nil {
			t.Fatalf("invalid lineage accepted: %#v", scope)
		}
	}
	at := TimeUTC(time.Now())
	hold := &RetentionHold{ID: NewRetentionHoldID(), Scope: RetentionHoldScope{ExamID: examID, SittingID: sittingID, SubmissionID: submissionID},
		Revision: 1, CreatedAt: at, CreatedByUserID: NewUserID(), ReasonCode: "integrity_review", PrivateReason: "An institution case requires preservation."}
	if hold.Validate() != nil || hold.Scope.Resource().Type != ResourceSubmission || hold.ReleasedAt.Valid {
		t.Fatalf("initial hold = %#v", hold)
	}
	original := *hold
	if err := hold.Release(2, NewUserID(), "case_closed", "Release approved.", at.Add(time.Hour)); err == nil || *hold != original {
		t.Fatal("hold release ignored revision guard")
	}
	actor := NewUserID()
	if err := hold.Release(1, actor, "case_closed", "Release approved.", at.Add(time.Hour)); err != nil || hold.Validate() != nil ||
		hold.Revision != 2 || hold.ReleasedByUserID != actor || !hold.ReleasedAt.Valid {
		t.Fatalf("manual release = %#v, %v", hold, err)
	}
	released := *hold
	if err := hold.Release(2, actor, "case_closed", "Release approved.", at.Add(2*time.Hour)); err == nil || *hold != released {
		t.Fatal("released hold accepted a second transition")
	}
}

func TestRecordsReasonBounds(t *testing.T) {
	if err := ValidateRecordsReason("institution_request", strings.Repeat("文", 1000)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ code, private string }{
		{"", "reason"}, {"email@example.test", "reason"}, {strings.Repeat("x", 65), "reason"},
		{"other", ""}, {"other", " reason"}, {"other", "reason\n"}, {"other", strings.Repeat("x", 1001)}, {"other", string([]byte{0xff})},
	} {
		if ValidateRecordsReason(tc.code, tc.private) == nil {
			t.Fatalf("unbounded or unsafe records reason accepted: %q", tc.code)
		}
	}
}
