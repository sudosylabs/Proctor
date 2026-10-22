// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00" + document))
	require.NotNil(t, got, "prepared idempotency")
	require.Equal(t, userID, got.UserID, "prepared idempotency")
	require.Equal(t, operation, got.Operation, "prepared idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "prepared idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "prepared idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "prepared idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "prepared idempotency")
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	submissionID, reviewID := model.NewSubmissionID(), model.NewSubmissionReviewID()
	flagID := model.NewIntegrityFlagID()

	decision, err := prepareDecisionIdempotency(invocation, SaveDecisionCommand{SubmissionID: submissionID, ReviewID: reviewID,
		FlagID: flagID, ExpectedReviewRevision: 2, ExpectedDecisionRevision: 1, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "verified", IdempotencyKey: "decision-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, decision, userID, store.ExamIntegrityReviewDecisionOperation, "decision-key",
		fmt.Sprintf(`{"submission_id":%q,"submission_review_id":%q,"integrity_flag_id":%q,"expected_review_revision":2,"expected_decision_revision":1,"outcome":"confirmed","private_rationale":"verified"}`,
			submissionID, reviewID, flagID))

	draft, err := prepareDraftIdempotency(invocation, UpdateDraftCommand{SubmissionID: submissionID, ReviewID: reviewID,
		ExpectedReviewRevision: 2, ManagerNotes: "private", StudentRemarksMarkdown: "Visible", IdempotencyKey: "draft-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, draft, userID, store.ExamIntegrityReviewDraftOperation, "draft-key",
		fmt.Sprintf(`{"submission_id":%q,"submission_review_id":%q,"expected_review_revision":2,"manager_notes":"private","student_remarks_markdown":"Visible"}`,
			submissionID, reviewID))

	for _, operation := range []string{store.ExamIntegrityReviewFinalizeOperation, store.ExamIntegrityReviewReleaseOperation} {
		terminal, prepareErr := prepareTerminalIdempotency(invocation, operation, "terminal-key", submissionID, reviewID, 3)
		require.NoError(t, prepareErr)
		assertPreparedIdempotency(t, terminal, userID, operation, "terminal-key",
			fmt.Sprintf(`{"submission_id":%q,"submission_review_id":%q,"expected_review_revision":3}`, submissionID, reviewID))
	}
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		store.ExamIntegrityReviewDecisionOperation: "exam.integrity_review.decision.v1",
		store.ExamIntegrityReviewDraftOperation:    "exam.integrity_review.draft.v1",
		store.ExamIntegrityReviewFinalizeOperation: "exam.integrity_review.finalize.v1",
		store.ExamIntegrityReviewReleaseOperation:  "exam.integrity_review.release.v1",
	}
	require.Len(t, tests, 4)
	for operation, want := range tests {
		assert.Equal(t, want, operation, "operation")
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := applicationidempotency.Require(invocation, "operation", "", struct{}{})
	fault, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "idempotency.key_required", fault.Code())
}
