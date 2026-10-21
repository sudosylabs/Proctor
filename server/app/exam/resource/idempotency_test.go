// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package resource

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

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
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	require.NotNil(t, got, "prepared idempotency")
	require.Equal(t, userID, got.UserID, "prepared idempotency")
	require.Equal(t, operation, got.Operation, "prepared idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "prepared idempotency")
	require.Equal(t, 1, got.FingerprintVersion, "prepared idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "prepared idempotency")
	require.Equal(t, 1, got.OutcomeVersion, "prepared idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "prepared idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "prepared idempotency")
}

func TestResourceIdempotencyOperationAndDocumentCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, resourceID := model.NewExamID(), model.NewExamResourceID()
	ids := []string{resourceID.String()}
	tests := []struct {
		name        string
		operation   string
		resource    string
		nameValue   string
		description string
		media       model.ExamResourceMediaType
		size        int64
		sha         string
		ids         []string
		document    string
	}{
		{name: "add", operation: idempotencyOperationAddResource, nameValue: "Reference", description: "Read it",
			media: model.ExamResourceMediaText, size: 4, sha: "digest",
			document: fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"display_name":"Reference","description_markdown":"Read it","media_type":"text/plain","size":4,"sha256":"digest"}`, examID)},
		{name: "replace", operation: idempotencyOperationReplaceResourceContent, resource: resourceID.String(),
			media: model.ExamResourceMediaPDF, size: 5, sha: "digest",
			document: fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"resource_id":%q,"media_type":"application/pdf","size":5,"sha256":"digest"}`, examID, resourceID)},
		{name: "reorder", operation: idempotencyOperationReorderResources, ids: ids,
			document: fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"resource_ids":[%q]}`, examID, resourceID)},
		{name: "remove", operation: idempotencyOperationRemoveResource, resource: resourceID.String(),
			document: fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":2,"resource_id":%q}`, examID, resourceID)},
	}
	wantOperations := map[string]string{
		"add": "exam.resource.add.v1", "replace": "exam.resource.content.replace.v1",
		"reorder": "exam.resource.reorder.v1", "remove": "exam.resource.remove.v1",
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := prepareResourceIdempotency(invocation, test.operation, "key", examID, 2, test.resource,
				test.nameValue, test.description, test.media, test.size, test.sha, test.ids)
			require.NoError(t, err, "%s", test.name)
			assertPreparedIdempotency(t, prepared, userID, wantOperations[test.name], "key", test.document)
		})
	}
	require.Equal(t, "exam.resource.metadata.edit.v1", idempotencyOperationEditResourceMetadata)
}

func TestMetadataIdempotencyPreservesFieldPresence(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, resourceID := model.NewExamID(), model.NewExamResourceID()
	empty := ""
	nameOnly, err := prepareMetadataIdempotency(invocation, EditMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DisplayName: &empty, IdempotencyKey: "same-key"})
	require.NoError(t, err)
	descriptionOnly, err := prepareMetadataIdempotency(invocation, EditMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DescriptionMarkdown: &empty, IdempotencyKey: "same-key"})
	require.NoError(t, err)
	require.NotEqual(t, descriptionOnly.Fingerprint, nameOnly.Fingerprint, "metadata field presence was erased")
	assertPreparedIdempotency(t, nameOnly, userID, idempotencyOperationEditResourceMetadata, "same-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"resource_id":%q,"display_name":"","description_markdown":null}`, examID, resourceID))
	assertPreparedIdempotency(t, descriptionOnly, userID, idempotencyOperationEditResourceMetadata, "same-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"resource_id":%q,"display_name":null,"description_markdown":""}`, examID, resourceID))
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := applicationidempotency.Require(invocation, "operation", "", struct{}{})
	fault, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "idempotency.key_required", fault.Code())
}
