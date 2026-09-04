// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestIntegrityReviewEventsExposeOnlySafeLifecycleFacts(t *testing.T) {
	t.Parallel()
	submissionID, attemptID := model.NewSubmissionID(), model.NewExamAttemptID()
	candidateID, reviewID := model.NewUserID(), model.NewSubmissionReviewID()
	at := time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC)

	fact := ExamIntegrityReviewEventFact{SubmissionID: submissionID, AttemptID: attemptID, CandidateID: candidateID,
		ReviewID: reviewID, State: model.SubmissionReviewDraft, Revision: 2,
		ReleaseState: model.SubmissionReviewWithheld, ChangedAt: at}
	changed, err := NewExamIntegrityReviewChangedEvent(fact)
	if err != nil {
		t.Fatal(err)
	}
	fact.State, fact.Revision = model.SubmissionReviewFinalized, 3
	finalized, err := NewExamIntegrityReviewFinalizedEvent(fact)
	if err != nil {
		t.Fatal(err)
	}
	fact.Revision, fact.ReleaseState = 4, model.SubmissionReviewReleased
	released, err := NewExamStudentResultReleasedEvent(fact)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewCandidateStudentResultReleasedEvent(fact)
	if err != nil {
		t.Fatal(err)
	}

	if changed.Name != "exam_integrity_review_changed" || finalized.Name != "exam_integrity_review_finalized" ||
		released.Name != "exam_student_result_released" || candidate.Name != "exam_student_result_released" ||
		changed.Action != model.ActionSubmissionView || changed.Resource != (model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}) ||
		candidate.UserID != candidateID.String() || candidate.Action != "" || candidate.Resource != (model.Resource{}) {
		t.Fatalf("review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	}
	for _, event := range []RealtimeEvent{changed, finalized, released, candidate} {
		if err = event.ValidateForPublish(); err != nil {
			t.Fatalf("ValidateForPublish(%s): %v", event.Name, err)
		}
		payload := string(event.Data)
		for _, forbidden := range []string{"manager_notes", "student_remarks", "rationale", "evidence", "outcome"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("%s exposed %q in %s", event.Name, forbidden, payload)
			}
		}
	}
}

func TestLateIntegrityDiscrepancyEventIsOnlyARefetchHint(t *testing.T) {
	t.Parallel()
	submissionID, sittingID := model.NewSubmissionID(), model.NewExamSittingID()
	attemptID, candidateID := model.NewExamAttemptID(), model.NewUserID()
	discrepancyID := model.NewIntegrityDiscrepancyID()
	at := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	event, err := NewExamIntegrityDiscrepancyRecordedEvent(submissionID, sittingID, attemptID, candidateID,
		discrepancyID, at)
	if err != nil || event.Name != "exam_integrity_discrepancy_recorded" || event.Action != model.ActionSubmissionView ||
		event.Resource != (model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}) {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	payload := string(event.Data)
	for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "evidence", "outcome"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("discrepancy event exposed %q: %s", forbidden, payload)
		}
	}
}
