// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package correction

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
	examID, sittingID, revisionID := model.NewExamID(), model.NewExamSittingID(), model.NewExamRevisionID()
	resourceID, stageID := model.NewExamResourceID(), model.NewExamCorrectionResourceStageID()

	staged, err := prepareStageIdempotency(call, StageResourceContentCommand{ExamID: examID, SittingID: sittingID,
		BaseRevisionID: revisionID, Target: store.ExamCorrectionResourceReplacement, ResourceID: resourceID,
		MediaType: model.ExamResourceMediaPDF, Size: 12, ExpectedSHA256: "digest", IdempotencyKey: "stage-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, staged, userID, store.ExamCorrectionResourceStageOperation, "stage-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"base_revision_id":%q,"target":"replacement","resource_id":%q,"media_type":"application/pdf","size":12,"sha256":"digest"}`,
			examID, sittingID, revisionID, resourceID))

	applied, err := prepareApplyIdempotency(call, ApplyCommand{ExamID: examID, SittingID: sittingID,
		ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: revisionID,
		Instructions:     OptionalInstructions{Present: true, Markdown: "Updated"},
		Resources:        []ResourceManifestItem{{ResourceID: resourceID, DisplayName: "Reference", DescriptionMarkdown: "Read it", StageID: stageID}},
		CandidateSummary: "Instructions and reference were corrected.", AcknowledgementRequired: true,
		PrivateReason: "ambiguity", IdempotencyKey: "apply-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, applied, userID, idempotencyOperationApplyCorrection, "apply-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_sitting_revision":3,"expected_current_revision_id":%q,"instructions_present":true,"instructions_markdown":"Updated","browser_policy_present":false,"browser_policy":"","resources":[{"resource_id":%q,"display_name":"Reference","description_markdown":"Read it","stage_id":%q}],"candidate_summary":"Instructions and reference were corrected.","acknowledgement_required":true,"private_reason":"ambiguity"}`,
			examID, sittingID, revisionID, resourceID, stageID))
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	if store.ExamCorrectionResourceStageOperation != "exam.sitting.correction.resource.stage.v1" {
		t.Errorf("stage operation = %q", store.ExamCorrectionResourceStageOperation)
	}
	if idempotencyOperationApplyCorrection != "exam.sitting.correction.apply.v1" {
		t.Errorf("apply operation = %q", idempotencyOperationApplyCorrection)
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
