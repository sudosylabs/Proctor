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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertStoreIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	require.NotNil(t, got, "Store idempotency")
	require.Equal(t, userID, got.UserID, "Store idempotency")
	require.Equal(t, operation, got.Operation, "Store idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "Store idempotency")
	require.Equal(t, 1, got.FingerprintVersion, "Store idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "Store idempotency")
	require.Equal(t, 1, got.OutcomeVersion, "Store idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "Store idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "Store idempotency")
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"create":                     idempotencyOperationCreate,
		"edit Draft text":            idempotencyOperationEditDraftText,
		"configure Draft Focus Loss": idempotencyOperationConfigureDraftFocusLoss,
		"archive":                    idempotencyOperationArchive,
		"add manager":                idempotencyOperationAddManager,
		"remove manager":             idempotencyOperationRemoveManager,
		"transfer owner":             idempotencyOperationTransferOwner,
		"publish Revision":           idempotencyOperationPublishRevision,
	}
	want := map[string]string{
		"create":                     "exam.create.v1",
		"edit Draft text":            "exam.draft.text.edit.v1",
		"configure Draft Focus Loss": "exam.draft.focus_loss.configure.v1",
		"archive":                    "exam.archive.v1",
		"add manager":                "exam.manager.add.v1",
		"remove manager":             "exam.manager.remove.v1",
		"transfer owner":             "exam.owner.transfer.v1",
		"publish Revision":           "exam.revision.publish.v1",
	}
	for name, operation := range tests {
		assert.Equal(t, want[name], operation, "%s operation", name)
	}
}
