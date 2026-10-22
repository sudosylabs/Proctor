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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestPasswordProofResetPreservesNewPasswordAgainstStaleRehash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence, institution, user, password := passwordProofFixture(t, ctx)
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	creation := sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10)
	rehash := &store.PasswordCredentialRehash{ID: password.ID, UserID: user.ID,
		ExpectedHash: password.PasswordHash, ExpectedRevision: password.Revision, PasswordHash: "upgraded-old-password"}
	completion := passwordProofReset(t, ctx, persistence, institution, user)
	reset, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
	require.NoError(t, err)
	err = persistence.PasswordCredential().Rehash(ctx, rehash)
	require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
	current, err := persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, completion.PasswordHash, current.PasswordHash, "stale rehash changed the reset credential or its lifecycle metadata")
	require.Equal(t, password.Revision+1, current.Revision, "stale rehash changed the reset credential or its lifecycle metadata")
	require.True(t, current.PasswordChangedAt.Equal(reset.PasswordCredential.PasswordChangedAt), "stale rehash changed the reset credential or its lifecycle metadata")
	_, _, err = storetest.SaveSession(ctx, persistence, creation)
	require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
}

func TestPasswordProofRehashPreservesOutstandingProof(t *testing.T) {
	ctx := context.Background()
	persistence, _, user, password := passwordProofFixture(t, ctx)
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	creation := sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10)
	require.NoError(t, persistence.PasswordCredential().Rehash(ctx, &store.PasswordCredentialRehash{
		ID: password.ID, UserID: user.ID, ExpectedHash: password.PasswordHash,
		ExpectedRevision: password.Revision, PasswordHash: "upgraded-same-password",
	}))
	_, _, err := storetest.SaveSession(ctx, persistence, creation)
	require.NoError(t, err)
}

func TestPasswordProofRehashSerializesWithReset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence, institution, user, password := passwordProofFixture(t, ctx)
	completion := passwordProofReset(t, ctx, persistence, institution, user)
	controllerPID, release := pausePasswordProofWrite(t, ctx, persistence, true)
	resetResult, rehashResult := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
		resetResult <- err
	}()
	resetPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "UPDATE password_credentials")
	go func() {
		rehashResult <- persistence.PasswordCredential().Rehash(ctx, &store.PasswordCredentialRehash{
			ID: password.ID, UserID: user.ID, ExpectedRevision: password.Revision,
			ExpectedHash: password.PasswordHash, PasswordHash: "upgraded-old-password",
		})
	}()
	waitForBlockedMailQuery(t, ctx, persistence, resetPID, "UPDATE password_credentials")
	release()
	require.NoError(t, (<-resetResult))
	require.ErrorIs(t, (<-rehashResult), store.ErrPasswordCredentialChanged)
	current, err := persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, completion.PasswordHash, current.PasswordHash)
	require.Equal(t, password.Revision+1, current.Revision)
}

func TestPasswordProofResetSerializesWithSessionCreation(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		for _, resetFirst := range []bool{false, true} {
			name := "ordinary"
			if desktop {
				name = "desktop"
			}
			if resetFirst {
				name += "/reset_first"
			} else {
				name += "/creation_first"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				persistence, institution, user, _ := passwordProofFixture(t, ctx)
				completion := passwordProofReset(t, ctx, persistence, institution, user)
				create := func() (*model.Session, error) { return nil, nil }
				if desktop {
					_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
					audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
					exchange := desktopAuthorizationExchangeForSQLTest(code, state, verifier, audit)
					create = func() (*model.Session, error) {
						result, err := persistence.BrowserAuthentication().Exchange(ctx, exchange)
						if err != nil {
							return nil, err
						}
						return result.Session, nil
					}
				} else {
					session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
					creation := sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10)
					create = func() (*model.Session, error) {
						result, _, err := storetest.SaveSession(ctx, persistence, creation)
						return result, err
					}
				}
				reset := func() (*model.Session, error) {
					_, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
					return nil, err
				}
				first, second := create, reset
				query := "INSERT INTO sessions"
				if resetFirst {
					first, second = reset, create
					query = "UPDATE password_credentials"
				}
				controllerPID, release := pausePasswordProofWrite(t, ctx, persistence, resetFirst)
				type outcome struct {
					session *model.Session
					err     error
				}
				firstResult, secondResult := make(chan outcome, 1), make(chan outcome, 1)
				go func() { s, err := first(); firstResult <- outcome{s, err} }()
				firstPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, query)
				go func() { s, err := second(); secondResult <- outcome{s, err} }()
				waitForBlockedMailQuery(t, ctx, persistence, firstPID, "pg_advisory_xact_lock")
				release()
				one, two := <-firstResult, <-secondResult
				require.NoError(t, one.err)
				if resetFirst {
					require.ErrorIs(t, two.err, store.ErrPasswordCredentialChanged)
					require.Nil(t, two.session)
				} else {
					require.NoError(t, two.err)
					require.NotNil(t, one.session)
					current, err := persistence.Session().Get(ctx, one.session.ID)
					require.NoError(t, err)
					require.True(t, current.RevokedAt.Valid)
					require.Equal(t, model.SessionRevocationPasswordReset, current.RevocationReason)
				}
			})
		}
	}
}

func TestPasswordProofResetFencesEachDesktopTransition(t *testing.T) {
	for _, stage := range []string{"authenticate", "issue", "exchange"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			persistence, institution, user, password := passwordProofFixture(t, ctx)
			transaction, handle, browserProof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
			_, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
			require.NoError(t, err)
			binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, browserProof, state)
			require.NoError(t, err)
			authenticate := func() error {
				_, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
					BindingHash: model.HashToken(binding), UserID: user.ID, AuthenticationMethod: "password",
					AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(),
					PasswordProof: store.PasswordCredentialProof{ID: password.ID, Revision: password.Revision},
					Capabilities:  store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
				})
				return err
			}
			code := model.NewCredentialToken()
			issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "issue")
			issue := func() error {
				_, err := persistence.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
					BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code), ExpectedUserID: user.ID,
					CodeLifetime: time.Minute, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
					AuditEventID: issueAudit.ID, AuditAt: model.NowUTC(),
				})
				return err
			}
			if stage != "authenticate" {
				err = authenticate()
				require.NoError(t, err)
			}
			if stage == "exchange" {
				err = issue()
				require.NoError(t, err)
			}
			_, err = persistence.UserToken().ConsumePasswordReset(ctx, passwordProofReset(t, ctx, persistence, institution, user))
			require.NoError(t, err)
			switch stage {
			case "authenticate":
				err = authenticate()
			case "issue":
				err = issue()
			case "exchange":
				audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
				_, err = persistence.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier, audit))
			}
			require.ErrorIs(t, err, store.ErrPasswordCredentialChanged, "%s accepted pre-reset proof", stage)
		})
	}
}

func TestPasswordProofDesktopRejectsRevokedSourceSession(t *testing.T) {
	ctx := context.Background()
	persistence, institution, user, _ := passwordProofFixture(t, ctx)
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	session, credentials, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10))
	require.NoError(t, err)
	var accessID model.SessionCredentialID
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialAccess {
			accessID = credential.ID
		}
	}
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	_, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	require.NoError(t, err)
	_, err = persistence.UserToken().ConsumePasswordReset(ctx, passwordProofReset(t, ctx, persistence, institution, user))
	require.NoError(t, err)
	_, err = persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(binding), UserID: user.ID, SourceSessionID: session.ID, SourceCredentialID: accessID,
		AuthenticationMethod: session.AuthenticationMethod, AuthenticationStrength: session.AuthenticationStrength,
		AuthenticatedAt: session.AuthenticatedAt, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	require.True(t, store.IsNotFound(err), "revoked source Session error")
}

func passwordProofFixture(t *testing.T, ctx context.Context) (*SQLStore, *model.Institution, *model.User, *model.PasswordCredential) {
	t.Helper()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "password-proof", DisplayName: "Password Proof"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "password-proof", Email: "password-proof@example.edu"})
	password, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"})
	require.NoError(t, err)
	return persistence, institution, user, password
}

func passwordProofReset(t *testing.T, ctx context.Context, persistence *SQLStore, institution *model.Institution, user *model.User) *store.PasswordResetCompletion {
	t.Helper()
	token, err := authenticationPolicyTestIssue(t, ctx, persistence, &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)},
		authenticationPolicyTestAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	return authenticationPolicyTestResetCompletion(t, user, token.TokenHash, "encoded-reset-password", model.NowUTC(), authenticationPolicyTestCompletionAudit(institution.ID.String()))
}

// A database trigger pauses the first writer after it holds the Session lock.
// The blocking graph proves the second operation waits on that same lock; no
// sleeps or scheduler assumptions decide which operation wins.
func pausePasswordProofWrite(t *testing.T, ctx context.Context, persistence *SQLStore, reset bool) (int, func()) {
	t.Helper()
	const key int64 = 8154700260905
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	var pid int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key)
	require.NoError(t, err)
	locked := true
	release := func() {
		if locked {
			_, err := controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
			assert.NoError(t, err)
			locked = false
		}
	}
	table, event := "sessions", "INSERT"
	if reset {
		table, event = "password_credentials", "UPDATE"
	}
	t.Cleanup(func() {
		release()
		_ = controller.Close()
		_, err := persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_password_proof ON `+table+`; DROP FUNCTION IF EXISTS proctor_test_pause_password_proof()`)
		assert.NoError(t, err)
	})
	_, err = persistence.GetMaster().Exec(ctx, `CREATE FUNCTION proctor_test_pause_password_proof() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(8154700260905); RETURN NEW; END $$;
		CREATE TRIGGER proctor_test_pause_password_proof BEFORE `+event+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_password_proof()`)
	require.NoError(t, err)
	return pid, release
}
