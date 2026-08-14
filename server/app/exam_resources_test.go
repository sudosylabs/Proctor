// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceMetadataIdempotencyIncludesFieldPresence(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID, resourceID := model.NewExamID(), model.NewExamResourceID()
	empty := ""
	nameOnly, err := newExamResourceMetadataIdempotency(invocation, EditExamResourceMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DisplayName: &empty, IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	descriptionOnly, err := newExamResourceMetadataIdempotency(invocation, EditExamResourceMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: 1, DescriptionMarkdown: &empty, IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	if nameOnly.Fingerprint == descriptionOnly.Fingerprint {
		t.Fatal("idempotency fingerprint erased metadata field presence")
	}
}
