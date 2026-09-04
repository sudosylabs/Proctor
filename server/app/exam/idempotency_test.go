// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertStoreIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	if got == nil || got.UserID != userID || got.Operation != operation || got.KeyDigest != wantKey ||
		got.FingerprintVersion != 1 || got.Fingerprint != wantFingerprint || got.OutcomeVersion != 1 ||
		got.Retention != 24*time.Hour || got.Wait != 2*time.Second {
		t.Fatalf("Store idempotency = %#v; want user=%s operation=%q key_digest=%x fingerprint_version=1 fingerprint=%x outcome_version=1 retention=%s wait=%s",
			got, userID, operation, wantKey, wantFingerprint, 24*time.Hour, 2*time.Second)
	}
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"create":                      idempotencyOperationCreate,
		"edit Draft text":             idempotencyOperationEditDraftText,
		"configure Draft Focus Loss":  idempotencyOperationConfigureDraftFocusLoss,
		"configure Execution Profile": idempotencyOperationConfigureExecutionProfile,
		"archive":                     idempotencyOperationArchive,
		"add manager":                 idempotencyOperationAddManager,
		"remove manager":              idempotencyOperationRemoveManager,
		"transfer owner":              idempotencyOperationTransferOwner,
		"publish Revision":            idempotencyOperationPublishRevision,
	}
	want := map[string]string{
		"create":                      "exam.create.v1",
		"edit Draft text":             "exam.draft.text.edit.v1",
		"configure Draft Focus Loss":  "exam.draft.focus_loss.configure.v1",
		"configure Execution Profile": "exam.draft.execution_profile.configure.v1",
		"archive":                     "exam.archive.v1",
		"add manager":                 "exam.manager.add.v1",
		"remove manager":              "exam.manager.remove.v1",
		"transfer owner":              "exam.owner.transfer.v1",
		"publish Revision":            "exam.revision.publish.v1",
	}
	for name, operation := range tests {
		if operation != want[name] {
			t.Errorf("%s operation = %q; want %q", name, operation, want[name])
		}
	}
}
