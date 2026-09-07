// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
		return nil, ErrAuthenticationCacheMiss
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
		return ErrAuthenticationCacheNotStored
	}
	c.values[key] = append([]byte(nil), value...)
	return nil
}

func (c *authenticationCacheFake) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
	delete(c.counters, key)
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
	createdSettings     *model.UserSettingsDocument
	rotation            *store.SessionRotation
	userGetErr          error
	rotatedAccess       *model.SessionCredential
	rotatedRefresh      *model.SessionCredential
	rotatedAt           time.Time
	rotatedIdleExpiry   time.Time
}

type authenticationDesktopRegistrationLookupFake struct{}

func (authenticationDesktopRegistrationLookupFake) Get(context.Context, string) (*model.DesktopRegistration, error) {
	return nil, store.NewErrNotFound("desktop_registration", "")
}

func testAuthenticationDesktopDependencies(t *testing.T, cache authenticationCache) authenticationDesktopDependencies {
	t.Helper()
	dpop, err := newDPoPSecurity(cache, dpopPolicy{Origin: "https://proctor.test", NonceLifetime: 5 * time.Minute,
		ProofLifetime: 5 * time.Minute, ClockSkew: time.Minute, NewNonce: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return authenticationDesktopDependencies{registrations: authenticationDesktopRegistrationLookupFake{}, dpop: dpop}
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

func (s authenticationUserStore) GetCurrentContext(context.Context, model.UserID, int) (*store.CurrentUserContext, error) {
	return nil, errors.New("current context is not implemented by authentication test store")
}

func (s authenticationUserStore) RegisterLocal(context.Context, *store.PublicLocalUserRegistration) (*store.PublicLocalUserRegistrationResult, error) {
	return nil, errors.New("public registration is not implemented by authentication test store")
}

func (s authenticationUserStore) Create(
	_ context.Context,
	input *store.UserCreation,
) (*store.UserCreationResult, error) {
	if input.Settings == nil || input.Settings.UserID != input.User.ID ||
		input.Settings.Source != model.UserSettingsInitialSource ||
		input.Settings.FormatVersion != model.UserSettingsFormatVersion1 {
		return nil, errors.New("invalid initial user settings")
	}
	cloned := *input.User
	s.root.users[cloned.ID.String()] = &cloned
	s.root.usersByUsername[strings.ToLower(cloned.Username)] = &cloned
	s.root.usersByEmail[strings.ToLower(cloned.Email)] = &cloned
	pass := *input.PasswordCredential
	s.root.passwords[cloned.ID.String()] = &pass
	job := *input.DefaultProfilePictureJob
	s.root.createdJob = &job
	s.root.createdSettings = input.Settings.Clone()
	return &store.UserCreationResult{User: &cloned, PasswordCredential: &pass}, nil
}

func (s authenticationUserStore) Get(_ context.Context, id string) (*model.User, error) {
	if s.root.userGetErr != nil {
		return nil, s.root.userGetErr
	}
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
func (authenticationUserStore) List(context.Context, store.UserListOptions) ([]*model.User, error) {
	return nil, errors.New("unused")
}
func (authenticationUserStore) MatchVisibility(context.Context, string, store.UserVisibilityScope) (store.UserVisibilityMatch, error) {
	return store.UserVisibilityMatch{}, errors.New("unused")
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

func (s authenticationPasswordStore) Rehash(_ context.Context, input *store.PasswordCredentialRehash) error {
	credential := s.root.passwords[input.UserID.String()]
	if credential == nil || credential.ArchivedAt.Valid || credential.ID != input.ID ||
		credential.Revision != input.ExpectedRevision || credential.PasswordHash != input.ExpectedHash {
		return store.ErrPasswordCredentialChanged
	}
	cloned := *credential
	cloned.PasswordHash = input.PasswordHash
	s.root.passwords[input.UserID.String()] = &cloned
	return nil
}

func (authenticationPasswordStore) Save(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error) {
	return nil, errors.New("unused")
}

func (authenticationPasswordStore) EnrollWithAudit(context.Context, *store.PasswordCredentialEnrollment) (*store.AuthenticationMethodMutationResult, error) {
	return nil, errors.New("unused")
}

func (authenticationPasswordStore) RemoveWithAudit(context.Context, *store.PasswordCredentialRemoval) (*store.AuthenticationMethodMutationResult, error) {
	return nil, errors.New("unused")
}

type authenticationSessionStore struct{ root *authenticationStoreFake }

func (s authenticationSessionStore) Save(
	_ context.Context,
	input *store.SessionCreation,
) (*model.Session, []*model.SessionCredential, error) {
	if s.root.saveErr != nil {
		return nil, nil, s.root.saveErr
	}
	session, credentials, maximumPerUser := input.Session, input.Credentials, input.MaximumActive
	if session.AuthenticationMethod == "password" {
		credential := s.root.passwords[session.UserID.String()]
		if credential == nil || credential.ArchivedAt.Valid || credential.ID != input.PasswordProof.ID ||
			credential.Revision != input.PasswordProof.Revision {
			return nil, nil, store.ErrPasswordCredentialChanged
		}
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

func (s authenticationSessionStore) UpdateActivity(_ context.Context, sessionID string, lastActivityAt, idleExpiresAt time.Time) error {
	session, ok := s.root.sessions[sessionID]
	if !ok {
		return store.NewErrNotFound("session", sessionID)
	}
	at := model.TimeUTC(lastActivityAt)
	session.LastActivityAt = at
	session.IdleExpiresAt = model.TimeUTC(idleExpiresAt)
	session.UpdatedAt = at
	return nil
}

func (s authenticationSessionStore) Revoke(_ context.Context, sessionID, _ string, revokedAt int64, reason model.SessionRevocationReason) ([]string, error) {
	return s.revoke(sessionID, model.TimeFromMillis(revokedAt), reason)
}

func (s authenticationSessionStore) revoke(sessionID string, at time.Time, reason model.SessionRevocationReason) ([]string, error) {
	session, ok := s.root.sessions[sessionID]
	if !ok {
		return nil, store.NewErrNotFound("session", sessionID)
	}
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

func (s authenticationSessionStore) EnforceExpiry(
	_ context.Context,
	sessionID string,
	userID string,
	at time.Time,
) (*store.SessionExpiryEnforcementResult, error) {
	session, ok := s.root.sessions[sessionID]
	if !ok || session.UserID.String() != userID || session.RevokedAt.Valid {
		return nil, store.NewErrNotFound("session", sessionID)
	}
	at = model.TimeUTC(at)
	if !session.IsExpiredAt(at) {
		cloned := *session
		return &store.SessionExpiryEnforcementResult{Session: &cloned}, nil
	}
	hashes, err := s.revoke(sessionID, at, model.SessionRevocationExpired)
	if err != nil {
		return nil, err
	}
	cloned := *session
	return &store.SessionExpiryEnforcementResult{
		Session: &cloned, TokenHashes: hashes, Expired: true,
	}, nil
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
func (authenticationSessionStore) ListActiveByUser(context.Context, string, time.Time) ([]*model.Session, error) {
	return nil, errors.New("unused")
}
func (s authenticationSessionStore) RevokeWithAudit(_ context.Context, input *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	session := s.root.sessions[input.SessionID]
	if session == nil || session.UserID.String() != input.UserID || session.RevokedAt.Valid {
		if input.Reason == model.SessionRevocationUserLogout {
			return &store.SessionRevocationResult{}, nil
		}
		return nil, store.NewErrNotFound("session", input.SessionID)
	}
	hashes, err := s.revoke(input.SessionID, model.TimeFromMillis(input.RevokedAt), input.Reason)
	if err != nil {
		return nil, err
	}
	cloned := *session
	return &store.SessionRevocationResult{Session: &cloned, TokenHashes: hashes}, nil
}
func (authenticationSessionStore) RevokeAllForUser(context.Context, string, int64, model.SessionRevocationReason) ([]*model.Session, []string, error) {
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
	if kind == model.SessionCredentialRefresh && s.root.rotation != nil && s.root.rotation.Session != nil {
		session := *s.root.rotation.Session
		return &model.SessionCredential{Kind: kind, TokenHash: tokenHash}, &session, nil
	}
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
	at time.Time,
	idleExpiry time.Time,
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
	hasher, err := newPasswordHasher(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newAuthenticationService(
		persistence.User(),
		persistence.PasswordCredential(),
		persistence.Session(),
		persistence.SessionCredential(),
		allowAllAuthenticationAccessPolicy(),
		cache,
		mustAuthenticationAttemptAccounting(t, cache),
		effects,
		&mutationAttemptAuditorFake{events: &[]string{}, beginID: model.NewId()},
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
		testAuthenticationDesktopDependencies(t, cache),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mustAuthenticationAttemptAccounting(
	t *testing.T,
	cache authenticationAttemptCache,
) *authenticationAttemptAccounting {
	t.Helper()
	accounting, err := newAuthenticationAttemptAccounting(cache)
	if err != nil {
		t.Fatal(err)
	}
	return accounting
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
	persistence.userGetErr = errors.New("database unavailable")
	if err := service.ValidatePrincipal(context.Background(), principal); !Is(err, "authentication.internal") {
		t.Fatalf("dependency error = %v", err)
	}
	persistence.userGetErr = nil
	session.UserID = model.NewUserID()
	if err := service.ValidatePrincipal(context.Background(), principal); !Is(err, "authentication.invalid_token") {
		t.Fatalf("mismatched session error = %v", err)
	}
}

func TestAuthenticationExpiryEnforcementClosesEstablishedWebSocketSession(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID()}
	session := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID,
		ExpiresAt: at.Add(time.Hour), IdleExpiresAt: at,
	}
	persistence := newAuthenticationStoreFake()
	persistence.users[user.ID.String()] = user
	persistence.sessions[session.ID.String()] = session
	effects := &authenticationEffectsRecorder{}
	service := newTestAuthenticationServiceWithEffects(
		t, persistence, newAuthenticationCacheFake(), effects,
		discardAuthenticationMFAVerifier{}, discardAuthenticationPATResolver{},
		model.NewCredentialToken, func() time.Time { return at },
	)
	principal := model.Principal{
		UserID: user.ID, SessionID: session.ID,
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: at.Add(-time.Minute), ClientType: model.SessionClientWeb,
	}
	if err := service.ValidatePrincipal(context.Background(), principal); !Is(err, "authentication.invalid_token") {
		t.Fatalf("ValidatePrincipal(expired) error = %v", err)
	}
	if effects.last != "sessions" || !session.RevokedAt.Valid ||
		session.RevocationReason != model.SessionRevocationExpired {
		t.Fatalf("expiry effects=%q session=%#v", effects.last, session)
	}
}

func TestAuthenticationExpiryEnforcementRejectsHTTPAccessAndRefresh(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID()}
	accessRaw := model.NewCredentialToken()
	accessHash := model.HashToken(accessRaw)
	accessSession := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID, ClientType: model.SessionClientWeb,
		ExpiresAt: at.Add(time.Hour), IdleExpiresAt: at,
	}
	persistence := newAuthenticationStoreFake()
	persistence.users[user.ID.String()] = user
	persistence.sessions[accessSession.ID.String()] = accessSession
	persistence.accessByHash[accessHash] = &model.SessionCredential{
		ID: model.NewSessionCredentialID(), SessionID: accessSession.ID,
		Kind: model.SessionCredentialAccess, TokenHash: accessHash, ExpiresAt: at.Add(time.Hour),
	}
	persistence.sessionByCredential[accessHash] = accessSession
	effects := &authenticationEffectsRecorder{}
	service := newTestAuthenticationServiceWithEffects(
		t, persistence, newAuthenticationCacheFake(), effects,
		discardAuthenticationMFAVerifier{}, discardAuthenticationPATResolver{},
		model.NewCredentialToken, func() time.Time { return at },
	)
	if principal, err := service.authenticateAccess(context.Background(), accessRaw); principal != nil ||
		!Is(err, "authentication.invalid_token") {
		t.Fatalf("authenticateAccess(expired) = %#v, %v", principal, err)
	}
	if effects.last != "sessions" || accessSession.RevocationReason != model.SessionRevocationExpired {
		t.Fatalf("access expiry effects=%q session=%#v", effects.last, accessSession)
	}

	refreshSession := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID, ClientType: model.SessionClientWeb,
		ExpiresAt: at.Add(time.Hour), IdleExpiresAt: at,
	}
	persistence.rotation = &store.SessionRotation{
		Session: refreshSession, Expired: true,
		RevokedAccessHashes: []string{model.HashToken(model.NewCredentialToken())},
	}
	effects.last = ""
	if session, tokens, err := service.refresh(context.Background(), RefreshSessionCommand{
		RefreshToken: model.NewCredentialToken(),
	}); session != nil || tokens != nil || !Is(err, "authentication.invalid_token") {
		t.Fatalf("refresh(expired) = %#v, %#v, %v", session, tokens, err)
	}
	if effects.last != "sessions" {
		t.Fatalf("refresh expiry effect = %q", effects.last)
	}
}

func TestAuthenticationAuthoritativeIdleExtensionOverridesStaleExpiredCache(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID()}
	accessRaw := model.NewCredentialToken()
	accessHash := model.HashToken(accessRaw)
	authoritative := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: at.Add(-time.Hour), LastActivityAt: at.Add(-30 * time.Second),
		IdleExpiresAt: at.Add(time.Hour), ExpiresAt: at.Add(24 * time.Hour),
	}
	credential := &model.SessionCredential{
		ID: model.NewSessionCredentialID(), SessionID: authoritative.ID,
		Kind: model.SessionCredentialAccess, TokenHash: accessHash, ExpiresAt: at.Add(time.Hour),
	}
	persistence := newAuthenticationStoreFake()
	persistence.users[user.ID.String()] = user
	persistence.sessions[authoritative.ID.String()] = authoritative
	persistence.accessByHash[accessHash] = credential
	persistence.sessionByCredential[accessHash] = authoritative
	cache := newAuthenticationCacheFake()
	service := newTestAuthenticationServiceWithPorts(
		t, persistence, cache, discardAuthenticationMFAVerifier{}, discardAuthenticationPATResolver{},
		model.NewCredentialToken, func() time.Time { return at },
	)
	stale := *authoritative
	stale.IdleExpiresAt = at
	cacheLegacyAuthentication(t, cache, credential, &stale, user)

	principal, err := service.authenticateAccess(context.Background(), accessRaw)
	if err != nil || principal == nil || principal.SessionID != authoritative.ID {
		t.Fatalf("authenticateAccess(stale cached expiry) = %#v, %v", principal, err)
	}
	if authoritative.RevokedAt.Valid {
		t.Fatalf("authoritatively extended Session was revoked: %#v", authoritative)
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

			_, tokens, err := service.refresh(context.Background(), RefreshSessionCommand{RefreshToken: presentedRefresh})
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
			if !persistence.rotatedAt.Equal(at) ||
				!persistence.rotatedIdleExpiry.Equal(at.Add(2*time.Hour)) {
				t.Fatalf("rotation time = %v idle = %v", persistence.rotatedAt, persistence.rotatedIdleExpiry)
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
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationServiceWithRuntime(
		t,
		persistence,
		newAuthenticationCacheFake(),
		func() string {
			credential := credentials[next]
			next++
			return credential
		},
		func() time.Time { return at },
	)
	user := &model.User{ID: model.NewUserID(), CreatedAt: at, UpdatedAt: at, Revision: 1}
	passwordCredential := &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-password"}
	passwordCredential.PrepareCreate(model.NewPasswordCredentialID(), at)
	persistence.passwords[user.ID.String()] = passwordCredential
	resultSession, tokens, err := service.createSession(
		context.Background(),
		sessionIssuance{
			User: user, ClientType: model.SessionClientCLI,
			DeviceID: "device", DeviceName: "Device",
			AuthenticationMethod:   "password",
			PasswordProof:          store.PasswordCredentialProof{ID: passwordCredential.ID, Revision: passwordCredential.Revision},
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
	recoveryCalls int
	recoveryErr   error
	calls         int
	strength      model.AuthenticationStrength
	completedAt   int64
	afterVerify   func()
}

func (f *authenticationMFAVerifierFake) VerifyLogin(
	context.Context,
	string,
	string,
	time.Time,
) (model.AuthenticationStrength, int64, error) {
	f.calls++
	if f.afterVerify != nil {
		f.afterVerify()
	}
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

func TestLoginRejectsExistingLocalCredentialWhenCurrentPolicyDisablesLocalLogin(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{Username: "policy-user", Email: "policy-user@example.edu"}, Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	service.accessPolicy = authenticationAccessPolicyFake{local: false}
	result, err := service.login(context.Background(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientWeb, Source: "192.0.2.5",
	})
	if result != nil || !Is(err, "authentication.invalid_credentials") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(persistence.sessions) != 0 {
		t.Fatalf("disabled local login created %d sessions", len(persistence.sessions))
	}
}

func TestLoginRejectsDisabledUserBeforeMFARecoveryRead(t *testing.T) {
	t.Parallel()
	for _, password := range []string{"CorrectHorseBatteryStaple1!", "incorrect password"} {
		name := "wrong password"
		if password == "CorrectHorseBatteryStaple1!" {
			name = "correct password"
		}
		t.Run(name, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
				User: &model.User{Username: "disabled-user", Email: "disabled@example.edu"}, Password: "CorrectHorseBatteryStaple1!",
			})
			if err != nil {
				t.Fatal(err)
			}
			persistence.usersByEmail[user.Email].DisabledAt = model.OptionalTimeFrom(time.Now())
			mfa := &authenticationMFAVerifierFake{recoveryErr: errors.New("disabled User has no active MFA recovery state")}
			service.mfa = mfa
			result, err := service.login(context.Background(), LoginCommand{
				LoginID: user.Email, Password: password, ClientType: model.SessionClientCLI, Source: "192.0.2.7",
			})
			if result != nil || !Is(err, "authentication.invalid_credentials") || mfa.recoveryCalls != 0 || mfa.calls != 0 || len(persistence.sessions) != 0 {
				t.Fatalf("disabled login reached MFA or issued a Session: %v; recovery reads=%d, MFA calls=%d", err, mfa.recoveryCalls, mfa.calls)
			}
		})
	}
}

func TestLoginPasswordCapacityPreservesAccountParity(t *testing.T) {
	for _, account := range []string{"active", "wrong password", "missing user", "disabled user", "missing credential", "local login disabled", "invalid login", "oversized password"} {
		t.Run(account, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			const password = "CorrectHorseBatteryStaple1!"
			user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
				User: &model.User{Username: "capacity-user", Email: "capacity-user@example.edu"}, Password: password,
			})
			if err != nil {
				t.Fatal(err)
			}
			credential := *persistence.passwords[user.ID.String()]
			command := LoginCommand{LoginID: user.Email, Password: password, ClientType: model.SessionClientWeb, Source: "192.0.2.7"}
			switch account {
			case "wrong password":
				command.Password = "incorrect password"
			case "missing user":
				command.LoginID = "missing@example.edu"
			case "disabled user":
				persistence.usersByEmail[user.Email].DisabledAt = model.OptionalTimeFrom(time.Now())
			case "missing credential":
				delete(persistence.passwords, user.ID.String())
			case "local login disabled":
				service.accessPolicy = authenticationAccessPolicyFake{local: false}
			case "invalid login":
				command.LoginID = ""
			case "oversized password":
				command.Password = strings.Repeat("x", service.hasher.maximumLength+1)
			}
			saturatePasswordWork(t, service.hasher)
			result, err := service.login(context.Background(), command)
			if result != nil || !Is(err, "service.busy") {
				t.Fatalf("saturated login = %#v/%v, want service.busy", result, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err = service.login(ctx, command)
			if result != nil || !Is(err, "authentication.internal") || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled login = %#v/%v", result, err)
			}
			if len(persistence.sessions) != 0 || len(persistence.accessByHash) != 0 {
				t.Fatal("rejected password work issued Session credentials")
			}
			if current := persistence.passwords[user.ID.String()]; current != nil && *current != credential {
				t.Fatal("rejected password work mutated the password credential")
			}
		})
	}
}

func TestLocalUserCreationRejectsPasswordWorkBeforePersistence(t *testing.T) {
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	saturatePasswordWork(t, service.hasher)
	command := CreateLocalUserCommand{User: &model.User{Username: "new-user", Email: "new-user@example.edu"}, Password: "CorrectHorseBatteryStaple1!"}
	if user, err := service.createLocalUser(context.Background(), command); user != nil || !Is(err, "service.busy") {
		t.Fatalf("saturated creation = %#v/%v", user, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if user, err := service.createLocalUser(ctx, command); user != nil || !Is(err, "authentication.internal") || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled creation = %#v/%v", user, err)
	}
	if len(persistence.users) != 0 || len(persistence.passwords) != 0 || persistence.createdJob != nil {
		t.Fatal("rejected password work created account state")
	}
}

func TestLocalAuthenticationRejectsRehashCapacityBeforeProofAndMutation(t *testing.T) {
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	const password = "CorrectHorseBatteryStaple1!"
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{Username: "rehash-capacity", Email: "rehash-capacity@example.edu"}, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := *persistence.passwords[user.ID.String()]
	service.hasher.parameters.iterations++
	recorder := &passwordWorkRecorderFake{}
	service.hasher.recorder = recorder
	mfa := &authenticationMFAVerifierFake{strength: model.AuthenticationSingleFactor}
	service.mfa = mfa
	// Admit competing work at the next context check after verification has
	// returned its permit. This selects the rehash admission boundary without
	// sleeps, cryptographic substitutes, or a production-only test hook.
	var competed sync.Once
	ctx := passwordWorkBoundaryContext{Context: context.Background(), check: func() {
		if recorder.finished.Load() == 1 && len(service.hasher.work) == 0 {
			competed.Do(func() { saturatePasswordWork(t, service.hasher) })
		}
	}}
	proof, err := service.authenticateLocal(ctx, LoginCommand{
		LoginID: user.Email, Password: password, ClientType: model.SessionClientWeb, Source: "192.0.2.8",
	})
	if proof != nil || !Is(err, "service.busy") || recorder.rejected.Load() != 1 || mfa.calls != 0 {
		t.Fatalf("saturated rehash = %#v/%v, rejected work %d, MFA calls %d", proof, err, recorder.rejected.Load(), mfa.calls)
	}
	if *persistence.passwords[user.ID.String()] != credential || len(persistence.sessions) != 0 {
		t.Fatal("rejected rehash changed credentials or created a Session")
	}
}

type passwordWorkBoundaryContext struct {
	context.Context
	check func()
}

func (ctx passwordWorkBoundaryContext) Err() error {
	ctx.check()
	return ctx.Context.Err()
}

func TestLoginRejectsDirectDesktopSessionIssuance(t *testing.T) {
	t.Parallel()

	service := newTestAuthenticationService(t, newAuthenticationStoreFake())
	result, err := service.login(context.Background(), LoginCommand{LoginID: "user@example.edu",
		Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientDesktop, Source: "127.0.0.1:3"})
	if result != nil || !Is(err, "authentication.client_type.invalid") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestLoginMapsTerminalAccessPolicyFenceToGenericCredentialsFailure(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{Username: "terminal-policy-user", Email: "terminal-policy-user@example.edu"}, Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	persistence.saveErr = store.ErrAuthenticationMethodDisabled
	result, err := service.login(context.Background(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientWeb, Source: "192.0.2.6",
	})
	if result != nil || !Is(err, "authentication.invalid_credentials") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestLoginRejectsPasswordChangedAfterVerification(t *testing.T) {
	for _, rehash := range []bool{false, true} {
		name := "current_hash"
		if rehash {
			name = "rehash_required"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			const oldPassword = "CorrectHorseBatteryStaple1!"
			user, err := service.createLocalUser(ctx, CreateLocalUserCommand{
				User: &model.User{Username: "password-race", Email: "password-race@example.edu"}, Password: oldPassword,
			})
			if err != nil {
				t.Fatal(err)
			}
			if rehash {
				service.hasher.parameters.iterations++
			}
			replacement := *persistence.passwords[user.ID.String()]
			replacement.PasswordHash, err = service.hasher.Hash(context.Background(), "ReplacementPasswordForReset1!")
			if err != nil {
				t.Fatal(err)
			}
			replacement.Revision++
			replacement.PasswordChangedAt = replacement.PasswordChangedAt.Add(time.Microsecond)
			replacement.UpdatedAt = replacement.PasswordChangedAt
			// MFA is the existing seam immediately after password verification.
			// Commit reset here to force the otherwise nondeterministic ordering.
			service.mfa = &authenticationMFAVerifierFake{
				strength:    model.AuthenticationSingleFactor,
				afterVerify: func() { persistence.passwords[user.ID.String()] = &replacement },
			}
			result, err := service.login(ctx, LoginCommand{
				LoginID: user.Email, Password: oldPassword, ClientType: model.SessionClientWeb, Source: "192.0.2.10",
			})
			if result != nil || !Is(err, "authentication.invalid_credentials") {
				t.Fatalf("login after reset = %#v, %v", result, err)
			}
			if len(persistence.sessions) != 0 || len(persistence.accessByHash) != 0 {
				t.Fatal("stale password proof created a Session or access credential")
			}
			if *persistence.passwords[user.ID.String()] != replacement {
				t.Fatal("stale login changed the replacement password credential")
			}
		})
	}
}

func TestLocalAuthenticationRejectsRehashConflictBeforeReturningProof(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	const password = "CorrectHorseBatteryStaple1!"
	user, err := service.createLocalUser(ctx, CreateLocalUserCommand{
		User: &model.User{Username: "removed-password", Email: "removed-password@example.edu"}, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.hasher.parameters.iterations++
	service.mfa = &authenticationMFAVerifierFake{
		strength:    model.AuthenticationSingleFactor,
		afterVerify: func() { delete(persistence.passwords, user.ID.String()) },
	}
	proof, err := service.authenticateLocal(ctx, LoginCommand{
		LoginID: user.Email, Password: password, ClientType: model.SessionClientWeb, Source: "192.0.2.11",
	})
	if proof != nil || !Is(err, "authentication.invalid_credentials") {
		t.Fatalf("authenticate removed password = %#v, %v", proof, err)
	}
	if persistence.passwords[user.ID.String()] != nil {
		t.Fatal("rehash resurrected removed password credential")
	}
}

func TestLocalAuthenticationRehashPreservesPasswordProof(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationService(t, persistence)
	const password = "CorrectHorseBatteryStaple1!"
	user, err := service.createLocalUser(ctx, CreateLocalUserCommand{
		User: &model.User{Username: "rehash-proof", Email: "rehash-proof@example.edu"}, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := *persistence.passwords[user.ID.String()]
	service.hasher.parameters.iterations++
	recorder := &passwordWorkRecorderFake{}
	service.hasher.recorder = recorder
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base.Add(time.Duration(recorder.finished.Load()) * time.Second) }
	service.mfa = &authenticationMFAVerifierFake{strength: model.AuthenticationSingleFactor, afterVerify: func() {
		if recorder.finished.Load() != 2 || len(service.hasher.work) != 0 {
			t.Fatal("MFA ran before password work completed and released its permits")
		}
	}}
	proof, err := service.authenticateLocal(ctx, LoginCommand{
		LoginID: user.Email, Password: password, ClientType: model.SessionClientWeb, Source: "192.0.2.12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proof.AuthenticatedAt != base.Add(2*time.Second).UnixMilli() {
		t.Fatalf("authentication time = %d, want time after both password operations", proof.AuthenticatedAt)
	}
	after := persistence.passwords[user.ID.String()]
	if proof.PasswordProof.ID != before.ID || proof.PasswordProof.Revision != before.Revision ||
		after.Revision != before.Revision || after.PasswordChangedAt != before.PasswordChangedAt {
		t.Fatal("rehash changed the proved password identity or revision")
	}
	if after.PasswordHash == before.PasswordHash || service.hasher.NeedsRehash(after.PasswordHash) ||
		service.hasher.Verify(context.Background(), after.PasswordHash, password) != nil {
		t.Fatal("rehash did not preserve password verification under the new parameters")
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
	if persistence.createdSettings == nil || persistence.createdSettings.UserID != user.ID ||
		persistence.createdSettings.Source != model.UserSettingsInitialSource ||
		persistence.createdSettings.FormatVersion != model.UserSettingsFormatVersion1 {
		t.Fatalf("local user settings = %#v", persistence.createdSettings)
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

func (discardAuthenticationMFAVerifier) RecoveryState(_ context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	return &model.UserMFARecovery{UserID: userID}, nil
}
func (f *authenticationMFAVerifierFake) RecoveryState(_ context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	f.recoveryCalls++
	if f.recoveryErr != nil {
		return nil, f.recoveryErr
	}
	return &model.UserMFARecovery{UserID: userID}, nil
}
