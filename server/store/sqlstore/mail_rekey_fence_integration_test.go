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
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// TestMailPrimaryKeyFenceSerializesEnqueueBeforeRekey proves the lock order,
// rather than merely relying on a scheduler delay. A test-only trigger pauses
// EnqueueTest after it has acquired the shared mail-key fence. PostgreSQL's
// blocking graph must then show StartRekey waiting on that exact transaction.
// Once released, the old-key insertion commits before promotion and the
// retirement proof necessarily observes its reference.
func TestMailPrimaryKeyFenceSerializesEnqueueBeforeRekey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "mail-fence", DisplayName: "Mail Fence"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "mail-fence", Email: "mail-fence@example.edu"})

	const advisoryKey int64 = 8154700260818
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_mail_enqueue() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260818);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_mail_enqueue BEFORE INSERT ON mail_occurrences
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_mail_enqueue()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_mail_enqueue ON mail_occurrences; DROP FUNCTION IF EXISTS proctor_test_pause_mail_enqueue()`)
	}()

	enqueueInput := storetest.MailTestEnqueueFixtureForSQLTest(t, user, institution, model.NowUTC())
	enqueueResult := make(chan error, 1)
	go func() {
		_, enqueueErr := persistence.Mail().EnqueueTest(ctx, enqueueInput)
		enqueueResult <- enqueueErr
	}()
	enqueuePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "INSERT INTO mail_occurrences")

	rekeyInput := clusteredMailRekeyStart(t, ctx, persistence, user, institution,
		"22222222222222222222222222222222", "11111111111111111111111111111111", model.NowUTC())
	rekeyResult := make(chan error, 1)
	go func() {
		_, rekeyErr := persistence.Mail().StartRekey(ctx, rekeyInput)
		rekeyResult <- rekeyErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, enqueuePID, "mail_key_state")

	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err = <-enqueueResult; err != nil {
		t.Fatalf("EnqueueTest() error = %v", err)
	}
	if err = <-rekeyResult; err != nil {
		t.Fatalf("StartRekey() error = %v", err)
	}
	proof, err := persistence.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: rekeyInput.Job.ID,
		PrimaryKeyID: rekeyInput.PrimaryKeyID, RetiringKeyID: rekeyInput.RetiringKeyID})
	if err != nil {
		t.Fatal(err)
	}
	if proof.NonPrimaryReferences != 1 || proof.RetiringReferences != 1 || proof.RetirementSafe {
		t.Fatalf("post-fence proof = %#v", proof)
	}
}

func waitForBlockedMailQuery(t *testing.T, ctx context.Context, persistence *SQLStore, blockerPID int, queryFragment string) int {
	t.Helper()
	query := `SELECT pid FROM pg_stat_activity
		WHERE pid <> pg_backend_pid() AND $1 = ANY(pg_blocking_pids(pid)) AND query LIKE '%' || $2 || '%'
		ORDER BY pid LIMIT 1`
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var pid int
		err := persistence.GetMaster().DB().QueryRowContext(ctx, query, blockerPID, queryFragment).Scan(&pid)
		if err == nil {
			return pid
		}
		if err != sql.ErrNoRows {
			t.Fatalf("inspect PostgreSQL blocking graph: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(fmt.Errorf("wait for %q blocked by pid %d: %w", queryFragment, blockerPID, ctx.Err()))
		case <-ticker.C:
		}
	}
}
