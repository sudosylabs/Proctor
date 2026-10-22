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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	const total = 24
	for index := 0; index < total; index++ {
		createdAt := model.NowUTC().Add(-10 * time.Minute)
		transaction, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(createdAt, institution.ID)
		_, err = first.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
		require.NoError(t, err)
		_, err = first.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
			SET created_at=?,updated_at=?,expires_at=? WHERE id=?`, createdAt, createdAt,
			createdAt.Add(transaction.Lifetime), transaction.ID.String())
		require.NoError(t, err)
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
		require.NoError(t, result.err, "concurrent Maintain()")
		require.NotNil(t, result.result, "concurrent Maintain()")
		expired += result.result.Expired
	}
	require.Equal(t, 20, expired, "concurrent expired rows")
	last, err := first.BrowserAuthentication().Maintain(ctx, 10)
	require.NoError(t, err, "final Maintain() = %#v", last)
	require.Equal(t, 4, last.Expired, "final Maintain() = %#v", last)
	require.False(t, last.More, "final Maintain() = %#v", last)
	var activeProofs int
	err = first.GetMaster().Get(ctx, &activeProofs, `SELECT COUNT(*) FROM browser_authentication_transactions
		WHERE state <> 'expired' OR handle_hash IS NOT NULL OR browser_proof_hash IS NOT NULL OR state_hash IS NOT NULL
		   OR callback_url IS NOT NULL OR code_challenge IS NOT NULL OR code_hash IS NOT NULL
		   OR proposed_public_jwk IS NOT NULL OR proposed_key_thumbprint IS NOT NULL`)
	require.NoError(t, err)
	require.Equal(t, 0, activeProofs, "maintenance left")
}

func TestDesktopAuthorizationExchangeSerializesWithConcurrentUserDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-disable-race", DisplayName: "Desktop Disable Race"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-disable-race", Email: "desktop-disable-race@example.edu"})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID)

	const pauseKey int64 = 8154700260819
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	var controllerPID int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()
	_, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_desktop_session() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260819);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_desktop_session BEFORE INSERT ON sessions
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_desktop_session()`)
	require.NoError(t, err)
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
	// The fixture asserts, so it is built on the test goroutine.
	disable := storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.NowUTC(),
		RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID, AuditAt: model.NowUTC(),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	go func() {
		result, disableErr := persistence.User().SetDisabled(ctx, disable)
		disableResult <- disableOutcome{result: result, err: disableErr}
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, exchangePID, "pg_advisory_xact_lock")
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey)
	require.NoError(t, err)
	locked = false
	exchanged := <-exchangeResult
	require.NoError(t, exchanged.err, "Exchange()")
	require.NotNil(t, exchanged.result, "Exchange()")
	require.NotNil(t, exchanged.result.Session, "Exchange()")
	disabled := <-disableResult
	require.NoError(t, disabled.err, "SetDisabled()")
	require.NotNil(t, disabled.result, "SetDisabled()")
	require.NotNil(t, disabled.result.User, "SetDisabled()")
	require.True(t, disabled.result.User.DisabledAt.Valid, "SetDisabled()")
	storedSession, err := persistence.Session().Get(ctx, exchanged.result.Session.ID)
	require.NoError(t, err)
	require.True(t, storedSession.RevokedAt.Valid)
	require.Equal(t, model.SessionRevocationAccountDisabled, storedSession.RevocationReason)
}

func TestDesktopAuthorizationIssueCodeSerializesWithConcurrentUserDisableAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primary := openTestStore(t)
	resetTestStore(t, primary)
	secondary := openTestStore(t)
	institution, err := primary.Institution().Save(ctx, &model.Institution{Name: "desktop-issue-disable-race", DisplayName: "Desktop Issue Disable Race"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, primary, &model.User{Username: "desktop-issue-disable-race", Email: "desktop-issue-disable-race@example.edu"})
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = primary.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, primary, handle, proof, state, user.ID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, primary, institution.ID, user.ID, "issue-disable-race")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, primary, institution.ID, user.ID)

	controller, err := primary.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	var controllerPID int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `BEGIN`)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	_, err = controller.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user.ID.String())
	require.NoError(t, err)

	type disableOutcome struct {
		result *store.UserDisabledStateResult
		err    error
	}
	disableResult := make(chan disableOutcome, 1)
	// The fixture asserts, so it is built on the test goroutine.
	disable := storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.NowUTC(),
		RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID, AuditAt: model.NowUTC(),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	go func() {
		result, disableErr := secondary.User().SetDisabled(ctx, disable)
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
			AuditEventID: issueAudit.ID, AuditAt: model.NowUTC(),
		})
		issueResult <- issueOutcome{result: result, err: issueErr}
	}()
	waitForSecondLockWaiter(t, ctx, primary, controllerPID, disablePID)
	_, err = controller.ExecContext(ctx, `ROLLBACK`)
	require.NoError(t, err)
	locked = false
	disabled := <-disableResult
	require.NoError(t, disabled.err, "SetDisabled()")
	require.NotNil(t, disabled.result, "SetDisabled()")
	require.NotNil(t, disabled.result.User, "SetDisabled()")
	require.True(t, disabled.result.User.DisabledAt.Valid, "SetDisabled()")
	issued := <-issueResult
	require.True(t, store.IsNotFound(issued.err), "IssueCode() after concurrent disable")
	require.Nil(t, issued.result, "IssueCode() after concurrent disable")
	after, err := primary.Audit().Get(ctx, issueAudit.ID)
	require.NoError(t, err, "IssueCode audit = %#v", after)
	require.Equal(t, model.AuditStatusAttempt, after.Status, "IssueCode audit = %#v", after)
}

func TestDesktopAuthorizationRejectsArchivedUsersWithoutConsumingProofsOrCodes(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-archived-user", DisplayName: "Desktop Archived User"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-archived-user", Email: "desktop-archived-user@example.edu"})
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, persistence, handle, proof, state, user.ID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=clock_timestamp() WHERE id=?`, user.ID.String())
	require.NoError(t, err)
	code := model.NewCredentialToken()
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-issue")
	issue := &store.DesktopAuthorizationCodeIssue{BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code),
		ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: issueAudit.ID, AuditAt: model.NowUTC()}
	result, issueErr := persistence.BrowserAuthentication().IssueCode(ctx, issue)
	require.Nil(t, result, "IssueCode(archived User)")
	require.True(t, store.IsNotFound(issueErr), "IssueCode(archived User)")
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, issueAudit.ID)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=NULL WHERE id=?`, user.ID.String())
	require.NoError(t, err)
	issueAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "issue-after-unarchive")
	issue.AuditEventID, issue.AuditAt = issueAudit.ID, model.NowUTC()
	_, err = persistence.BrowserAuthentication().IssueCode(ctx, issue)
	require.NoError(t, err)

	_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=clock_timestamp() WHERE id=?`, user.ID.String())
	require.NoError(t, err)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	exchanged, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.Nil(t, exchanged, "Exchange(archived User)")
	require.True(t, store.IsNotFound(exchangeErr), "Exchange(archived User) = %v; want not found", exchangeErr)
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	sessions, listErr := persistence.Session().ListByUser(ctx, &store.SessionPageQuery{UserID: user.ID, Limit: store.MaximumSessionPageSize})
	require.NoError(t, listErr, "archived exchange sessions")
	require.Empty(t, sessions, "archived exchange sessions")
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE users SET archived_at=NULL WHERE id=?`, user.ID.String())
	require.NoError(t, err)
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange-after-unarchive")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID, model.NowUTC()
	exchanged, exchangeErr = persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.NoError(t, exchangeErr, "Exchange after unarchive did not retain code")
	require.NotNil(t, exchanged, "Exchange after unarchive did not retain code")
	require.NotNil(t, exchanged.Session, "Exchange after unarchive did not retain code")
}

func TestDesktopAuthorizationRejectsMismatchedOrArchivedExternalIdentity(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	seedTestAuthenticationPolicy(t, persistence, map[string]model.ProviderAdmissionMode{"campus-cas": model.ProviderAdmissionLinkedOnly})
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-external-fence", DisplayName: "Desktop External Fence"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-external-user", Email: "desktop-external-user@example.edu"})
	other := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-external-other", Email: "desktop-external-other@example.edu"})
	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
		Subject: "desktop-external-user", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	otherIdentity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: other.ID, Provider: "campus-cas",
		Subject: "desktop-external-other", LastSeenAt: model.OptionalTimeFrom(model.NowUTC())})
	require.NoError(t, err)
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	require.NoError(t, err)
	code := model.NewCredentialToken()
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "mismatched-external-issue")
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}}
	result, authenticationErr := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: user.ID,
			AuthenticationMethod: "oidc", AuthenticationProviderID: "campus-cas", ExternalIdentityID: otherIdentity.ID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(), Capabilities: capabilities})
	require.Nil(t, result, "AuthenticateDesktopAuthorization(mismatched identity)")
	require.ErrorIs(t, authenticationErr, store.ErrAuthenticationMethodDisabled, "AuthenticateDesktopAuthorization(mismatched identity)")
	result, authenticationErr = persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: user.ID,
			AuthenticationMethod: "oidc", AuthenticationProviderID: "campus-cas", ExternalIdentityID: identity.ID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(), Capabilities: capabilities})
	require.NoError(t, authenticationErr, "AuthenticateDesktopAuthorization(matched identity)")
	require.NotNil(t, result, "AuthenticateDesktopAuthorization(matched identity)")
	require.False(t, result.Denied, "AuthenticateDesktopAuthorization(matched identity)")
	issue := &store.DesktopAuthorizationCodeIssue{BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(code), ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
		AuditEventID: issueAudit.ID, AuditAt: model.NowUTC()}
	_, err = persistence.BrowserAuthentication().IssueCode(ctx, issue)
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE external_identities SET archived_at=clock_timestamp() WHERE id=?`, identity.ID.String())
	require.NoError(t, err)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "archived-external-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	exchange.Capabilities = issue.Capabilities
	exchanged, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.Nil(t, exchanged, "Exchange(archived identity)")
	require.ErrorIs(t, exchangeErr, store.ErrAuthenticationMethodDisabled, "Exchange(archived identity)")
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE external_identities SET archived_at=NULL WHERE id=?`, identity.ID.String())
	require.NoError(t, err)
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "external-exchange-after-restore")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID, model.NowUTC()
	exchanged, exchangeErr = persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.NoError(t, exchangeErr, "Exchange after identity restore did not retain code")
	require.NotNil(t, exchanged, "Exchange after identity restore did not retain code")
	require.NotNil(t, exchanged.Session, "Exchange after identity restore did not retain code")
}

func TestDesktopAuthorizationMaximumActiveSessionConflictDoesNotConsumeCode(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-session-limit", DisplayName: "Desktop Session Limit"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-session-limit", Email: "desktop-session-limit@example.edu"})
	created, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	now := model.NowUTC()
	existing, _, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, &model.Session{UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour)}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(2 * time.Hour)},
	}, 10))
	require.NoError(t, err)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "session-limit-exchange")
	exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit)
	exchange.MaximumActive = 1
	result, exchangeErr := persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.Nil(t, result, "Exchange(at session limit)")
	require.True(t, store.IsConflict(exchangeErr), "Exchange(at session limit)")
	assertDesktopAuthorizationAuditAttempt(t, ctx, persistence, exchangeAudit.ID)
	sessions, listErr := persistence.Session().ListByUser(ctx, &store.SessionPageQuery{UserID: user.ID, Limit: store.MaximumSessionPageSize})
	require.NoError(t, listErr, "sessions after limit rollback")
	require.Len(t, sessions, 1, "sessions after limit rollback")
	require.Equal(t, existing.ID, sessions[0].ID, "sessions after limit rollback")
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp(),revocation_reason='user_session' WHERE id=?`, existing.ID.String())
	require.NoError(t, err)
	exchangeAudit = saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange-after-session-slot")
	exchange.AuditEventID, exchange.AuditAt = exchangeAudit.ID, model.NowUTC()
	result, err = persistence.BrowserAuthentication().Exchange(ctx, exchange)
	require.NoError(t, err, "Exchange after freeing slot did not retain code %s", created.ID)
	require.NotNil(t, result, "Exchange after freeing slot did not retain code %s", created.ID)
	require.NotNil(t, result.Session, "Exchange after freeing slot did not retain code %s", created.ID)
}

func TestDesktopAuthorizationRevalidatesSourceWebSessionProof(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-source-proof", DisplayName: "Desktop Source Proof"})
	require.NoError(t, err)
	for _, name := range []string{"live", "future authentication", "revoked session", "expired session", "idle expired session", "revoked credential", "expired credential", "rotated credential", "other Session credential", "refresh credential"} {
		t.Run(name, func(t *testing.T) {
			user := saveIntegrationUser(t, ctx, persistence, &model.User{
				Username: "source-" + model.NewID(), Email: model.NewID() + "@source.example.edu",
			})
			session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
			session.AuthenticationStrength = model.AuthenticationMultiFactor
			session.AuthenticatedAt = model.NowUTC().Add(-2 * time.Minute).Truncate(time.Millisecond).Add(321 * time.Microsecond)
			session.MFACompletedAt = model.OptionalTimeFrom(session.AuthenticatedAt.Add(time.Minute + 123*time.Microsecond))
			if name == "future authentication" {
				session.AuthenticationStrength, session.MFACompletedAt = model.AuthenticationSingleFactor, model.OptionalTime{}
			}
			creation := sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10)
			source, savedCredentials, err := storetest.SaveSession(ctx, persistence, creation)
			require.NoError(t, err)
			var access, refresh *model.SessionCredential
			for _, credential := range savedCredentials {
				if credential.Kind == model.SessionCredentialAccess {
					access = credential
				} else {
					refresh = credential
				}
			}
			require.NotNil(t, access, "source Session fixture has no credential pair")
			require.NotNil(t, refresh, "source Session fixture has no credential pair")
			transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
			_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
			require.NoError(t, err)
			binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
			require.NoError(t, err)
			input := &store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(binding), UserID: user.ID, AuthenticationMethod: "password",
				AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(),
				SourceSessionID: source.ID, SourceCredentialID: access.ID,
				Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			}
			switch name {
			case "future authentication":
				_, err = persistence.GetMaster().Exec(ctx, `UPDATE sessions
					SET created_at=created_at+interval '1 hour',updated_at=updated_at+interval '1 hour',
					    authenticated_at=authenticated_at+interval '1 hour',last_activity_at=last_activity_at+interval '1 hour',
					    idle_expires_at=idle_expires_at+interval '1 hour',expires_at=expires_at+interval '1 hour'
					WHERE id=?`, source.ID.String())
			case "revoked session":
				_, err = persistence.Session().Withdraw(ctx, &store.SessionWithdrawal{ID: source.ID, UserID: user.ID, RevokedAt: model.NowUTC(), Reason: model.SessionRevocationInactiveUser})
			case "expired session":
				_, err = persistence.GetMaster().Exec(ctx, `UPDATE sessions SET expires_at=statement_timestamp(),idle_expires_at=statement_timestamp() WHERE id=?`, source.ID.String())
			case "idle expired session":
				_, err = persistence.GetMaster().Exec(ctx, `UPDATE sessions SET idle_expires_at=clock_timestamp() WHERE id=?`, source.ID.String())
			case "revoked credential":
				_, err = persistence.GetMaster().Exec(ctx, `UPDATE session_credentials SET revoked_at=clock_timestamp() WHERE id=?`, access.ID.String())
			case "expired credential":
				_, err = persistence.GetMaster().Exec(ctx, `UPDATE session_credentials SET expires_at=clock_timestamp() WHERE id=?`, access.ID.String())
			case "rotated credential":
				now := model.TimeUTC(time.Now())
				_, err = persistence.SessionCredential().RotateRefresh(ctx, &store.SessionRefreshRotation{TokenHash: refresh.TokenHash, Access: &model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(time.Minute)}, Refresh: &model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: source.ExpiresAt}, Now: now, IdleExpiresAt: now.Add(time.Hour)})
			case "other Session credential":
				otherSession, otherCredentials := authenticationPolicyTestSession(user.ID, "password", "", "")
				_, otherSavedCredentials, saveErr := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, otherSession, otherCredentials, 10))
				err = saveErr
				for _, credential := range otherSavedCredentials {
					if credential.Kind == model.SessionCredentialAccess {
						input.SourceCredentialID = credential.ID
					}
				}
			case "refresh credential":
				input.SourceCredentialID = refresh.ID
			}
			require.NoError(t, err)
			if name == "future authentication" {
				future, getErr := persistence.Session().Get(ctx, source.ID)
				require.NoError(t, getErr)
				require.NoError(t, future.Validate())
			}
			result, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, input)
			if name != "live" {
				require.Nil(t, result, "AuthenticateDesktopAuthorization(%s)", name)
				require.Error(t, err, "AuthenticateDesktopAuthorization(%s)", name)
				require.False(t, name != "future authentication" && !store.IsNotFound(err), "AuthenticateDesktopAuthorization(%s)", name)
				current, contextErr := persistence.BrowserAuthentication().GetDesktopAuthorizationContext(ctx, input.BindingHash)
				require.NoError(t, contextErr, "rejected source changed browser transaction = %#v", current)
				require.Equal(t, model.BrowserAuthenticationStateBound, current.State, "rejected source changed browser transaction = %#v", current)
				require.True(t, current.UserID.IsZero(), "rejected source changed browser transaction = %#v", current)
				return
			}
			require.NoError(t, err, "AuthenticateDesktopAuthorization(live)")
			require.NotNil(t, result, "AuthenticateDesktopAuthorization(live)")
			require.False(t, result.Denied, "AuthenticateDesktopAuthorization(live)")
			var stored browserAuthenticationRow
			err = persistence.GetMaster().Get(ctx, &stored, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, transaction.ID.String())
			require.NoError(t, err)
			authenticated, err := stored.model()
			require.NoError(t, err)
			require.Equal(t, source.AuthenticationStrength, authenticated.AuthenticationStrength, "Desktop authentication did not derive current assurance and password proof from the live source Session")
			require.True(t, authenticated.AuthenticatedAt.Time.Equal(source.AuthenticatedAt), "Desktop authentication did not derive current assurance and password proof from the live source Session")
			require.True(t, authenticated.MFACompletedAt.Time.Equal(source.MFACompletedAt.Time), "Desktop authentication did not derive current assurance and password proof from the live source Session")
			require.Equal(t, creation.PasswordProof.ID, authenticated.PasswordCredentialID, "Desktop authentication did not derive current assurance and password proof from the live source Session")
			require.Equal(t, creation.PasswordProof.Revision, authenticated.PasswordCredentialRevision, "Desktop authentication did not derive current assurance and password proof from the live source Session")
			require.Equal(t, model.AuthenticationSingleFactor, input.AuthenticationStrength, "Desktop authentication mutated the caller's source proof")
			require.False(t, input.PasswordProof.IsValid(), "Desktop authentication mutated the caller's source proof")
		})
	}
}

func assertDesktopAuthorizationAuditAttempt(t *testing.T, ctx context.Context, persistence *SQLStore, id model.AuditEventID) {
	t.Helper()
	event, err := persistence.Audit().Get(ctx, id)
	require.NoError(t, err, "desktop authorization audit = %#v, %v; want attempt", event, err)
	require.Equal(t, model.AuditStatusAttempt, event.Status, "desktop authorization audit = %#v, %v; want attempt", event, err)
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
		require.NoError(t, err)
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			require.FailNow(t, fmt.Sprint(ctx.Err()))
		case <-ticker.C:
		}
	}
}

func TestDesktopAuthorizationRetentionTerminalizesEveryLiveStateAndPurgesSafeMetadata(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-retention", DisplayName: "Desktop Retention"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-retention", Email: "desktop-retention@example.edu"})

	issued, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET created_at=aged.now-interval '3 minutes', updated_at=aged.now-interval '2 minutes',
		    expires_at=aged.now+interval '2 minutes', authenticated_at=aged.now-interval '2 minutes',
		    code_expires_at=aged.now-interval '1 minute'
		FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, issued.ID.String())
	require.NoError(t, err)
	maintained, err := persistence.BrowserAuthentication().Maintain(ctx, 500)
	require.NoError(t, err, "Maintain(expired code) = %#v", maintained)
	require.Equal(t, 1, maintained.Expired, "Maintain(expired code) = %#v", maintained)
	var expired browserAuthenticationRow
	err = persistence.GetMaster().Get(ctx, &expired, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, issued.ID.String())
	require.NoError(t, err)
	expiredModel, err := expired.model()
	require.NoError(t, err)
	require.Equal(t, model.BrowserAuthenticationStateExpired, expiredModel.State)
	require.Empty(t, expiredModel.StateHash)
	require.Empty(t, expiredModel.CallbackURL)
	require.Empty(t, expiredModel.CodeChallenge)
	require.Empty(t, expiredModel.CodeHash)
	require.True(t, expiredModel.UserID.IsValid())
	require.True(t, expiredModel.ExpiredAt.Valid)
	_, err = persistence.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier,
		saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "expired-exchange")))
	require.True(t, store.IsNotFound(err), "Exchange(expired) error")

	canceled, handle, proof, cancelState, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, canceled)
	require.NoError(t, err)
	cancelBinding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, cancelState)
	require.NoError(t, err)
	err = persistence.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{
		BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState),
	})
	require.NoError(t, err)
	exchanged, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, exchanged)
	require.NoError(t, err)
	passwordProof := passwordProofForSQLTest(t, ctx, persistence, user.ID)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET state='exchanged', handle_hash=NULL, browser_proof_hash=NULL, state_hash=NULL, callback_url=NULL,
		    code_challenge=NULL, proposed_public_jwk=NULL, proposed_key_thumbprint=NULL, desktop_release=NULL,
		    desktop_build_id=NULL, desktop_platform=NULL, desktop_architecture=NULL, desktop_realtime_protocol=NULL,
		    user_id=?, authentication_method='password', authentication_strength='single_factor',
		    password_credential_id=?, password_credential_revision=?,
		    authenticated_at=created_at, exchanged_at=updated_at WHERE id=?`,
		user.ID.String(), passwordProof.ID.String(), passwordProof.Revision, exchanged.ID.String())
	require.NoError(t, err)

	for _, terminal := range []struct{ id string }{{issued.ID.String()}, {canceled.ID.String()}, {exchanged.ID.String()}} {
		_, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions SET
			created_at=aged.now-interval '24 hours 6 minutes', expires_at=aged.now-interval '24 hours 1 minute',
			canceled_at=CASE WHEN state='canceled' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			exchanged_at=CASE WHEN state='exchanged' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			expired_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE NULL END,
			updated_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE aged.now-interval '24 hours 2 minutes' END,
			authenticated_at=CASE WHEN user_id IS NOT NULL THEN aged.now-interval '24 hours 5 minutes' ELSE NULL END
			FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, terminal.id)
		require.NoError(t, err)
	}
	maintained, err = persistence.BrowserAuthentication().Maintain(ctx, 500)
	require.NoError(t, err, "Maintain(terminal retention) = %#v", maintained)
	require.Equal(t, 3, maintained.Purged, "Maintain(terminal retention) = %#v", maintained)
	var retained int
	err = persistence.GetMaster().Get(ctx, &retained, `SELECT COUNT(*) FROM browser_authentication_transactions WHERE id IN (?,?,?)`,
		issued.ID.String(), canceled.ID.String(), exchanged.ID.String())
	require.NoError(t, err)
	require.Equal(t, 0, retained, "terminal rows past retention")
}

func TestDesktopAuthorizationCreatesAndRevokesBoundDesktopRegistration(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "desktop-registration", DisplayName: "Desktop Registration",
	})
	require.NoError(t, err)
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
	require.NoError(t, err)
	require.NotNil(t, exchanged.Registration)
	require.NotNil(t, exchanged.Session)
	require.Equal(t, exchanged.Registration.ID, exchanged.Session.DesktopRegistrationID)
	require.Equal(t, exchanged.Registration.KeyThumbprint, exchanged.Session.DPoPKeyThumbprint)
	registrations, err := persistence.DesktopRegistration().ListByUser(ctx, user.ID)
	require.NoError(t, err, "ListByUser()")
	require.Len(t, registrations, 1, "ListByUser()")
	require.Equal(t, exchanged.Registration.ID, registrations[0].ID, "ListByUser()")

	revokeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "registration-revoke")
	revoked, err := persistence.DesktopRegistration().Revoke(ctx, &store.DesktopRegistrationRevocation{
		RegistrationID: exchanged.Registration.ID, UserID: user.ID, RevokedAt: model.NowUTC(),
		AuditEventID: revokeAudit.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	require.False(t, revoked.AlreadyRevoked)
	require.NotNil(t, revoked.Registration)
	require.True(t, revoked.Registration.RevokedAt.Valid)
	require.Len(t, revoked.Sessions, 1)
	require.Equal(t, model.SessionRevocationDesktopRegistration, revoked.Sessions[0].RevocationReason)
	require.Len(t, revoked.TokenHashes, 2)
	for _, credential := range []struct {
		token string
		kind  model.SessionCredentialKind
	}{{accessToken, model.SessionCredentialAccess}, {refreshToken, model.SessionCredentialRefresh}} {
		t.Run(string(credential.kind), func(t *testing.T) {
			storedCredential, storedSession, credentialErr := persistence.SessionCredential().GetSessionByTokenHash(
				ctx, model.HashToken(credential.token), credential.kind,
			)
			require.NoError(t, credentialErr, "revoked %s credential = %#v, session = %#v, error", credential.kind, storedCredential, storedSession)
			require.True(t, storedCredential.RevokedAt.Valid, "revoked %s credential = %#v, session = %#v, error", credential.kind, storedCredential, storedSession)
			require.True(t, storedSession.RevokedAt.Valid, "revoked %s credential = %#v, session = %#v, error", credential.kind, storedCredential, storedSession)
			require.Equal(t, model.SessionRevocationDesktopRegistration, storedSession.RevocationReason, "revoked %s credential = %#v, session = %#v, error", credential.kind, storedCredential, storedSession)
		})
	}
}

func issueDesktopAuthorizationForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) (*store.DesktopAuthorizationCreated, string, string, string) {
	t.Helper()
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institutionID)
	created, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	code := model.NewCredentialToken()
	binding := bindAndAuthenticateDesktopAuthorizationForSQLTest(t, ctx, persistence, handle, proof, state, userID,
		"password", "", "", store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}})
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institutionID, userID, "issue")
	_, err = persistence.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code), ExpectedUserID: userID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: audit.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err, "issue Desktop authorization code")
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
	require.NoError(t, err)
	var passwordProof store.PasswordCredentialProof
	if method == "password" {
		passwordProof = passwordProofForSQLTest(t, ctx, persistence, userID)
	}
	result, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
		&store.DesktopAuthorizationAuthentication{BindingHash: model.HashToken(binding), UserID: userID,
			PasswordProof:        passwordProof,
			AuthenticationMethod: method, AuthenticationProviderID: providerID, ExternalIdentityID: identityID,
			AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(), Capabilities: capabilities})
	require.NoError(t, err, "AuthenticateDesktopAuthorization()")
	require.NotNil(t, result, "AuthenticateDesktopAuthorization()")
	require.False(t, result.Denied, "AuthenticateDesktopAuthorization()")
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
		AuditEventID: audit.ID, AuditAt: model.NowUTC()}
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
	require.NoError(t, err)
	return event
}

func saveUserStateAuditForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) *model.AuditEvent {
	t.Helper()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: string(model.ActionUserManage),
		Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "desktop-disable-race"})
	require.NoError(t, err)
	return event
}
