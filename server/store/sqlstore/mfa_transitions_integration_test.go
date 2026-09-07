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
	"errors"
	"testing"
	"time"

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
			audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
			input := &store.MFAPendingEnrollment{Principal: mfaSQLPrincipal(f.targetSession, f.targetCredential),
				Credential: &model.MFACredential{UserID: f.target.ID, State: model.MFAStatePending,
					EncryptedSecret: "pending-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef"},
				Lifetime: time.Minute, RecentAuthenticationTTL: time.Hour, AuditEventID: audit.ID}
			controller, err := s.GetMaster().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer controller.Rollback()
			var id string
			if err = controller.Get(ctx, &id, `SELECT id FROM sessions WHERE id=? FOR UPDATE`, f.targetSession.ID.String()); err != nil {
				t.Fatal(err)
			}
			var blockerPID int
			if err = controller.Get(ctx, &blockerPID, `SELECT pg_backend_pid()`); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, callErr := s.MFA().SavePendingWithAudit(ctx, input)
				finished <- callErr
			}()
			pid := waitForBlockedMailQuery(t, ctx, s, blockerPID, "FROM sessions")
			// The query began after the helper's initial clock read. Place each
			// deadline at that instant, now known to have passed, without sleeps.
			var deadline time.Time
			if err = controller.Get(ctx, &deadline, `SELECT query_start FROM pg_stat_activity WHERE pid=?`, pid); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "primary proof":
				_, err = controller.Exec(ctx, `UPDATE sessions SET authenticated_at=? WHERE id=?`, deadline.Add(-time.Hour), id)
			case "Session":
				_, err = controller.Exec(ctx, `UPDATE sessions SET idle_expires_at=? WHERE id=?`, deadline, id)
			case "access credential":
				_, err = controller.Exec(ctx, `UPDATE session_credentials SET expires_at=? WHERE id=?`, deadline, f.targetCredential.ID.String())
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = controller.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-finished:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var conflict *store.ErrConflict
			if !errors.As(err, &conflict) || conflict.Resource != "session" {
				t.Fatalf("MFA enrollment did not reject expired %s after waiting for the Session row: %v", name, err)
			}
			var count int
			if err = s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM mfa_credentials WHERE user_id=?`, f.target.ID.String()); err != nil || count != 0 {
				t.Fatalf("rejected enrollment persisted a factor: count=%d error=%v", count, err)
			}
			storedAudit, err := s.Audit().Get(ctx, audit.ID.String())
			if err != nil || storedAudit.Status != model.AuditStatusAttempt {
				t.Fatalf("rejected enrollment completed its audit: %#v %v", storedAudit, err)
			}
		})
	}
}
