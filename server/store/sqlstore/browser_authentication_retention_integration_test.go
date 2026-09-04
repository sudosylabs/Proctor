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
	"crypto/elliptic"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestBrowserAuthenticationMaintenanceIsBoundedAndMultiNodeSafe(t *testing.T) {
	ctx := context.Background()
	first := openTestStore(t)
	resetTestStore(t, first)
	second := openTestStore(t)
	institution, err := first.Institution().Save(ctx, &model.Institution{Name: "desktop-maintenance", DisplayName: "Desktop Maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	const total = 24
	for index := 0; index < total; index++ {
		createdAt := model.NowUTC().Add(-10 * time.Minute)
		transaction, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(createdAt, institution.ID)
		if _, err = first.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
			t.Fatal(err)
		}
		if _, err = first.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
			SET created_at=?,updated_at=?,expires_at=? WHERE id=?`, createdAt, createdAt,
			createdAt.Add(transaction.Lifetime), transaction.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		result *store.BrowserAuthenticationMaintenanceResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, candidate := range []store.BrowserAuthenticationStore{first.BrowserAuthentication(), second.BrowserAuthentication()} {
		wait.Add(1)
		go func(maintenance store.BrowserAuthenticationStore) {
			defer wait.Done()
			result, maintainErr := maintenance.Maintain(ctx, 10)
			outcomes <- outcome{result: result, err: maintainErr}
		}(candidate)
	}
	wait.Wait()
	close(outcomes)
	expired := 0
	for result := range outcomes {
		if result.err != nil || result.result == nil {
			t.Fatalf("concurrent Maintain() = %#v, %v", result.result, result.err)
		}
		expired += result.result.Expired
	}
	if expired != 20 {
		t.Fatalf("concurrent expired rows = %d, want 20", expired)
	}
	last, err := first.BrowserAuthentication().Maintain(ctx, 10)
	if err != nil || last.Expired != 4 || last.More {
		t.Fatalf("final Maintain() = %#v, %v", last, err)
	}
	var activeProofs int
	if err = first.GetMaster().Get(ctx, &activeProofs, `SELECT COUNT(*) FROM browser_authentication_transactions
		WHERE state <> 'expired' OR handle_hash IS NOT NULL OR browser_proof_hash IS NOT NULL OR state_hash IS NOT NULL
		   OR callback_url IS NOT NULL OR code_challenge IS NOT NULL OR code_hash IS NOT NULL
		   OR proposed_public_jwk IS NOT NULL OR proposed_key_thumbprint IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if activeProofs != 0 {
		t.Fatalf("maintenance left %d rows live or proof-bearing", activeProofs)
	}
}

func TestDesktopAuthorizationExchangeSerializesWithConcurrentUserDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-disable-race", DisplayName: "Desktop Disable Race"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-disable-race", Email: "desktop-disable-race@example.edu"})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID)

	const pauseKey int64 = 8154700260819
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_desktop_session() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260819);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_desktop_session BEFORE INSERT ON sessions
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_desktop_session()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_desktop_session ON sessions; DROP FUNCTION IF EXISTS proctor_test_pause_desktop_session()`)
	}()

	type exchangeOutcome struct {
		result *store.DesktopAuthorizationExchangeResult
		err    error
	}
	exchangeResult := make(chan exchangeOutcome, 1)
	go func() {
		result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx,
			desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit))
		exchangeResult <- exchangeOutcome{result: result, err: exchangeErr}
	}()
	exchangePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "INSERT INTO sessions")

	type disableOutcome struct {
		result *store.UserDisabledStateResult
		err    error
	}
	disableResult := make(chan disableOutcome, 1)
	go func() {
		result, disableErr := persistence.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.GetMillis(),
			RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID.String(), AuditAt: model.GetMillis(),
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		}))
		disableResult <- disableOutcome{result: result, err: disableErr}
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, exchangePID, "pg_advisory_xact_lock")
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	exchanged := <-exchangeResult
	if exchanged.err != nil || exchanged.result == nil || exchanged.result.Session == nil {
		t.Fatalf("Exchange() = %#v, %v", exchanged.result, exchanged.err)
	}
	disabled := <-disableResult
	if disabled.err != nil || disabled.result == nil || disabled.result.User == nil || !disabled.result.User.DisabledAt.Valid {
		t.Fatalf("SetDisabledWithAudit() = %#v, %v", disabled.result, disabled.err)
	}
	storedSession, err := persistence.Session().Get(ctx, exchanged.result.Session.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !storedSession.RevokedAt.Valid || storedSession.RevocationReason != model.SessionRevocationAccountDisabled {
		t.Fatalf("session escaped concurrent disable: %#v", storedSession)
	}
}

func TestDesktopAuthorizationIssueCodeSerializesWithConcurrentUserDisableAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primary := openTestStore(t)
	resetTestStore(t, primary)
	secondary := openTestStore(t)
	institution, err := primary.Institution().Save(ctx, &model.Institution{Name: "desktop-issue-disable-race", DisplayName: "Desktop Issue Disable Race"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, primary, &model.User{Username: "desktop-issue-disable-race", Email: "desktop-issue-disable-race@example.edu"})
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = primary.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, primary, handle, proof, state, user.ID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, primary, institution.ID, user.ID, "issue-disable-race")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, primary, institution.ID, user.ID)

	controller, err := primary.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if _, err = controller.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user.ID.String()); err != nil {
		t.Fatal(err)
	}

	type disableOutcome struct {
		result *store.UserDisabledStateResult
		err    error
	}
	disableResult := make(chan disableOutcome, 1)
	go func() {
		result, disableErr := secondary.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.GetMillis(),
			RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID.String(), AuditAt: model.GetMillis(),
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		}))
		disableResult <- disableOutcome{result: result, err: disableErr}
	}()
	disablePID := waitForBlockedMailQuery(t, ctx, primary, controllerPID, "FROM users WHERE id")

	type issueOutcome struct {
		result *store.DesktopAuthorizationCodeIssued
		err    error
	}
	issueResult := make(chan issueOutcome, 1)
	go func() {
		result, issueErr := primary.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
			BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(model.NewCredentialToken()),
			ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
		})
		issueResult <- issueOutcome{result: result, err: issueErr}
	}()
	waitForSecondLockWaiter(t, ctx, primary, controllerPID, disablePID)
	if _, err = controller.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	locked = false
	disabled := <-disableResult
	if disabled.err != nil || disabled.result == nil || disabled.result.User == nil || !disabled.result.User.DisabledAt.Valid {
		t.Fatalf("SetDisabledWithAudit() = %#v, %v", disabled.result, disabled.err)
	}
	issued := <-issueResult
	if !store.IsNotFound(issued.err) || issued.result != nil {
		t.Fatalf("IssueCode() after concurrent disable = %#v, %v; want not found", issued.result, issued.err)
	}
	after, err := primary.Audit().Get(ctx, issueAudit.ID.String())
	if err != nil || after.Status != model.AuditStatusAttempt {
		t.Fatalf("IssueCode audit = %#v, %v", after, err)
	}
}

func TestDesktopAuthorizationRejectsArchivedUsersWithoutConsumingProofsOrCodes(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-archived-user", DisplayName: "Desktop Archived User"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-archived-user", Email: "desktop-archived-user@example.edu"})
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, persistence, handle, proof, state, user.ID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=clock_timestamp() WHERE id=?`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	code := model.NewCredentialToken()
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-issue")
	issue := &store.DesktopAuthorizationCodeIssue{BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code),
		ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis()}
	if result, issueErr := persistence.BrowserAuthentication().IssueCode(ctx, issue); result != nil || !store.IsNotFound(issueErr) {
		t.Fatalf("IssueCode(archived User) = %#v, %v; want not found", result, issueErr)
	}
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, issueAudit.ID)
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=NULL WHERE id=?`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	issueAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "issue-after-unarchive")
	issue.AuditEventID, issue.AuditAt = issueAudit.ID.String(), model.GetMillis()
	if _, err = persistence.BrowserAuthentication().IssueCode(ctx, issue); err != nil {
		t.Fatalf("IssueCode after unarchive did not retain pending proofs: %v", err)
	}

	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=clock_timestamp() WHERE id=?`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	if result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange); result != nil || !store.IsNotFound(exchangeErr) {
		t.Fatalf("Exchange(archived User) = %#v, %v; want not found", result, exchangeErr)
	}
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	if sessions, listErr := persistence.Session().ListByUser(ctx, user.ID.String()); listErr != nil || len(sessions) != 0 {
		t.Fatalf("archived exchange sessions = %#v, %v", sessions, listErr)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=NULL WHERE id=?`, user.ID.String()); err != nil {
		t.Fatal(err)
	}
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange-after-unarchive")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID.String(), model.GetMillis()
	if result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange); exchangeErr != nil || result == nil || result.Session == nil {
		t.Fatalf("Exchange after unarchive did not retain code = %#v, %v", result, exchangeErr)
	}
}

func TestDesktopAuthorizationRejectsMismatchedOrArchivedExternalIdentity(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	seedTestAuthenticationPolicy(t, persistence, map[string]model.ProviderAdmissionMode{"campus-cas": model.ProviderAdmissionLinkedOnly})
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-external-fence", DisplayName: "Desktop External Fence"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-external-user", Email: "desktop-external-user@example.edu"})
	other := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-external-other", Email: "desktop-external-other@example.edu"})
	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
		Subject: "desktop-external-user", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	if err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: other.ID, Provider: "campus-cas",
		Subject: "desktop-external-other", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	if err != nil {
		t.Fatal(err)
	}
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	if err != nil {
		t.Fatal(err)
	}
	code := model.NewCredentialToken()
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "mismatched-external-issue")
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}}
	if result, authenticationErr := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: user.ID,
			AuthenticationMethod: "oidc", AuthenticationProviderID: "campus-cas", ExternalIdentityID: otherIdentity.ID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(), Capabilities: capabilities}); result != nil || !errors.Is(authenticationErr, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("AuthenticateDesktopAuthorization(mismatched identity) = %#v, %v", result, authenticationErr)
	}
	if result, authenticationErr := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: user.ID,
			AuthenticationMethod: "oidc", AuthenticationProviderID: "campus-cas", ExternalIdentityID: identity.ID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(), Capabilities: capabilities}); authenticationErr != nil || result == nil || result.Denied {
		t.Fatalf("AuthenticateDesktopAuthorization(matched identity) = %#v, %v", result, authenticationErr)
	}
	issue := &store.DesktopAuthorizationCodeIssue{BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(code), ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
		AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis()}
	if _, err = persistence.BrowserAuthentication().IssueCode(ctx, issue); err != nil {
		t.Fatalf("IssueCode after authentication mismatch did not retain binding: %v", err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE external_identities SET archived_at=clock_timestamp() WHERE id=?`, identity.ID.String()); err != nil {
		t.Fatal(err)
	}
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-external-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	exchange.Capabilities = issue.Capabilities
	if result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange); result != nil || !errors.Is(exchangeErr, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("Exchange(archived identity) = %#v, %v", result, exchangeErr)
	}
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE external_identities SET archived_at=NULL WHERE id=?`, identity.ID.String()); err != nil {
		t.Fatal(err)
	}
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "external-exchange-after-restore")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID.String(), model.GetMillis()
	if result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange); exchangeErr != nil || result == nil || result.Session == nil {
		t.Fatalf("Exchange after identity restore did not retain code = %#v, %v", result, exchangeErr)
	}
}

func TestDesktopAuthorizationMaximumActiveSessionConflictDoesNotConsumeCode(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-session-limit", DisplayName: "Desktop Session Limit"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-session-limit", Email: "desktop-session-limit@example.edu"})
	created, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	now := model.NowUTC()
	existing, _, err := persistence.Session().Save(ctx, &model.Session{UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour)}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(2 * time.Hour)},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "session-limit-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	exchange.MaximumActive = 1
	if result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange); result != nil || !store.IsConflict(exchangeErr) {
		t.Fatalf("Exchange(at session limit) = %#v, %v; want conflict", result, exchangeErr)
	}
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	if sessions, listErr := persistence.Session().ListByUser(ctx, user.ID.String()); listErr != nil || len(sessions) != 1 || sessions[0].ID != existing.ID {
		t.Fatalf("sessions after limit rollback = %#v, %v", sessions, listErr)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp(),revocation_reason='user_session' WHERE id=?`, existing.ID.String()); err != nil {
		t.Fatal(err)
	}
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange-after-session-slot")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID.String(), model.GetMillis()
	result, err := persistence.BrowserAuthentication().Exchange(ctx, exchange)
	if err != nil || result == nil || result.Session == nil {
		t.Fatalf("Exchange after freeing slot did not retain code %s = %#v, %v", created.ID, result, err)
	}
}

func assertDesktopAuthorizationAuditAttempt(t *testing.T, ctx context.Context, persistence *SQLStore, id model.AuditEventID) {
	t.Helper()
	event, err := persistence.Audit().Get(ctx, id.String())
	if err != nil || event.Status != model.AuditStatusAttempt {
		t.Fatalf("desktop authorization audit = %#v, %v; want attempt", event, err)
	}
}

func waitForSecondLockWaiter(t *testing.T, ctx context.Context, persistence *SQLStore, controllerPID, firstWaiterPID int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := persistence.GetMaster().Get(ctx, &waiting, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE pid NOT IN (?, ?, pg_backend_pid())
			AND cardinality(pg_blocking_pids(pid)) > 0
			AND (query LIKE '%FROM users%' OR query LIKE '%pg_advisory_xact_lock%'))`, controllerPID, firstWaiterPID)
		if err != nil {
			t.Fatalf("inspect PostgreSQL lock-order probe: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestDesktopAuthorizationRetentionTerminalizesEveryLiveStateAndPurgesSafeMetadata(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-retention", DisplayName: "Desktop Retention"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-retention", Email: "desktop-retention@example.edu"})

	issued, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET created_at=aged.now-interval '3 minutes', updated_at=aged.now-interval '2 minutes',
		    expires_at=aged.now+interval '2 minutes', authenticated_at=aged.now-interval '2 minutes',
		    code_expires_at=aged.now-interval '1 minute'
		FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, issued.ID.String()); err != nil {
		t.Fatal(err)
	}
	maintained, err := persistence.BrowserAuthentication().Maintain(ctx, 500)
	if err != nil || maintained.Expired != 1 {
		t.Fatalf("Maintain(expired code) = %#v, %v", maintained, err)
	}
	var expired browserAuthenticationRow
	if err = persistence.GetMaster().Get(ctx, &expired, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, issued.ID.String()); err != nil {
		t.Fatal(err)
	}
	expiredModel, err := expired.model()
	if err != nil {
		t.Fatal(err)
	}
	if expiredModel.State != model.BrowserAuthenticationStateExpired || expiredModel.StateHash != "" ||
		expiredModel.CallbackURL != "" || expiredModel.CodeChallenge != "" || expiredModel.CodeHash != "" ||
		!expiredModel.UserID.IsValid() || !expiredModel.ExpiredAt.Valid {
		t.Fatalf("expired issued-code transaction = %#v", expiredModel)
	}
	if _, err = persistence.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier,
		saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "expired-exchange"))); !store.IsNotFound(err) {
		t.Fatalf("Exchange(expired) error = %v", err)
	}

	cancelled, handle, proof, cancelState, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	cancelBinding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, cancelState)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistence.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{
		BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState),
	}); err != nil {
		t.Fatal(err)
	}
	exchanged, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, exchanged); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET state='exchanged', handle_hash=NULL, browser_proof_hash=NULL, state_hash=NULL, callback_url=NULL,
		    code_challenge=NULL, proposed_public_jwk=NULL, proposed_key_thumbprint=NULL, desktop_release=NULL,
		    desktop_build_id=NULL, desktop_platform=NULL, desktop_architecture=NULL, desktop_realtime_protocol=NULL,
		    user_id=?, authentication_method='password', authentication_strength='single_factor',
		    authenticated_at=created_at, exchanged_at=updated_at WHERE id=?`, user.ID.String(), exchanged.ID.String()); err != nil {
		t.Fatal(err)
	}

	for _, terminal := range []struct{ id string }{{issued.ID.String()}, {cancelled.ID.String()}, {exchanged.ID.String()}} {
		if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions SET
			created_at=aged.now-interval '24 hours 6 minutes', expires_at=aged.now-interval '24 hours 1 minute',
			cancelled_at=CASE WHEN state='cancelled' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			exchanged_at=CASE WHEN state='exchanged' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			expired_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE NULL END,
			updated_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE aged.now-interval '24 hours 2 minutes' END,
			authenticated_at=CASE WHEN user_id IS NOT NULL THEN aged.now-interval '24 hours 5 minutes' ELSE NULL END
			FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, terminal.id); err != nil {
			t.Fatal(err)
		}
	}
	maintained, err = persistence.BrowserAuthentication().Maintain(ctx, 500)
	if err != nil || maintained.Purged != 3 {
		t.Fatalf("Maintain(terminal retention) = %#v, %v", maintained, err)
	}
	var retained int
	if err = persistence.GetMaster().Get(ctx, &retained, `SELECT COUNT(*) FROM browser_authentication_transactions WHERE id IN (?,?,?)`,
		issued.ID.String(), cancelled.ID.String(), exchanged.ID.String()); err != nil {
		t.Fatal(err)
	}
	if retained != 0 {
		t.Fatalf("terminal rows past retention = %d", retained)
	}
}

func TestDesktopAuthorizationCreatesAndRevokesBoundDesktopRegistration(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "desktop-registration", DisplayName: "Desktop Registration",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "desktop-registration", Email: "desktop-registration@example.edu",
	})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "registration-exchange")
	accessToken, refreshToken := model.NewCredentialToken(), model.NewCredentialToken()
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, audit)
	exchange.AccessTokenHash = model.HashToken(accessToken)
	exchange.RefreshTokenHash = model.HashToken(refreshToken)
	exchanged, err := persistence.BrowserAuthentication().Exchange(ctx, exchange)
	if err != nil {
		t.Fatal(err)
	}
	if exchanged.Registration == nil || exchanged.Session == nil ||
		exchanged.Session.DesktopRegistrationID != exchanged.Registration.ID ||
		exchanged.Session.DPoPKeyThumbprint != exchanged.Registration.KeyThumbprint {
		t.Fatalf("Desktop exchange binding = %#v", exchanged)
	}
	registrations, err := persistence.DesktopRegistration().ListByUser(ctx, user.ID.String())
	if err != nil || len(registrations) != 1 || registrations[0].ID != exchanged.Registration.ID {
		t.Fatalf("ListByUser() = %#v, %v", registrations, err)
	}

	revokeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "registration-revoke")
	revoked, err := persistence.DesktopRegistration().RevokeWithAudit(ctx, &store.DesktopRegistrationRevocation{
		RegistrationID: exchanged.Registration.ID, UserID: user.ID, RevokedAt: model.GetMillis(),
		AuditEventID: revokeAudit.ID.String(), AuditAt: model.GetMillis(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.AlreadyRevoked || revoked.Registration == nil || !revoked.Registration.RevokedAt.Valid ||
		len(revoked.Sessions) != 1 || revoked.Sessions[0].RevocationReason != model.SessionRevocationDesktopRegistration ||
		len(revoked.TokenHashes) != 2 {
		t.Fatalf("RevokeWithAudit() = %#v", revoked)
	}
	for _, credential := range []struct {
		token string
		kind  model.SessionCredentialKind
	}{{accessToken, model.SessionCredentialAccess}, {refreshToken, model.SessionCredentialRefresh}} {
		storedCredential, storedSession, credentialErr := persistence.SessionCredential().GetSessionByTokenHash(
			ctx, model.HashToken(credential.token), credential.kind,
		)
		if credentialErr != nil || !storedCredential.RevokedAt.Valid || !storedSession.RevokedAt.Valid ||
			storedSession.RevocationReason != model.SessionRevocationDesktopRegistration {
			t.Fatalf("revoked %s credential = %#v, session = %#v, error = %v", credential.kind, storedCredential, storedSession, credentialErr)
		}
	}
}

func issueDesktopAuthorizationForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) (*store.DesktopAuthorizationCreated, string, string, string) {
	t.Helper()
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institutionID)
	created, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	if err != nil {
		t.Fatal(err)
	}
	code := model.NewCredentialToken()
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, persistence, handle, proof, state, userID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institutionID, userID, "issue")
	_, err = persistence.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code), ExpectedUserID: userID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
	})
	if err != nil {
		t.Fatalf("issue Desktop authorization code: %v (cause: %v)", err, errors.Unwrap(err))
	}
	return created, code, state, verifier
}

func bindDesktopAuthorizationForSQLTest(ctx context.Context, persistence *SQLStore, handle, proof, state string) (string, error) {
	binding := model.NewCredentialToken()
	_, err := persistence.BrowserAuthentication().BindDesktopAuthorization(ctx, &store.DesktopAuthorizationBinding{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
		StateHash: model.HashToken(state), BindingHash: model.HashToken(binding),
	})
	return binding, err
}

func bindAndAuthenticateDesktopAuthorizationForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore,
	handle, proof, state string, userID model.UserID, method, providerID string, identityID model.ExternalIdentityID,
	capabilities store.AccessDeploymentCapabilities,
) string {
	t.Helper()
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	if err != nil {
		t.Fatal(err)
	}
	result, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: userID,
			AuthenticationMethod: method, AuthenticationProviderID: providerID, ExternalIdentityID: identityID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(), Capabilities: capabilities})
	if err != nil || result == nil || result.Denied {
		t.Fatalf("AuthenticateDesktopAuthorization() = %#v, %v", result, err)
	}
	return binding
}

func desktopAuthorizationTransactionForSQLTest(_ time.Time, institutionID model.InstitutionID) (*store.DesktopAuthorizationCreation, string, string, string, string) {
	handle, proof, state := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	verifier := model.NewCredentialToken()
	publicJWK, thumbprint := desktopAuthorizationKeyForSQLTest()
	transaction := &store.DesktopAuthorizationCreation{ID: model.NewBrowserAuthenticationTransactionID(),
		InstitutionID: institutionID, Issuer: "https://proctor.example.edu", HandleHash: model.HashToken(handle),
		BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state), CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		CodeChallenge: model.PKCES256Challenge(verifier),
		DeviceName:    "SQL integration Desktop", ProposedPublicJWK: publicJWK, ProposedKeyThumbprint: thumbprint,
		DesktopRelease: "0.1.0", DesktopBuildID: "sql-integration-build", DesktopPlatform: model.DesktopPlatformDarwin,
		DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		Lifetime: model.BrowserAuthenticationTransactionLifetime}
	return transaction, handle, proof, state, verifier
}

func desktopAuthorizationExchangeForSQLTest(code, state, verifier string, audit *model.AuditEvent) *store.DesktopAuthorizationExchange {
	publicJWK, thumbprint := desktopAuthorizationKeyForSQLTest()
	return &store.DesktopAuthorizationExchange{CodeHash: model.HashToken(code), StateHash: model.HashToken(state),
		CodeChallenge: model.PKCES256Challenge(verifier), Issuer: "https://proctor.example.edu",
		ExpectedPublicJWK: publicJWK, ExpectedKeyThumbprint: thumbprint,
		DesktopRelease: "0.1.0", DesktopBuildID: "sql-integration-build", DesktopPlatform: model.DesktopPlatformDarwin,
		DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		DesktopCompatibilityPolicyRevision: 1,
		AccessTokenHash:                    model.HashToken(model.NewCredentialToken()), RefreshTokenHash: model.HashToken(model.NewCredentialToken()),
		AccessLifetime: 5 * time.Minute, RefreshLifetime: time.Hour, IdleLifetime: 30 * time.Minute, AbsoluteLifetime: time.Hour,
		MaximumActive: 10, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}

func desktopAuthorizationKeyForSQLTest() (model.DesktopPublicJWK, string) {
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	encode := func(coordinate []byte) string {
		padded := make([]byte, 32)
		copy(padded[32-len(coordinate):], coordinate)
		return base64.RawURLEncoding.EncodeToString(padded)
	}
	publicJWK := model.DesktopPublicJWK{
		Kty: "EC", Crv: "P-256", X: encode(x.Bytes()), Y: encode(y.Bytes()),
	}
	thumbprint, err := publicJWK.Thumbprint()
	if err != nil {
		panic(err)
	}
	return publicJWK, thumbprint
}

func saveDesktopAuthorizationAuditForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: "authentication.desktop_authorization",
		Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "desktop-retention", Parameters: []byte(`{"operation":"` + operation + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func saveUserStateAuditForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) *model.AuditEvent {
	t.Helper()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: string(model.ActionUserManage),
		Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "desktop-disable-race"})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
