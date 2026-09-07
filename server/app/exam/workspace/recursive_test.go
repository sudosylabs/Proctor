// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRecursiveRemovalBindsFlagToDraftCommand(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	command := RemoveEntryCommand{ExamID: f.examID, EntryID: model.NewStarterWorkspaceEntryID(), ExpectedDraftRevision: 1, IdempotencyKey: "remove"}
	if _, err := f.service.RemoveEntry(context.Background(), f.call, command); err != nil {
		t.Fatal(err)
	}
	ordinary := *f.persistence.idempotency
	command.Recursive = true
	if _, err := f.service.RemoveEntry(context.Background(), f.call, command); err != nil {
		t.Fatal(err)
	}
	if !f.persistence.mutation.Recursive || f.persistence.mutation.ExpectedDraftRevision != 1 ||
		ordinary.KeyDigest != f.persistence.idempotency.KeyDigest || ordinary.Fingerprint == f.persistence.idempotency.Fingerprint {
		t.Fatal("recursive removal lost its Draft precondition or semantic identity")
	}
}
