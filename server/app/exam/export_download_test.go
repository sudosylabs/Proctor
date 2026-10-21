// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
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
			service.deps.Now = func() time.Time { return databaseNow.Add(-10 * time.Second) }
			if scenario == "node clock ahead" {
				service.deps.Now = func() time.Time { return databaseNow.Add(10 * time.Second) }
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
			download, err := service.OpenExamExport(context.Background(), f.invocation, ExportQuery{Scope: base.value.Scope, ExportID: base.value.ID})
			if scenario == "unchanged archive" || scenario == "node clock ahead" {
				require.NoError(t, err, "current archive was not available")
				require.NotNil(t, download, "current archive was not available")
				require.Equal(t, download.Body, body, "current archive was not available")
				require.False(t, body.closed, "current archive was not available")
				err = download.Body.Close()
				require.NoError(t, err)
				return
			}
			require.Error(t, err, "pending download escaped %s", scenario)
			require.Nil(t, download, "pending download escaped %s", scenario)
			require.True(t, body.closed, "pending download escaped %s", scenario)
			if scenario == "database expiry" {
				var fault *apperror.Error
				require.ErrorAs(t, err, &fault)
				require.Equal(t, "exam.export.expired", fault.Code())
			}
		})
	}
}
