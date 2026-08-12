// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type authenticationCacheFake struct {
	mu       sync.Mutex
	values   map[string][]byte
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
	users               map[string]*model.User
	usersByUsername     map[string]*model.User
	usersByEmail        map[string]*model.User
	passwords           map[string]*model.PasswordCredential
	sessions            map[string]*model.Session
	accessByHash        map[string]*model.SessionCredential
	sessionByCredential map[string]*model.Session
	saveErr             error
	maximumPerUser      int
	createdJob          *model.Job
	rotation            *store.SessionRotation
	rotatedAccess       *model.SessionCredential
	rotatedRefresh      *model.SessionCredential
	rotatedAt           int64
	rotatedIdleExpiry   int64
}

func (s *authenticationStoreFake) File() store.FileStore { return nil }
func (s *authenticationStoreFake) Job() store.JobStore   { return nil }

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

func (s *authenticationStoreFake) User() store.UserStore { return authenticationUserStore{s} }
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
func (s *authenticationStoreFake) Institution() store.InstitutionStore               { return nil }
func (s *authenticationStoreFake) AcademicUnit() store.AcademicUnitStore             { return nil }
func (s *authenticationStoreFake) Programme() store.ProgrammeStore                   { return nil }
func (s *authenticationStoreFake) ProgrammeLevel() store.ProgrammeLevelStore         { return nil }
func (s *authenticationStoreFake) AcademicPeriod() store.AcademicPeriodStore         { return nil }
func (s *authenticationStoreFake) Class() store.ClassStore                           { return nil }
func (s *authenticationStoreFake) ExternalIdentity() store.ExternalIdentityStore     { return nil }
func (s *authenticationStoreFake) ExternalLoginState() store.ExternalLoginStateStore { return nil }
func (s *authenticationStoreFake) UserToken() store.UserTokenStore                   { return nil }
func (s *authenticationStoreFake) Affiliation() store.AffiliationStore               { return nil }
func (s *authenticationStoreFake) AcademicUnitMember() store.AcademicUnitMemberStore { return nil }
func (s *authenticationStoreFake) ClassMember() store.ClassMemberStore               { return nil }
func (s *authenticationStoreFake) Role() store.RoleStore                             { return nil }
func (s *authenticationStoreFake) RoleBinding() store.RoleBindingStore               { return nil }
func (s *authenticationStoreFake) Audit() store.AuditStore                           { return nil }
func (s *authenticationStoreFake) Installation() store.InstallationStore             { return nil }
func (s *authenticationStoreFake) ClusterDiscovery() store.ClusterDiscoveryStore     { return nil }
func (s *authenticationStoreFake) Ping(context.Context) error                        { return nil }
func (s *authenticationStoreFake) GetDBSchemaVersion(context.Context) (int, error)   { return 0, nil }
func (s *authenticationStoreFake) GetLocalSchemaVersion() (int, error)               { return 0, nil }
func (s *authenticationStoreFake) ValidateSchema(context.Context) error              { return nil }
func (s *authenticationStoreFake) Close() error                                      { return nil }

type authenticationUserStore struct{ root *authenticationStoreFake }

func (s authenticationUserStore) Create(
	_ context.Context,
	input *store.UserCreation,
) (*store.UserCreationResult, error) {
	cloned := *input.User
	s.root.users[cloned.ID.String()] = &cloned
	s.root.usersByUsername[strings.ToLower(cloned.Username)] = &cloned
	s.root.usersByEmail[strings.ToLower(cloned.Email)] = &cloned
	pass := *input.PasswordCredential
	s.root.passwords[cloned.ID.String()] = &pass
	job := *input.DefaultProfilePictureJob
	s.root.createdJob = &job
	return &store.UserCreationResult{User: &cloned, PasswordCredential: &pass}, nil
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
	now := cloned.LastActivityAt
	if now.IsZero() {
		now = model.NowUTC()
	}
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

func (s authenticationSessionStore) Get(_ context.Context, id string) (*model.Session, error) {
	session, ok := s.root.sessions[id]
	if !ok {
		return nil, store.NewErrNotFound("session", id)
	}
	cloned := *session
	return &cloned, nil
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

func (s authenticationSessionCredentialStore) RotateRefresh(
	_ context.Context,
	_ string,
	access *model.SessionCredential,
	refresh *model.SessionCredential,
	at int64,
	idleExpiry int64,
) (*store.SessionRotation, error) {
	s.root.rotatedAccess = access
	s.root.rotatedRefresh = refresh
	s.root.rotatedAt = at
	s.root.rotatedIdleExpiry = idleExpiry
	if s.root.rotation == nil {
		return nil, errors.New("unused")
	}
	result := *s.root.rotation
	if !result.ReplayDetected {
		result.AccessCredential = access
		result.RefreshCredential = refresh
	}
	return &result, nil
}

func newTestAuthenticationService(t *testing.T, persistence *authenticationStoreFake) *authenticationService {
	return newTestAuthenticationServiceWithCache(t, persistence, newAuthenticationCacheFake())
}

func newTestAuthenticationServiceWithCache(
	t *testing.T,
	persistence *authenticationStoreFake,
	cache authenticationCache,
) *authenticationService {
	return newTestAuthenticationServiceWithRuntime(
		t, persistence, cache, model.NewCredentialToken, time.Now,
	)
}

func newTestAuthenticationServiceWithRuntime(
	t *testing.T,
	persistence *authenticationStoreFake,
	cache authenticationCache,
	newCredential func() string,
	now func() time.Time,
) *authenticationService {
	return newTestAuthenticationServiceWithPorts(
		t,
		persistence,
		cache,
		discardAuthenticationMFAVerifier{},
		discardAuthenticationPATResolver{},
		newCredential,
		now,
	)
}

func newTestAuthenticationServiceWithPorts(
	t *testing.T,
	persistence *authenticationStoreFake,
	cache authenticationCache,
	mfa authenticationMFAVerifier,
	personalTokens authenticationPATResolver,
	newCredential func() string,
	now func() time.Time,
) *authenticationService {
	return newTestAuthenticationServiceWithEffects(
		t, persistence, cache, discardAuthenticationSecurityEffects{}, mfa,
		personalTokens, newCredential, now,
	)
}

func newTestAuthenticationServiceWithEffects(
	t *testing.T,
	persistence *authenticationStoreFake,
	cache authenticationCache,
	effects authenticationSecurityEffects,
	mfa authenticationMFAVerifier,
	personalTokens authenticationPATResolver,
	newCredential func() string,
	now func() time.Time,
) *authenticationService {
	t.Helper()
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newAuthenticationService(
		persistence.User(),
		persistence.PasswordCredential(),
		persistence.Session(),
		persistence.SessionCredential(),
		cache,
		effects,
		hasher,
		mfa,
		personalTokens,
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
		&securityEffectsDiagnosticsFake{},
		newCredential,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAuthenticationValidatesLongLivedSessionPrincipal(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID()}
	session := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID,
		ExpiresAt: at.Add(time.Hour), IdleExpiresAt: at.Add(30 * time.Minute),
	}
	persistence := newAuthenticationStoreFake()
	persistence.users[user.ID.String()] = user
	persistence.sessions[session.ID.String()] = session
	service := newTestAuthenticationServiceWithPorts(
		t, persistence, newAuthenticationCacheFake(),
		discardAuthenticationMFAVerifier{}, discardAuthenticationPATResolver{},
		model.NewCredentialToken, func() time.Time { return at },
	)
	principal := model.Principal{
		UserID: user.ID, SessionID: session.ID,
		CredentialID:         model.PrincipalCredentialID(model.NewId()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: at.Add(-time.Minute), ClientType: model.SessionClientWeb,
	}
	if err := service.ValidatePrincipal(context.Background(), principal); err != nil {
		t.Fatalf("valid principal rejected: %v", err)
	}
	session.UserID = model.NewUserID()
	if err := service.ValidatePrincipal(context.Background(), principal); !Is(err, "authentication.invalid_token") {
		t.Fatalf("mismatched session error = %v", err)
	}
}

func TestAuthenticationRefreshUsesControlledRuntimeAndPreservesReplayEffects(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID(), CreatedAt: at, UpdatedAt: at, Revision: 1}
	session := &model.Session{ID: model.NewSessionID(), UserID: user.ID}
	access := base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	refresh := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyzABCDEF"))
	presentedRefresh := base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	for _, test := range []struct {
		name       string
		replay     bool
		wantCode   string
		wantEffect string
	}{
		{name: "success", wantEffect: "cache"},
		{name: "replay", replay: true, wantCode: "authentication.invalid_token", wantEffect: "sessions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			persistence.users[user.ID.String()] = user
			persistence.rotation = &store.SessionRotation{
				Session: session, ReplayDetected: test.replay,
				RevokedAccessHashes: []string{"old-access-hash"},
			}
			effects := &authenticationEffectsRecorder{}
			credentials := []string{access, refresh}
			next := 0
			service := newTestAuthenticationServiceWithEffects(
				t, persistence, newAuthenticationCacheFake(), effects,
				discardAuthenticationMFAVerifier{}, discardAuthenticationPATResolver{},
				func() string { value := credentials[next]; next++; return value },
				func() time.Time { return at },
			)

			_, tokens, err := service.refresh(context.Background(), presentedRefresh)
			if test.wantCode != "" {
				if !Is(err, test.wantCode) {
					t.Fatalf("refresh error = %v, want %s", err, test.wantCode)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if tokens.AccessToken != access || tokens.RefreshToken != refresh {
					t.Fatalf("tokens = %#v, want controlled credentials", tokens)
				}
			}
			if persistence.rotatedAt != at.UnixMilli() ||
				persistence.rotatedIdleExpiry != at.Add(2*time.Hour).UnixMilli() {
				t.Fatalf("rotation time = %d idle = %d", persistence.rotatedAt, persistence.rotatedIdleExpiry)
			}
			if effects.last != test.wantEffect {
				t.Fatalf("effect = %q, want %q", effects.last, test.wantEffect)
			}
		})
	}
}

type authenticationEffectsRecorder struct{ last string }

func (e *authenticationEffectsRecorder) AuthenticationCacheInvalidated(context.Context, string, []string) {
	e.last = "cache"
}

func (e *authenticationEffectsRecorder) SessionsRevoked(context.Context, string, []string, []string) {
	e.last = "sessions"
}

func TestAuthenticationUsesControlledClockAndCredentialGenerator(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	credentials := []string{
		base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789012345678901")),
		base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyzABCDEF")),
	}
	next := 0
	service := newTestAuthenticationServiceWithRuntime(
		t,
		newAuthenticationStoreFake(),
		newAuthenticationCacheFake(),
		func() string {
			credential := credentials[next]
			next++
			return credential
		},
		func() time.Time { return at },
	)
	user := &model.User{ID: model.NewUserID(), CreatedAt: at, UpdatedAt: at, Revision: 1}
	resultSession, tokens, err := service.createSession(
		context.Background(),
		sessionIssuance{
			User: user, ClientType: model.SessionClientCLI,
			DeviceID: "device", DeviceName: "Device",
			AuthenticationMethod:   "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt:        at.UnixMilli(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != credentials[0] || tokens.RefreshToken != credentials[1] {
		t.Fatalf("tokens = %#v, want controlled credentials", tokens)
	}
	if resultSession.LastActivityAt != at ||
		!tokens.AccessExpiresAt.Equal(at.Add(time.Hour)) ||
		!tokens.RefreshExpiresAt.Equal(at.Add(24*time.Hour)) {
		t.Fatalf("session/tokens did not use controlled clock: session=%#v tokens=%#v", resultSession, tokens)
	}
}

func TestAuthenticationConsumesMFAAndPATThroughBehavioralPorts(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	persistence := newAuthenticationStoreFake()
	mfa := &authenticationMFAVerifierFake{
		strength:    model.AuthenticationMultiFactor,
		completedAt: at.UnixMilli(),
	}
	patPrincipal := &model.Principal{
		UserID:               model.NewUserID(),
		CredentialID:         model.PrincipalCredentialID(model.NewPersonalAccessTokenID()),
		CredentialType:       model.CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token",
		ClientType:           model.SessionClientCLI,
		CredentialScopes:     []string{string(model.ActionUserView)},
	}
	pat := &authenticationPATResolverFake{principal: patPrincipal}
	service := newTestAuthenticationServiceWithPorts(
		t,
		persistence,
		newAuthenticationCacheFake(),
		mfa,
		pat,
		model.NewCredentialToken,
		func() time.Time { return at },
	)
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User:     &model.User{Username: "behavior-port-user", Email: "behavior-port@example.edu"},
		Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.login(context.Background(), LoginCommand{
		LoginID:    user.Username,
		Password:   "CorrectHorseBatteryStaple1!",
		ClientType: model.SessionClientCLI,
		MFACode:    "123456",
		Source:     "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mfa.calls != 1 || login.Session.AuthenticationStrength != model.AuthenticationMultiFactor {
		t.Fatalf("MFA calls=%d session=%#v", mfa.calls, login.Session)
	}

	app := &App{authentication: service}
	resolved, err := app.AuthenticateBearer(context.Background(), model.NewCredentialToken())
	if err != nil {
		t.Fatal(err)
	}
	if pat.calls != 1 || resolved.UserID != patPrincipal.UserID {
		t.Fatalf("PAT calls=%d principal=%#v", pat.calls, resolved)
	}
}

type authenticationMFAVerifierFake struct {
	calls       int
	strength    model.AuthenticationStrength
	completedAt int64
}

func (f *authenticationMFAVerifierFake) VerifyLogin(
	context.Context,
	string,
	string,
	time.Time,
) (model.AuthenticationStrength, int64, error) {
	f.calls++
	return f.strength, f.completedAt, nil
}

type authenticationPATResolverFake struct {
	calls     int
	principal *model.Principal
}

func (f *authenticationPATResolverFake) ResolveBearer(
	context.Context,
	string,
	time.Time,
) (*model.Principal, error) {
	f.calls++
	return f.principal, nil
}

type discardAuthenticationMFAVerifier struct{}

func (discardAuthenticationMFAVerifier) VerifyLogin(
	context.Context,
	string,
	string,
	time.Time,
) (model.AuthenticationStrength, int64, error) {
	return model.AuthenticationSingleFactor, 0, nil
}

type discardAuthenticationPATResolver struct{}

func (discardAuthenticationPATResolver) ResolveBearer(
	context.Context,
	string,
	time.Time,
) (*model.Principal, error) {
	return nil, invalidTokenAppError()
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
	if persistence.createdJob == nil || persistence.createdJob.DedupeKey != user.ID.String() ||
		persistence.createdJob.Type != model.JobTypeProfilePictureGenerateDefault {
		t.Fatalf("local user default-picture job = %#v", persistence.createdJob)
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
	if principal.UserID != user.ID || principal.SessionID != result.Session.ID {
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
