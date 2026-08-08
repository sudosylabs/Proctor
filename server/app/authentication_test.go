// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type authenticationCacheFake struct {
	mu      sync.Mutex
	values  map[string][]byte
	counters map[string]int64
}

func newAuthenticationCacheFake() *authenticationCacheFake {
	return &authenticationCacheFake{
		values:   make(map[string][]byte),
		counters: make(map[string]int64),
	}
}

func (c *authenticationCacheFake) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	if !ok {
		return nil, errAuthenticationCacheMiss
	}
	return append([]byte(nil), value...), nil
}

func (c *authenticationCacheFake) SetAlways(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = append([]byte(nil), value...)
	return nil
}

func (c *authenticationCacheFake) SetIfAbsent(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.values[key]; ok {
		return errAuthenticationCacheNotStored
	}
	c.values[key] = append([]byte(nil), value...)
	return nil
}

func (c *authenticationCacheFake) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
	return nil
}

func (c *authenticationCacheFake) Add(_ context.Context, key string, delta int64, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters[key] += delta
	return c.counters[key], nil
}

type authenticationStoreFake struct {
	users                map[string]*model.User
	usersByUsername      map[string]*model.User
	usersByEmail         map[string]*model.User
	passwords            map[string]*model.PasswordCredential
	sessions             map[string]*model.Session
	accessByHash         map[string]*model.SessionCredential
	sessionByCredential  map[string]*model.Session
	saveErr              error
	maximumPerUser       int
}

func newAuthenticationStoreFake() *authenticationStoreFake {
	return &authenticationStoreFake{
		users:               make(map[string]*model.User),
		usersByUsername:     make(map[string]*model.User),
		usersByEmail:        make(map[string]*model.User),
		passwords:           make(map[string]*model.PasswordCredential),
		sessions:            make(map[string]*model.Session),
		accessByHash:        make(map[string]*model.SessionCredential),
		sessionByCredential: make(map[string]*model.Session),
		maximumPerUser:      10,
	}
}

func (s *authenticationStoreFake) User() store.UserStore                         { return authenticationUserStore{s} }
func (s *authenticationStoreFake) PasswordCredential() store.PasswordCredentialStore {
	return authenticationPasswordStore{s}
}
func (s *authenticationStoreFake) Session() store.SessionStore { return authenticationSessionStore{s} }
func (s *authenticationStoreFake) SessionCredential() store.SessionCredentialStore {
	return authenticationSessionCredentialStore{s}
}
func (s *authenticationStoreFake) MFA() store.MFAStore { return nil }
func (s *authenticationStoreFake) PersonalAccessToken() store.PersonalAccessTokenStore {
	return nil
}
func (s *authenticationStoreFake) Institution() store.InstitutionStore                   { return nil }
func (s *authenticationStoreFake) AcademicUnit() store.AcademicUnitStore                 { return nil }
func (s *authenticationStoreFake) Programme() store.ProgrammeStore                       { return nil }
func (s *authenticationStoreFake) ProgrammeLevel() store.ProgrammeLevelStore             { return nil }
func (s *authenticationStoreFake) AcademicPeriod() store.AcademicPeriodStore             { return nil }
func (s *authenticationStoreFake) Class() store.ClassStore                               { return nil }
func (s *authenticationStoreFake) ExternalIdentity() store.ExternalIdentityStore         { return nil }
func (s *authenticationStoreFake) ExternalLoginState() store.ExternalLoginStateStore     { return nil }
func (s *authenticationStoreFake) UserToken() store.UserTokenStore                       { return nil }
func (s *authenticationStoreFake) Affiliation() store.AffiliationStore                   { return nil }
func (s *authenticationStoreFake) AcademicUnitMember() store.AcademicUnitMemberStore     { return nil }
func (s *authenticationStoreFake) ClassMember() store.ClassMemberStore                   { return nil }
func (s *authenticationStoreFake) Role() store.RoleStore                                 { return nil }
func (s *authenticationStoreFake) RoleBinding() store.RoleBindingStore                   { return nil }
func (s *authenticationStoreFake) Audit() store.AuditStore                               { return nil }
func (s *authenticationStoreFake) Installation() store.InstallationStore                 { return nil }
func (s *authenticationStoreFake) ClusterDiscovery() store.ClusterDiscoveryStore         { return nil }
func (s *authenticationStoreFake) Ping(context.Context) error                            { return nil }
func (s *authenticationStoreFake) GetDBSchemaVersion(context.Context) (int, error)       { return 0, nil }
func (s *authenticationStoreFake) GetLocalSchemaVersion() (int, error)                   { return 0, nil }
func (s *authenticationStoreFake) ValidateSchema(context.Context) error                  { return nil }
func (s *authenticationStoreFake) Close() error                                          { return nil }

type authenticationUserStore struct{ root *authenticationStoreFake }

func (s authenticationUserStore) SaveWithPassword(
	_ context.Context,
	user *model.User,
	credential *model.PasswordCredential,
) (*model.User, *model.PasswordCredential, error) {
	cloned := *user
	if cloned.ID.IsZero() {
		cloned.ID = model.NewUserID()
	}
	now := time.Now().UnixMilli()
	cloned.CreatedAt = model.TimeFromMillis(now)
	cloned.UpdatedAt = model.TimeFromMillis(now)
	s.root.users[cloned.ID.String()] = &cloned
	s.root.usersByUsername[strings.ToLower(cloned.Username)] = &cloned
	s.root.usersByEmail[strings.ToLower(cloned.Email)] = &cloned
	pass := *credential
	pass.ID = model.NewPasswordCredentialID()
	pass.UserID = cloned.ID
	pass.CreatedAt = model.TimeFromMillis(now)
	pass.UpdatedAt = model.TimeFromMillis(now)
	pass.PasswordChangedAt = model.TimeFromMillis(now)
	s.root.passwords[cloned.ID.String()] = &pass
	return &cloned, &pass, nil
}

func (s authenticationUserStore) Get(_ context.Context, id string) (*model.User, error) {
	user, ok := s.root.users[id]
	if !ok {
		return nil, store.NewErrNotFound("user", id)
	}
	cloned := *user
	return &cloned, nil
}

func (s authenticationUserStore) GetByUsername(_ context.Context, username string) (*model.User, error) {
	user, ok := s.root.usersByUsername[strings.ToLower(username)]
	if !ok {
		return nil, store.NewErrNotFound("user", username)
	}
	cloned := *user
	return &cloned, nil
}

func (s authenticationUserStore) GetByEmail(_ context.Context, email string) (*model.User, error) {
	user, ok := s.root.usersByEmail[strings.ToLower(email)]
	if !ok {
		return nil, store.NewErrNotFound("user", email)
	}
	cloned := *user
	return &cloned, nil
}

// Remaining UserStore methods are unused by the focused authentication paths.
func (authenticationUserStore) Save(context.Context, *model.User) (*model.User, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) Update(context.Context, *model.User) (*model.User, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) List(context.Context, store.UserListOptions) ([]*model.User, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) SetDisabledWithAudit(context.Context, *store.UserDisabledStateChange) (*store.UserDisabledStateResult, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) UpdateProfileWithAudit(context.Context, *store.UserProfileUpdate) (*model.User, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) UpdateLastLogin(context.Context, string, int64) error {
	return errors.New("unused")
}

type authenticationPasswordStore struct{ root *authenticationStoreFake }

func (s authenticationPasswordStore) GetByUser(_ context.Context, userID string) (*model.PasswordCredential, error) {
	credential, ok := s.root.passwords[userID]
	if !ok {
		return nil, store.NewErrNotFound("password_credential", userID)
	}
	cloned := *credential
	return &cloned, nil
}

func (s authenticationPasswordStore) Update(_ context.Context, credential *model.PasswordCredential) (*model.PasswordCredential, error) {
	cloned := *credential
	s.root.passwords[credential.UserID.String()] = &cloned
	return &cloned, nil
}

func (authenticationPasswordStore) Save(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error) {
	return nil, errors.New("unused")
}

type authenticationSessionStore struct{ root *authenticationStoreFake }

func (s authenticationSessionStore) Save(
	_ context.Context,
	session *model.Session,
	credentials []*model.SessionCredential,
	maximumPerUser int,
) (*model.Session, []*model.SessionCredential, error) {
	if s.root.saveErr != nil {
		return nil, nil, s.root.saveErr
	}
	if maximumPerUser > 0 {
		count := 0
		for _, existing := range s.root.sessions {
			if existing.UserID == session.UserID && !existing.RevokedAt.Valid {
				count++
			}
		}
		if count >= maximumPerUser {
			return nil, nil, store.NewErrConflict("session", "sessions_maximum_per_user", errors.New("max"))
		}
	}
	cloned := *session
	now := model.NowUTC()
	if cloned.ID.IsZero() {
		cloned.PrepareCreate(model.NewSessionID(), now)
	} else {
		cloned.PrepareUpdate(now)
	}
	s.root.sessions[cloned.ID.String()] = &cloned
	savedCredentials := make([]*model.SessionCredential, 0, len(credentials))
	for _, credential := range credentials {
		item := *credential
		item.SessionID = cloned.ID
		if item.ID.IsZero() {
			item.PrepareCreate(model.NewSessionCredentialID(), now)
		}
		savedCredentials = append(savedCredentials, &item)
		if item.Kind == model.SessionCredentialAccess {
			s.root.accessByHash[item.TokenHash] = &item
			s.root.sessionByCredential[item.TokenHash] = &cloned
		}
	}
	return &cloned, savedCredentials, nil
}

func (s authenticationSessionStore) UpdateActivity(_ context.Context, sessionID string, lastActivityAt, idleExpiresAt int64) error {
	session, ok := s.root.sessions[sessionID]
	if !ok {
		return store.NewErrNotFound("session", sessionID)
	}
	at := model.TimeFromMillis(lastActivityAt)
	session.LastActivityAt = at
	session.IdleExpiresAt = model.TimeFromMillis(idleExpiresAt)
	session.UpdatedAt = at
	return nil
}

func (s authenticationSessionStore) Revoke(_ context.Context, sessionID, _ string, revokedAt int64, reason string) ([]string, error) {
	session, ok := s.root.sessions[sessionID]
	if !ok {
		return nil, store.NewErrNotFound("session", sessionID)
	}
	at := model.TimeFromMillis(revokedAt)
	session.RevokedAt = model.OptionalTimeFrom(at)
	session.RevocationReason = reason
	if session.UpdatedAt.Before(at) {
		session.UpdatedAt = at
	}
	var hashes []string
	for hash, credential := range s.root.accessByHash {
		if credential.SessionID.String() == sessionID {
			hashes = append(hashes, hash)
			delete(s.root.accessByHash, hash)
			delete(s.root.sessionByCredential, hash)
		}
	}
	return hashes, nil
}

func (authenticationSessionStore) Get(context.Context, string) (*model.Session, error) {
	return nil, errors.New("unused")
}
func (authenticationSessionStore) ListByUser(context.Context, string) ([]*model.Session, error) {
	return nil, errors.New("unused")
}
func (authenticationSessionStore) ListActiveByUser(context.Context, string, int64) ([]*model.Session, error) {
	return nil, errors.New("unused")
}
func (authenticationSessionStore) RevokeWithAudit(context.Context, *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	return nil, errors.New("unused")
}
func (authenticationSessionStore) RevokeAllForUser(context.Context, string, int64, string) ([]*model.Session, []string, error) {
	return nil, nil, errors.New("unused")
}
func (authenticationSessionStore) RevokeAllForUserWithAudit(context.Context, *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error) {
	return nil, errors.New("unused")
}

type authenticationSessionCredentialStore struct{ root *authenticationStoreFake }

func (s authenticationSessionCredentialStore) GetSessionByTokenHash(
	_ context.Context,
	tokenHash string,
	kind model.SessionCredentialKind,
) (*model.SessionCredential, *model.Session, error) {
	if kind != model.SessionCredentialAccess {
		return nil, nil, store.NewErrNotFound("session_credential", tokenHash)
	}
	credential, ok := s.root.accessByHash[tokenHash]
	if !ok {
		return nil, nil, store.NewErrNotFound("session_credential", tokenHash)
	}
	session, ok := s.root.sessionByCredential[tokenHash]
	if !ok {
		return nil, nil, store.NewErrNotFound("session", tokenHash)
	}
	credentialClone := *credential
	sessionClone := *session
	return &credentialClone, &sessionClone, nil
}

func (authenticationSessionCredentialStore) RotateRefresh(
	context.Context,
	string,
	*model.SessionCredential,
	*model.SessionCredential,
	int64,
	int64,
) (*store.SessionRotation, error) {
	return nil, errors.New("unused")
}

func newTestAuthenticationService(t *testing.T, persistence *authenticationStoreFake) *AuthenticationService {
	t.Helper()
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings)
	if err != nil {
		t.Fatal(err)
	}
	return newAuthenticationService(
		persistence,
		newAuthenticationCacheFake(),
		hasher,
		nil,
		SessionPolicy{
			AccessTTL:              time.Hour,
			RefreshTTL:             24 * time.Hour,
			IdleTTL:                2 * time.Hour,
			AbsoluteTTL:            7 * 24 * time.Hour,
			ActivityUpdateInterval: time.Minute,
			MaximumPerUser:         10,
		},
		LoginRateLimitPolicy{
			Window:                time.Minute,
			MaximumAttempts:       20,
			MaximumSourceAttempts: 100,
		},
		PersonalAccessTokenPolicy{LastUsedUpdateInterval: time.Hour},
		nil,
		time.Now,
	)
}

func TestLoginReturnsTransportNeutralInvalidCredentials(t *testing.T) {
	t.Parallel()

	service := newTestAuthenticationService(t, newAuthenticationStoreFake())
	result, err := service.login(context.Background(), LoginCommand{
		LoginID:    "",
		Password:   "present",
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1:1",
	})
	if result != nil {
		t.Fatalf("result = %#v", result)
	}
	failure, ok := As(err)
	if !ok {
		t.Fatalf("expected *Error, got %T %v", err, err)
	}
	if failure.Code() != "authentication.invalid_credentials" {
		t.Fatalf("code = %q", failure.Code())
	}
	type httpStatuser interface{ HTTPStatus() int }
	var asHTTP httpStatuser
	if errors.As(err, &asHTTP) {
		t.Fatal("login error must not expose HTTP status")
	}
}

func TestLoginRejectsUnknownUserWithGenericFailure(t *testing.T) {
	t.Parallel()

	service := newTestAuthenticationService(t, newAuthenticationStoreFake())
	_, err := service.login(context.Background(), LoginCommand{
		LoginID:    "missing@example.edu",
		Password:   "CorrectHorseBatteryStaple1!",
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1:2",
	})
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.invalid_credentials" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginAndAuthenticateAccessConstructPrincipal(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)

	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User:     &model.User{Username: "auth-user", Email: "auth-user@example.edu"},
		Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.login(context.Background(), LoginCommand{
		LoginID:    user.Email,
		Password:   "CorrectHorseBatteryStaple1!",
		ClientType: model.SessionClientCLI,
		DeviceID:   "device-1",
		Source:     "127.0.0.1:3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session == nil || result.Tokens == nil || result.Tokens.AccessToken == "" {
		t.Fatalf("incomplete result %#v", result)
	}

	principal, err := service.authenticateAccess(context.Background(), result.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserId != user.ID.String() || principal.SessionId != result.Session.ID.String() {
		t.Fatalf("principal = %#v", principal)
	}
	if principal.CredentialType != model.CredentialSessionAccess {
		t.Fatalf("credential type = %q", principal.CredentialType)
	}
	if principal.AuthenticationMethod != "password" {
		t.Fatalf("method = %q", principal.AuthenticationMethod)
	}
}

func TestAuthenticateAccessRejectsUnknownToken(t *testing.T) {
	t.Parallel()

	service := newTestAuthenticationService(t, newAuthenticationStoreFake())
	raw := model.NewCredentialToken()
	_, err := service.authenticateAccess(context.Background(), raw)
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.invalid_token" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginRejectsInvalidClientType(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User:     &model.User{Username: "client-type-user", Email: "client-type@example.edu"},
		Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.login(context.Background(), LoginCommand{
		LoginID:    user.Username,
		Password:   "CorrectHorseBatteryStaple1!",
		ClientType: model.SessionClientType("pager"),
		Source:     "127.0.0.1:4",
	})
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.client_type.invalid" {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginRateLimitsRepeatedFailures(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	service.loginRateLimit.MaximumAttempts = 2
	service.loginRateLimit.MaximumSourceAttempts = 100

	_, _ = service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User:     &model.User{Username: "rate-user", Email: "rate-user@example.edu"},
		Password: "CorrectHorseBatteryStaple1!",
	})

	for attempt := 0; attempt < 2; attempt++ {
		_, err := service.login(context.Background(), LoginCommand{
			LoginID:    "rate-user",
			Password:   "wrong-password-value",
			ClientType: model.SessionClientCLI,
			Source:     "10.0.0.8:9",
		})
		failure, ok := As(err)
		if !ok || failure.Code() != "authentication.invalid_credentials" {
			t.Fatalf("attempt %d: err = %v", attempt, err)
		}
	}
	_, err := service.login(context.Background(), LoginCommand{
		LoginID:    "rate-user",
		Password:   "wrong-password-value",
		ClientType: model.SessionClientCLI,
		Source:     "10.0.0.8:9",
	})
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.rate_limited" {
		t.Fatalf("err = %v", err)
	}
}
