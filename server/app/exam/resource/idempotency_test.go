// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package resource

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

func TestResourceIdempotencyOperationAndDocumentCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
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
		prepared, err := prepareResourceIdempotency(call, test.operation, "key", examID, 2, test.resource,
			test.nameValue, test.description, test.media, test.size, test.sha, test.ids)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		assertPreparedIdempotency(t, prepared, userID, wantOperations[test.name], "key", test.document)
	}
	if idempotencyOperationEditResourceMetadata != "exam.resource.metadata.edit.v1" {
		t.Fatalf("metadata operation = %q", idempotencyOperationEditResourceMetadata)
	}
}

func TestMetadataIdempotencyPreservesFieldPresence(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, resourceID := model.NewExamID(), model.NewExamResourceID()
	empty := ""
	nameOnly, err := prepareMetadataIdempotency(call, EditMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DisplayName: &empty, IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	descriptionOnly, err := prepareMetadataIdempotency(call, EditMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DescriptionMarkdown: &empty, IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	if nameOnly.Fingerprint == descriptionOnly.Fingerprint {
		t.Fatal("metadata field presence was erased")
	}
	assertPreparedIdempotency(t, nameOnly, userID, idempotencyOperationEditResourceMetadata, "same-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"resource_id":%q,"display_name":"","description_markdown":null}`, examID, resourceID))
	assertPreparedIdempotency(t, descriptionOnly, userID, idempotencyOperationEditResourceMetadata, "same-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"resource_id":%q,"display_name":null,"description_markdown":""}`, examID, resourceID))
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := prepareIdempotency(call, "operation", "", struct{}{})
	if fault, ok := err.(*Fault); !ok || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v", err)
	}
}
