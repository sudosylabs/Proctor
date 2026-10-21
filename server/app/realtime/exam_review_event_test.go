// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	fact.State, fact.Revision = model.SubmissionReviewFinalized, 3
	finalized, err := NewExamIntegrityReviewFinalizedEvent(fact)
	require.NoError(t, err)
	fact.Revision, fact.ReleaseState = 4, model.SubmissionReviewReleased
	released, err := NewExamStudentResultReleasedEvent(fact)
	require.NoError(t, err)
	candidate, err := NewCandidateStudentResultReleasedEvent(fact)
	require.NoError(t, err)

	require.Equal(t, "exam_integrity_review_changed", changed.Name, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, "exam_integrity_review_finalized", finalized.Name, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, "exam_student_result_released", released.Name, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, "exam_student_result_released", candidate.Name, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, model.ActionSubmissionView, changed.Action, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, changed.Resource, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, candidateID.String(), candidate.UserID, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Empty(t, candidate.Action, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	require.Equal(t, model.Resource{}, candidate.Resource, "review events = changed %#v finalized %#v released %#v candidate %#v", changed, finalized, released, candidate)
	for _, event := range []RealtimeEvent{changed, finalized, released, candidate} {
		err = event.ValidateForPublish()
		require.NoError(t, err, "ValidateForPublish(%s)", event.Name)
		payload := string(event.Data)
		for _, forbidden := range []string{"manager_notes", "student_remarks", "rationale", "evidence", "outcome"} {
			require.NotContains(t, payload, forbidden, "%s exposed %q in", event.Name, forbidden)
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
	require.NoError(t, err, "event=%#v error", event)
	require.Equal(t, "exam_integrity_discrepancy_recorded", event.Name, "event=%#v error", event)
	require.Equal(t, model.ActionSubmissionView, event.Action, "event=%#v error", event)
	require.Equal(t, model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, event.Resource, "event=%#v error", event)
	payload := string(event.Data)
	for _, forbidden := range []string{"duration", "source", "sequence", "credential", "session", "evidence", "outcome"} {
		require.NotContains(t, payload, forbidden, "discrepancy event exposed")
	}
}
