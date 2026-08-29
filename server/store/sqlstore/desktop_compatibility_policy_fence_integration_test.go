//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestDesktopAuthorizationExchangeSerializesWithCompatibilityPolicyReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "desktop-policy-fence", DisplayName: "Desktop Policy Fence",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "desktop-policy-fence", Email: "desktop-policy-fence@example.edu",
	})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "policy-fence")
	var exchanged *store.DesktopAuthorizationExchangeResult
	err = serializeCompatibilityPolicyReplacement(t, ctx, persistence, "sessions", func() error {
		var exchangeErr error
		exchanged, exchangeErr = persistence.BrowserAuthentication().Exchange(
			ctx,
			desktopAuthorizationExchangeForSQLTest(code, state, verifier, audit),
		)
		return exchangeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if exchanged == nil || exchanged.Session == nil || exchanged.Session.UserID != user.ID {
		t.Fatalf("serialized Exchange() = %#v", exchanged)
	}
	policy, err := persistence.DesktopCompatibilityPolicy().Get(ctx)
	if err != nil || policy.Revision != 2 {
		t.Fatalf("policy after serialized Exchange = %#v, %v", policy, err)
	}
}

func TestExamAttemptAdmissionSerializesWithCompatibilityPolicyReplacement(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamAttemptCompatibilityPolicySerialization(
		t,
		persistence,
		func(t *testing.T, ctx context.Context, operation func() error) error {
			return serializeCompatibilityPolicyReplacement(t, ctx, persistence, "exam_attempts", operation)
		},
	)
}

func serializeCompatibilityPolicyReplacement(
	t *testing.T,
	ctx context.Context,
	persistence *SQLStore,
	table string,
	operation func() error,
) error {
	t.Helper()
	if operation == nil {
		return fmt.Errorf("serialized compatibility operation is required")
	}
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		return err
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		return err
	}
	const pauseKey int64 = 8154700260821
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey); err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()

	var triggerSQL, blockedQuery string
	switch table {
	case "sessions":
		blockedQuery = "INSERT INTO sessions"
		triggerSQL = `CREATE TRIGGER proctor_test_pause_compatibility_policy
			BEFORE INSERT ON sessions FOR EACH ROW
			EXECUTE FUNCTION proctor_test_pause_compatibility_policy()`
	case "exam_attempts":
		blockedQuery = "INSERT INTO exam_attempts"
		triggerSQL = `CREATE TRIGGER proctor_test_pause_compatibility_policy
			BEFORE INSERT ON exam_attempts FOR EACH ROW
			EXECUTE FUNCTION proctor_test_pause_compatibility_policy()`
	default:
		return fmt.Errorf("unsupported compatibility fence table %q", table)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_compatibility_policy() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260821);
			RETURN NEW;
		END $$`); err != nil {
		return err
	}
	if _, err = persistence.GetMaster().Exec(ctx, triggerSQL); err != nil {
		return err
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(),
			`DROP TRIGGER IF EXISTS proctor_test_pause_compatibility_policy ON `+table+`;
			 DROP FUNCTION IF EXISTS proctor_test_pause_compatibility_policy()`)
	}()

	operationResult := make(chan error, 1)
	go func() { operationResult <- operation() }()
	operationPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, blockedQuery)
	policyResult := make(chan error, 1)
	go func() {
		_, updateErr := persistence.GetMaster().Exec(ctx, `UPDATE desktop_compatibility_policies
			SET revision=revision+1, updated_at=statement_timestamp() WHERE singleton=1`)
		policyResult <- updateErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, operationPID, "UPDATE desktop_compatibility_policies")
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey); err != nil {
		return err
	}
	locked = false
	if err = <-operationResult; err != nil {
		return err
	}
	return <-policyResult
}
