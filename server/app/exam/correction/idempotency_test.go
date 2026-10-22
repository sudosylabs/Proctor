// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package correction

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
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
	examID, sittingID, revisionID := model.NewExamID(), model.NewExamSittingID(), model.NewExamRevisionID()
	resourceID, stageID := model.NewExamResourceID(), model.NewExamCorrectionResourceStageID()

	staged, err := prepareStageIdempotency(invocation, StageResourceContentCommand{ExamID: examID, SittingID: sittingID,
		BaseRevisionID: revisionID, Target: store.ExamCorrectionResourceReplacement, ResourceID: resourceID,
		MediaType: model.ExamResourceMediaPDF, Size: 12, ExpectedSHA256: "digest", IdempotencyKey: "stage-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, staged, userID, store.ExamCorrectionResourceStageOperation, "stage-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"base_revision_id":%q,"target":"replacement","resource_id":%q,"media_type":"application/pdf","size":12,"sha256":"digest"}`,
			examID, sittingID, revisionID, resourceID))

	applied, err := prepareApplyIdempotency(invocation, ApplyCommand{ExamID: examID, SittingID: sittingID,
		ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: revisionID,
		Instructions:         OptionalInstructions{Present: true, Markdown: "Updated"},
		Resources:            []ResourceManifestItem{{ResourceID: resourceID, DisplayName: "Reference", DescriptionMarkdown: "Read it", StageID: stageID}},
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "Instructions and reference were corrected.", AcknowledgementRequired: true,
		PrivateReason: "ambiguity", IdempotencyKey: "apply-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, applied, userID, idempotencyOperationApplyCorrection, "apply-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_sitting_revision":3,"expected_current_revision_id":%q,"instructions_present":true,"instructions_markdown":"Updated","browser_policy_present":false,"browser_policy":"","resources":[{"resource_id":%q,"display_name":"Reference","description_markdown":"Read it","stage_id":%q}],"candidate_summary":"Instructions and reference were corrected.","acknowledgement_required":true,"affected_capabilities":["browser","submission","workspace"],"private_reason":"ambiguity"}`,
			examID, sittingID, revisionID, resourceID, stageID))
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "exam.sitting.correction.resource.stage", store.ExamCorrectionResourceStageOperation)
	assert.Equal(t, "exam.sitting.correction.apply", idempotencyOperationApplyCorrection)
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := idempotency.Require(invocation, "operation", "", struct{}{})
	fault, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "idempotency.key_required", fault.Code())
}
