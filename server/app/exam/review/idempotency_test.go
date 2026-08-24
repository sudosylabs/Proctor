// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertStoreBoundaryCommand(t *testing.T, got, want *store.CommandIdempotency) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Store idempotency = %#v; want %#v", got, want)
	}
}

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	if got == nil || got.UserID != userID || got.Operation != operation || got.KeyDigest != wantKey ||
		got.FingerprintVersion != 1 || got.Fingerprint != wantFingerprint || got.OutcomeVersion != 1 ||
		got.Retention != 24*time.Hour || got.Wait != 2*time.Second {
		t.Fatalf("prepared idempotency = %#v; want user=%s operation=%q key=%x fingerprint=%x versions=1/1 retention=24h wait=2s",
			got, userID, operation, wantKey, wantFingerprint)
	}
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
	submissionID, reviewID := model.NewSubmissionID(), model.NewSubmissionReviewID()
	flagID := model.NewIntegrityFlagID()

	decision, err := prepareDecisionIdempotency(call, SaveDecisionCommand{SubmissionID: submissionID, ReviewID: reviewID,
		FlagID: flagID, ExpectedReviewRevision: 2, ExpectedDecisionRevision: 1, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "verified", IdempotencyKey: "decision-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, decision, userID, store.ExamIntegrityReviewDecisionOperation, "decision-key",
		fmt.Sprintf(`{"submission_id":%q,"submission_review_id":%q,"integrity_flag_id":%q,"expected_review_revision":2,"expected_decision_revision":1,"outcome":"confirmed","private_rationale":"verified"}`,
			submissionID, reviewID, flagID))

	draft, err := prepareDraftIdempotency(call, UpdateDraftCommand{SubmissionID: submissionID, ReviewID: reviewID,
		ExpectedReviewRevision: 2, ManagerNotes: "private", StudentRemarksMarkdown: "Visible", IdempotencyKey: "draft-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, draft, userID, store.ExamIntegrityReviewDraftOperation, "draft-key",
		fmt.Sprintf(`{"submission_id":%q,"submission_review_id":%q,"expected_review_revision":2,"manager_notes":"private","student_remarks_markdown":"Visible"}`,
			submissionID, reviewID))

	for _, operation := range []string{store.ExamIntegrityReviewFinalizeOperation, store.ExamIntegrityReviewReleaseOperation} {
		terminal, prepareErr := prepareTerminalIdempotency(call, operation, "terminal-key", submissionID, reviewID, 3)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
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
	if len(tests) != 4 {
		t.Fatalf("operation set collapsed: %#v", tests)
	}
	for operation, want := range tests {
		if operation != want {
			t.Errorf("operation = %q; want %q", operation, want)
		}
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := prepareIdempotency(call, "operation", "", struct{}{})
	if fault, ok := err.(*Fault); !ok || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v", err)
	}
}
