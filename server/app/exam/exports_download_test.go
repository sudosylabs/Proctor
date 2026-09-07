// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamExportDownloadRechecksAuthorityAfterStorageOpen(t *testing.T) {
	for _, scenario := range []string{"unchanged archive", "node clock ahead", "database expiry", "revoked credential", "withdrawn read permission", "replaced artifact"} {
		t.Run(scenario, func(t *testing.T) {
			f, service, base, auth, content := newExportFixture(t)
			base.value.State, base.value.ArchiveSizeBytes, base.value.ArchiveSHA256 = model.ExamExportReady, 42, strings.Repeat("a", 64)
			base.value.ReadyAt = model.OptionalTimeFrom(base.value.CreatedAt)
			databaseNow := base.value.ExpiresAt.Add(-time.Second)
			base.readAt = func() time.Time { return databaseNow }
			// PostgreSQL decides expiry even when the serving node's wall clock
			// lags behind it. No response body has reached the caller during Open.
			service.now = func() time.Time { return databaseNow.Add(-10 * time.Second) }
			if scenario == "node clock ahead" {
				service.now = func() time.Time { return databaseNow.Add(10 * time.Second) }
			}
			body := &exportClosingBody{}
			content.body = body
			content.opened = func() {
				switch scenario {
				case "database expiry":
					databaseNow = base.value.ExpiresAt
				case "revoked credential":
					base.getErr = store.NewErrConflict("authorization", "credential", nil)
				case "withdrawn read permission":
					auth.deny = model.ActionSubmissionViewOverride
				case "replaced artifact":
					base.artifactID = model.NewJobAttemptID()
				}
			}
			download, err := service.Open(context.Background(), f.call, ExportQuery{Scope: base.value.Scope, ExportID: base.value.ID})
			if scenario == "unchanged archive" || scenario == "node clock ahead" {
				if err != nil || download == nil || download.Body != body || body.closed {
					t.Fatalf("current archive was not available: %#v, %v, closed=%v", download, err, body.closed)
				}
				if err = download.Body.Close(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || download != nil || !body.closed {
				t.Fatalf("pending download escaped %s: %#v, %v, closed=%v", scenario, download, err, body.closed)
			}
			if scenario == "database expiry" {
				var fault *Fault
				if !errors.As(err, &fault) || fault.Code != "exam.export.expired" {
					t.Fatalf("expiry returned the wrong failure: %v", err)
				}
			}
		})
	}
}
