// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// These tests prove application security recovery when best-effort cluster
// invalidations are lost or duplicated. Authoritative PostgreSQL (store) state
// and bounded authentication caches decide correctness, not message delivery.

func TestSessionRevocationWithoutClusterFanoutRejectsAfterCacheMiss(t *testing.T) {
	t.Parallel()

	// Node A revokes in the store and clears its local cache, but the peer
	// never receives the cluster invalidation. After the peer's cache miss,
	// store resolution must reject the credential.
	storeFake := newAuthenticationStoreFake()
	cacheA := newAuthenticationCacheFake()
	cacheB := newAuthenticationCacheFake()
	serviceA := newTestAuthenticationService(t, storeFake)
	serviceA.cache = cacheA
	serviceB := newTestAuthenticationService(t, storeFake)
	serviceB.cache = cacheB

	user, rawAccess := seedAuthenticatedSession(t, serviceA)
	ctx := context.Background()

	// Warm both nodes' authentication caches (simulating prior successful auth).
	if _, err := serviceA.authenticateAccess(ctx, rawAccess); err != nil {
		t.Fatalf("node A warm auth: %v", err)
	}
	if _, err := serviceB.authenticateAccess(ctx, rawAccess); err != nil {
		t.Fatalf("node B warm auth: %v", err)
	}

	// Commit revocation on node A only; intentionally skip peer fan-out.
	sessionID := ""
	for id, session := range storeFake.sessions {
		if session.UserId == user.Id {
			sessionID = id
			break
		}
	}
	if sessionID == "" {
		t.Fatal("session not found")
	}
	hashes, err := storeFake.Session().Revoke(ctx, sessionID, user.Id, time.Now().UnixMilli(), "test")
	if err != nil {
		t.Fatal(err)
	}
	serviceA.deleteAuthenticationCache(ctx, hashes)

	// Stale cache on B may still accept until miss/TTL — that is the bounded
	// non-guarantee of best-effort invalidation.
	if _, err := serviceB.authenticateAccess(ctx, rawAccess); err != nil {
		t.Fatalf("stale cache on B still accepted before miss, got error %v", err)
	}

	// Force a cache miss to model TTL expiry or process restart without
	// replaying the lost invalidation message.
	for _, hash := range hashes {
		_ = cacheB.Delete(ctx, authenticationCachePrefix+hash)
	}
	if _, err := serviceB.authenticateAccess(ctx, rawAccess); !Is(err, "authentication.invalid_token") {
		t.Fatalf("node B auth after missed invalidation + cache miss error = %v", err)
	}
}

func TestDuplicateSessionRevocationPropagationIsIdempotent(t *testing.T) {
	t.Parallel()

	cache := newAuthenticationCacheFake()
	auth := &AuthenticationService{cache: cache}
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newRealtimeService(auth, nil)
	if err := realtime.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}

	userID := model.NewId()
	sessionID := model.NewId()
	hash := model.HashToken(model.NewCredentialToken())
	ctx := context.Background()
	// Seed a cache entry that both propagations must delete.
	if err := cache.SetAlways(ctx, authenticationCachePrefix+hash, []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}

	realtime.PropagateSessionRevocation(ctx, userID, []string{sessionID}, []string{hash})
	realtime.PropagateSessionRevocation(ctx, userID, []string{sessionID}, []string{hash})

	if len(sink.sessionCloses) < 2 {
		t.Fatalf("session closes = %#v, want duplicate local closes", sink.sessionCloses)
	}
	if len(cluster.broadcasts) < 2 {
		t.Fatalf("cluster broadcasts = %d, want duplicates attempted", len(cluster.broadcasts))
	}
	// Second delete is a no-op success; cache must remain absent.
	if _, err := cache.Get(ctx, authenticationCachePrefix+hash); !errors.Is(err, errAuthenticationCacheMiss) {
		t.Fatalf("cache after duplicate revocation = %v, want miss", err)
	}
}

func TestMissedAuthorizationInvalidationStillUsesCurrentStoreState(t *testing.T) {
	t.Parallel()

	// Authorization does not cache decisions. Ending a binding on one node
	// without delivering the cluster invalidation still denies the action on
	// the peer, because every Can() resolves active bindings from the store.
	institutionID := model.NewId()
	userID := model.NewId()
	roleID := model.NewId()
	bindingID := model.NewId()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	role := &model.Role{
		Id:          roleID,
		Name:        "recovery_admin",
		DisplayName: "Recovery Admin",
		Permissions: []string{string(model.ActionInstitutionManage)},
	}
	binding := &model.RoleBinding{
		Id:        bindingID,
		UserId:    userID,
		RoleId:    roleID,
		ScopeType: model.RoleScopeInstitution,
		ScopeId:   institutionID,
		StartAt:   now.Add(-time.Hour).UnixMilli(),
	}
	institution := &model.Institution{ID: model.InstitutionID(institutionID), Name: "Recovery University"}

	root := &recoveryAuthorizationStore{
		institution: institution,
		roles:       map[string]*model.Role{roleID: role},
		bindings:    map[string]*model.RoleBinding{bindingID: binding},
	}
	authz := newAuthorizationService(root, nil)
	authz.now = func() time.Time { return now }

	principal := model.Principal{
		UserId:                 userID,
		SessionId:              model.NewId(),
		CredentialId:           model.NewId(),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        now.UnixMilli(),
	}
	resource := model.Resource{Type: model.ResourceInstitution, Id: institutionID}
	ctx := context.Background()

	allowed, err := authz.Can(ctx, principal, model.ActionInstitutionManage, resource)
	if err != nil {
		t.Fatalf("initial Can() error = %v", err)
	}
	if !allowed {
		t.Fatal("initial Can() = false, want true with active binding")
	}

	// Simulate a peer that never receives InvalidateAuthorization: only the
	// durable binding is ended in the shared store.
	delete(root.bindings, bindingID)

	allowed, err = authz.Can(ctx, principal, model.ActionInstitutionManage, resource)
	if err != nil {
		t.Fatalf("Can() after missed invalidation error = %v", err)
	}
	if allowed {
		t.Fatal("Can() after binding end without cluster message = true, want false")
	}
}

func TestStaleAuthenticationCacheBoundedBySessionExpiry(t *testing.T) {
	t.Parallel()

	// Even without invalidation, a cached principal cannot outlive the session
	// absolute/idle expiry encoded in the cache value.
	storeFake := newAuthenticationStoreFake()
	cache := newAuthenticationCacheFake()
	service := newTestAuthenticationService(t, storeFake)
	service.cache = cache
	service.sessions.AccessTTL = time.Hour
	service.sessions.IdleTTL = time.Hour
	service.sessions.AbsoluteTTL = time.Hour

	fixedNow := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedNow }

	_, rawAccess := seedAuthenticatedSession(t, service)
	ctx := context.Background()
	if _, err := service.authenticateAccess(ctx, rawAccess); err != nil {
		t.Fatal(err)
	}

	// Put a deliberately stale long-lived cache entry that still claims validity
	// while the cached session is already past absolute/idle expiry.
	for _, session := range storeFake.sessions {
		for hash, credential := range storeFake.accessByHash {
			if credential.SessionId != session.Id {
				continue
			}
			user := storeFake.users[session.UserId]
			expiredSession := *session
			expiredSession.ExpiresAt = fixedNow.Add(-time.Minute).UnixMilli()
			expiredSession.IdleExpiresAt = fixedNow.Add(-time.Minute).UnixMilli()
			resolved := &cachedAuthentication{
				Credential: credential,
				Session:    &expiredSession,
				User:       user,
			}
			data, err := json.Marshal(resolved)
			if err != nil {
				t.Fatal(err)
			}
			// Direct cache write with long TTL to simulate a peer that never
			// received invalidation and has not yet dropped its entry by TTL.
			if err := cache.SetAlways(ctx, authenticationCachePrefix+hash, data, time.Hour); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := service.authenticateAccess(ctx, rawAccess); !Is(err, "authentication.invalid_token") {
		t.Fatalf("auth with expired cached session error = %v", err)
	}
}

func TestDuplicateRealtimePeerPublicationDoesNotRebroadcast(t *testing.T) {
	t.Parallel()

	// Lost or duplicated cluster realtime publications must never rebroadcast
	// and must not invent durable security state. Peers only apply locally.
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	service := newRealtimeService(nil, nil)
	if err := service.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}

	unitID := model.NewId()
	event := RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			Id:   unitID,
		},
	}
	if err := service.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(cluster.broadcasts) != 1 {
		t.Fatalf("broadcasts = %d", len(cluster.broadcasts))
	}
	payload := cluster.broadcasts[0].data

	// Duplicate peer deliveries (Memberlist non-guarantee).
	if err := service.handlePeerPublication(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if err := service.handlePeerPublication(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(cluster.broadcasts) != 1 {
		t.Fatalf("peer path rebroadcast: broadcasts = %d", len(cluster.broadcasts))
	}
	// Local sink may observe duplicates; durable correctness is HTTP resync.
	if len(sink.events) < 3 {
		t.Fatalf("local events = %d, want original + 2 peer applies", len(sink.events))
	}
}

func seedAuthenticatedSession(
	t *testing.T,
	service *AuthenticationService,
) (*model.User, string) {
	t.Helper()
	password := "CorrectHorseBatteryStaple1!"
	suffix := model.NewId()
	user, err := service.createLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{
			Username: "cluster-user-" + suffix,
			Email:    "cluster-user-" + suffix + "@example.edu",
		},
		Password: password,
	})
	if err != nil {
		t.Fatalf("createLocalUser() error = %v", err)
	}
	result, err := service.login(context.Background(), LoginCommand{
		LoginID:    user.Username,
		Password:   password,
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("login() error = %v", err)
	}
	if result.Tokens == nil || result.Tokens.AccessToken == "" {
		t.Fatalf("login result missing access token: %#v", result)
	}
	return user, result.Tokens.AccessToken
}

// recoveryAuthorizationStore is a minimal root store for authorization
// recovery proofs. Only Institution, Role, and RoleBinding are exercised.
type recoveryAuthorizationStore struct {
	institution *model.Institution
	roles       map[string]*model.Role
	bindings    map[string]*model.RoleBinding
}

func (s *recoveryAuthorizationStore) Institution() store.InstitutionStore {
	return recoveryInstitutionStore{root: s}
}
func (s *recoveryAuthorizationStore) Role() store.RoleStore {
	return recoveryRoleStore{root: s}
}
func (s *recoveryAuthorizationStore) RoleBinding() store.RoleBindingStore {
	return recoveryRoleBindingStore{root: s}
}

func (s *recoveryAuthorizationStore) User() store.UserStore                             { return nil }
func (s *recoveryAuthorizationStore) PasswordCredential() store.PasswordCredentialStore { return nil }
func (s *recoveryAuthorizationStore) Session() store.SessionStore                       { return nil }
func (s *recoveryAuthorizationStore) SessionCredential() store.SessionCredentialStore   { return nil }
func (s *recoveryAuthorizationStore) MFA() store.MFAStore                               { return nil }
func (s *recoveryAuthorizationStore) PersonalAccessToken() store.PersonalAccessTokenStore {
	return nil
}
func (s *recoveryAuthorizationStore) AcademicUnit() store.AcademicUnitStore             { return nil }
func (s *recoveryAuthorizationStore) Programme() store.ProgrammeStore                   { return nil }
func (s *recoveryAuthorizationStore) ProgrammeLevel() store.ProgrammeLevelStore         { return nil }
func (s *recoveryAuthorizationStore) AcademicPeriod() store.AcademicPeriodStore         { return nil }
func (s *recoveryAuthorizationStore) Class() store.ClassStore                           { return nil }
func (s *recoveryAuthorizationStore) ExternalIdentity() store.ExternalIdentityStore     { return nil }
func (s *recoveryAuthorizationStore) ExternalLoginState() store.ExternalLoginStateStore { return nil }
func (s *recoveryAuthorizationStore) UserToken() store.UserTokenStore                   { return nil }
func (s *recoveryAuthorizationStore) Affiliation() store.AffiliationStore               { return nil }
func (s *recoveryAuthorizationStore) AcademicUnitMember() store.AcademicUnitMemberStore { return nil }
func (s *recoveryAuthorizationStore) ClassMember() store.ClassMemberStore               { return nil }
func (s *recoveryAuthorizationStore) Audit() store.AuditStore                           { return nil }
func (s *recoveryAuthorizationStore) Installation() store.InstallationStore             { return nil }
func (s *recoveryAuthorizationStore) ClusterDiscovery() store.ClusterDiscoveryStore     { return nil }
func (s *recoveryAuthorizationStore) Ping(context.Context) error                        { return nil }
func (s *recoveryAuthorizationStore) GetDBSchemaVersion(context.Context) (int, error)   { return 0, nil }
func (s *recoveryAuthorizationStore) GetLocalSchemaVersion() (int, error)               { return 0, nil }
func (s *recoveryAuthorizationStore) ValidateSchema(context.Context) error              { return nil }
func (s *recoveryAuthorizationStore) Close() error                                      { return nil }

type recoveryInstitutionStore struct{ root *recoveryAuthorizationStore }

func (s recoveryInstitutionStore) Get(_ context.Context, id string) (*model.Institution, error) {
	if s.root.institution == nil || s.root.institution.ID.String() != id {
		return nil, store.NewErrNotFound("institution", id)
	}
	cloned := *s.root.institution
	return &cloned, nil
}
func (recoveryInstitutionStore) Save(context.Context, *model.Institution) (*model.Institution, error) {
	return nil, errors.New("unused")
}
func (recoveryInstitutionStore) GetSingleton(context.Context) (*model.Institution, error) {
	return nil, errors.New("unused")
}
func (recoveryInstitutionStore) Update(context.Context, *model.Institution) (*model.Institution, error) {
	return nil, errors.New("unused")
}
func (recoveryInstitutionStore) UpdateWithAudit(context.Context, *store.InstitutionUpdate) (*model.Institution, error) {
	return nil, errors.New("unused")
}
func (recoveryInstitutionStore) Delete(context.Context, string, int64) error {
	return errors.New("unused")
}

type recoveryRoleStore struct{ root *recoveryAuthorizationStore }

func (s recoveryRoleStore) GetByIds(_ context.Context, ids []string) ([]*model.Role, error) {
	out := make([]*model.Role, 0, len(ids))
	for _, id := range ids {
		role, ok := s.root.roles[id]
		if !ok {
			continue
		}
		cloned := *role
		out = append(out, &cloned)
	}
	return out, nil
}
func (recoveryRoleStore) Save(context.Context, *model.Role) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) SaveWithAudit(context.Context, *store.RoleCreation) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Get(context.Context, string) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) GetByName(context.Context, string) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) List(context.Context) ([]*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Update(context.Context, *model.Role) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) UpdateWithAudit(context.Context, *store.RoleUpdate) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Delete(context.Context, string, int64) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) DeleteWithAudit(context.Context, *store.RoleDeletion) (*model.Role, error) {
	return nil, errors.New("unused")
}

type recoveryRoleBindingStore struct{ root *recoveryAuthorizationStore }

func (s recoveryRoleBindingStore) ListActiveByUser(_ context.Context, userID string, at int64) ([]*model.RoleBinding, error) {
	out := make([]*model.RoleBinding, 0)
	for _, binding := range s.root.bindings {
		if binding.UserId != userID {
			continue
		}
		if binding.StartAt > at {
			continue
		}
		if binding.EndAt != 0 && binding.EndAt <= at {
			continue
		}
		cloned := *binding
		out = append(out, &cloned)
	}
	return out, nil
}
func (recoveryRoleBindingStore) Save(context.Context, *model.RoleBinding) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) SaveWithAudit(context.Context, *store.RoleBindingCreation) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) Get(context.Context, string) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) ListByUser(context.Context, string) ([]*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) End(context.Context, string, int64) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) EndWithAudit(context.Context, *store.RoleBindingEnd) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
