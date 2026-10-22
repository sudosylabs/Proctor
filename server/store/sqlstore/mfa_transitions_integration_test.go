//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMFAEnrollmentRechecksDeadlinesAfterSessionRowLock(t *testing.T) {
	for _, name := range []string{"primary proof", "Session", "access credential"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := mfaRecoveryFixture(t, ctx)
			s := f.persistence
			audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.NowUTC())
			input := &store.MFAPendingEnrollment{Principal: mfaSQLPrincipal(f.targetSession, f.targetCredential),
				Credential: &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending,
					EncryptedSecret: "pending-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef"},
				Lifetime: time.Minute, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID}
			controller, err := s.GetMaster().Begin(ctx)
			require.NoError(t, err)
			defer controller.Rollback()
			var id string
			err = controller.Get(ctx, &id, `SELECT id FROM sessions WHERE id=? FOR UPDATE`, f.targetSession.ID.String())
			require.NoError(t, err)
			var blockerPID int
			err = controller.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`)
			require.NoError(t, err)
			finished := make(chan error, 1)
			go func() {
				_, callErr := s.MFA().SavePending(ctx, input)
				finished <- callErr
			}()
			pid := waitForBlockedMailQuery(t, ctx, s, blockerPID, "FROM sessions")
			// The query began after the helper's initial clock read. Place each
			// deadline at that instant, now known to have passed, without sleeps.
			var deadline time.Time
			err = controller.Get(ctx, &deadline, `SELECT query_start FROM pg_stat_activity WHERE pid=?`, pid)
			require.NoError(t, err)
			switch name {
			case "primary proof":
				_, err = controller.Exec(ctx, `UPDATE sessions SET authenticated_at=? WHERE id=?`, deadline.Add(-time.Hour), id)
			case "Session":
				_, err = controller.Exec(ctx, `UPDATE sessions SET idle_expires_at=? WHERE id=?`, deadline, id)
			case "access credential":
				_, err = controller.Exec(ctx, `UPDATE session_credentials SET expires_at=? WHERE id=?`, deadline, f.targetCredential.ID.String())
			}
			require.NoError(t, err)
			err = controller.Commit()
			require.NoError(t, err)
			select {
			case err = <-finished:
			case <-ctx.Done():
				require.FailNow(t, fmt.Sprint(ctx.Err()))
			}
			var conflict *store.ErrConflict
			require.ErrorAs(t, err, &conflict, "MFA enrollment did not reject expired %s after waiting for the Session row", name)
			require.Equal(t, "session", conflict.Resource, "MFA enrollment did not reject expired %s after waiting for the Session row", name)
			var count int
			err = s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM mfa_credentials WHERE user_id=?`, f.target.ID.String())
			require.NoError(t, err, "rejected enrollment persisted a factor: count")
			require.Equal(t, 0, count, "rejected enrollment persisted a factor: count")
			storedAudit, err := s.Audit().Get(ctx, audit.ID)
			require.NoError(t, err, "rejected enrollment completed its audit: %#v", storedAudit)
			require.Equal(t, model.AuditStatusAttempt, storedAudit.Status, "rejected enrollment completed its audit: %#v", storedAudit)
		})
	}
}
