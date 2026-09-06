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
	if err != nil {
		t.Fatal(err)
	}
	if err = persistence.PasswordCredential().Rehash(ctx, rehash); !errors.Is(err, store.ErrPasswordCredentialChanged) {
		t.Fatalf("stale rehash error = %v", err)
	}
	current, err := persistence.PasswordCredential().GetByUser(ctx, user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if current.PasswordHash != completion.PasswordHash || current.Revision != password.Revision+1 ||
		!current.PasswordChangedAt.Equal(reset.PasswordCredential.PasswordChangedAt) {
		t.Fatal("stale rehash changed the reset credential or its lifecycle metadata")
	}
	if _, _, err = persistence.Session().Save(ctx, creation); !errors.Is(err, store.ErrPasswordCredentialChanged) {
		t.Fatalf("old proof after reset error = %v", err)
	}
}

func TestPasswordProofRehashPreservesOutstandingProof(t *testing.T) {
	ctx := context.Background()
	persistence, _, user, password := passwordProofFixture(t, ctx)
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	creation := sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10)
	if err := persistence.PasswordCredential().Rehash(ctx, &store.PasswordCredentialRehash{
		ID: password.ID, UserID: user.ID, ExpectedHash: password.PasswordHash,
		ExpectedRevision: password.Revision, PasswordHash: "upgraded-same-password",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistence.Session().Save(ctx, creation); err != nil {
		t.Fatalf("rehash invalidated outstanding proof: %v", err)
	}
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
	if err := <-resetResult; err != nil {
		t.Fatal(err)
	}
	if err := <-rehashResult; !errors.Is(err, store.ErrPasswordCredentialChanged) {
		t.Fatalf("concurrent rehash error = %v", err)
	}
	current, err := persistence.PasswordCredential().GetByUser(ctx, user.ID.String())
	if err != nil || current.PasswordHash != completion.PasswordHash || current.Revision != password.Revision+1 {
		t.Fatalf("concurrent rehash replaced the reset credential: %v", err)
	}
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
						result, _, err := persistence.Session().Save(ctx, creation)
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
				if one.err != nil {
					t.Fatalf("first operation: %v", one.err)
				}
				if resetFirst {
					if !errors.Is(two.err, store.ErrPasswordCredentialChanged) || two.session != nil {
						t.Fatalf("creation escaped earlier reset: %v", two.err)
					}
				} else {
					if two.err != nil || one.session == nil {
						t.Fatalf("reset following creation: %v", two.err)
					}
					current, err := persistence.Session().Get(ctx, one.session.ID.String())
					if err != nil || !current.RevokedAt.Valid || current.RevocationReason != model.SessionRevocationPasswordReset {
						t.Fatalf("created Session escaped reset revocation: %v", err)
					}
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
			if _, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
				t.Fatal(err)
			}
			binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, browserProof, state)
			if err != nil {
				t.Fatal(err)
			}
			authenticate := func() error {
				_, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
					BindingHash: model.HashToken(binding), UserID: user.ID, AuthenticationMethod: "password",
					AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(),
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
					AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
				})
				return err
			}
			if stage != "authenticate" {
				if err = authenticate(); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "exchange" {
				if err = issue(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = persistence.UserToken().ConsumePasswordReset(ctx, passwordProofReset(t, ctx, persistence, institution, user)); err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "authenticate":
				err = authenticate()
			case "issue":
				err = issue()
			case "exchange":
				audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
				_, err = persistence.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier, audit))
			}
			if !errors.Is(err, store.ErrPasswordCredentialChanged) {
				t.Fatalf("%s accepted pre-reset proof: %v", stage, err)
			}
		})
	}
}

func TestPasswordProofDesktopRejectsRevokedSourceSession(t *testing.T) {
	ctx := context.Background()
	persistence, institution, user, _ := passwordProofFixture(t, ctx)
	session, credentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	session, credentials, err := persistence.Session().Save(ctx, sessionCreationForSQLTest(t, ctx, persistence, session, credentials, 10))
	if err != nil {
		t.Fatal(err)
	}
	var accessID model.SessionCredentialID
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialAccess {
			accessID = credential.ID
		}
	}
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.UserToken().ConsumePasswordReset(ctx, passwordProofReset(t, ctx, persistence, institution, user)); err != nil {
		t.Fatal(err)
	}
	_, err = persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(binding), UserID: user.ID, SourceSessionID: session.ID, SourceCredentialID: accessID,
		AuthenticationMethod: session.AuthenticationMethod, AuthenticationStrength: session.AuthenticationStrength,
		AuthenticatedAt: session.AuthenticatedAt.UnixMilli(), Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	if !store.IsNotFound(err) {
		t.Fatalf("revoked source Session error = %v", err)
	}
}

func passwordProofFixture(t *testing.T, ctx context.Context) (*SQLStore, *model.Institution, *model.User, *model.PasswordCredential) {
	t.Helper()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "password-proof", DisplayName: "Password Proof"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "password-proof", Email: "password-proof@example.edu"})
	password, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"})
	if err != nil {
		t.Fatal(err)
	}
	return persistence, institution, user, password
}

func passwordProofReset(t *testing.T, ctx context.Context, persistence *SQLStore, institution *model.Institution, user *model.User) *store.PasswordResetCompletion {
	t.Helper()
	token, err := authenticationPolicyTestIssue(t, ctx, persistence, &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)},
		authenticationPolicyTestAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	return authenticationPolicyTestResetCompletion(t, user, token.TokenHash, "encoded-reset-password", model.GetMillis(), authenticationPolicyTestCompletionAudit(institution.ID.String()))
}

// A database trigger pauses the first writer after it holds the Session lock.
// The blocking graph proves the second operation waits on that same lock; no
// sleeps or scheduler assumptions decide which operation wins.
func pausePasswordProofWrite(t *testing.T, ctx context.Context, persistence *SQLStore, reset bool) (int, func()) {
	t.Helper()
	const key int64 = 8154700260905
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	locked := true
	release := func() {
		if locked {
			if _, err := controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key); err != nil {
				t.Error(err)
			}
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
		if err != nil {
			t.Error(err)
		}
	})
	if _, err = persistence.GetMaster().Exec(ctx, `CREATE FUNCTION proctor_test_pause_password_proof() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(8154700260905); RETURN NEW; END $$;
		CREATE TRIGGER proctor_test_pause_password_proof BEFORE `+event+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_password_proof()`); err != nil {
		t.Fatal(err)
	}
	return pid, release
}
