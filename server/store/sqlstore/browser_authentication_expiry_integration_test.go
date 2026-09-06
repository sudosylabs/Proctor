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
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopAuthorizationRejectsSourceCredentialExpiringDuringUserLockWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "desktop-expiry-lock", DisplayName: "Desktop Expiry Lock",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "desktop-expiry-lock", Email: "desktop-expiry-lock@example.edu",
	})
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	session.AuthenticatedAt = model.NowUTC().Add(-time.Minute)
	source, savedCredentials, err := persistence.Session().Save(ctx,
		sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10))
	if err != nil {
		t.Fatal(err)
	}
	var access *model.SessionCredential
	for _, credential := range savedCredentials {
		if credential.Kind == model.SessionCredentialAccess {
			access = credential
		}
	}
	if access == nil {
		t.Fatal("source Web Session has no access credential")
	}
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	created, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	if err != nil {
		t.Fatal(err)
	}

	// A profile or email transition can hold this row without taking the
	// per-User Session lock. Authentication resolves its live source first,
	// then waits here while the database clock passes the credential's deadline.
	controller, err := persistence.GetMaster().DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controller.Rollback() }()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	var expiresAt time.Time
	if err = persistence.GetMaster().Get(ctx, &expiresAt, `UPDATE session_credentials
		SET expires_at=clock_timestamp()+interval '2 seconds' WHERE id=? RETURNING expires_at`, access.ID.String()); err != nil {
		t.Fatal(err)
	}

	type authenticationOutcome struct {
		result *store.DesktopAuthorizationAuthenticationResult
		err    error
	}
	completed := make(chan authenticationOutcome, 1)
	go func() {
		result, authenticationErr := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
			&store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(binding), UserID: user.ID,
				SourceSessionID: source.ID, SourceCredentialID: access.ID,
				AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: source.AuthenticatedAt.UnixMilli(),
				Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			})
		completed <- authenticationOutcome{result: result, err: authenticationErr}
	}()
	waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "FROM users WHERE id")

	// Observe PostgreSQL time instead of assuming a fixed sleep crossed expiry.
	// No credential mutation occurs after authentication reads its source proof.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var expired bool
		if err = persistence.GetMaster().Get(ctx, &expired, `SELECT clock_timestamp()>=?`, expiresAt); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("source credential did not reach its database deadline: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	if err = controller.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-completed:
		if outcome.result != nil || !store.IsNotFound(outcome.err) {
			t.Fatalf("expired source credential authenticated a handoff after waiting: result=%#v, error=%v", outcome.result, outcome.err)
		}
	case <-ctx.Done():
		t.Fatalf("authentication did not finish after releasing the User row: %v", ctx.Err())
	}
	var row browserAuthenticationRow
	if err = persistence.GetMaster().Get(ctx, &row, `SELECT `+browserAuthenticationColumns+`
		FROM browser_authentication_transactions WHERE id=?`, created.ID.String()); err != nil {
		t.Fatal(err)
	}
	unchanged, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.State != model.BrowserAuthenticationStateBound || !unchanged.UserID.IsZero() ||
		unchanged.AuthenticatedAt.Valid || unchanged.MFACompletedAt.Valid || !unchanged.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatal("failed source authentication changed the bound transaction or its deadline")
	}
}
