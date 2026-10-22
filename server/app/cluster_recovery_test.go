// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// These tests prove application security recovery when best-effort cluster
// invalidations are lost or duplicated. Authoritative PostgreSQL (store) state
// decide correctness, not cache state or message delivery.

func TestSessionRevocationWithoutClusterFanoutRejectsWithWarmPeerCache(t *testing.T) {
	t.Parallel()

	// Node A revokes in the store and clears its local cache, but the peer
	// never receives the cluster invalidation. Every subsequent authentication
	// must reject the credential even while an old positive entry remains.
	storeFake := newAuthenticationStoreFake()
	cacheA := newAuthenticationCacheFake()
	cacheB := newAuthenticationCacheFake()
	serviceA := newTestAuthenticationServiceWithCache(t, storeFake, cacheA)
	serviceB := newTestAuthenticationServiceWithCache(t, storeFake, cacheB)

	user, rawAccess := seedAuthenticatedSession(t, serviceA)
	ctx := context.Background()
	hash := model.HashToken(rawAccess)
	cacheLegacyAuthentication(t, cacheA, storeFake.accessByHash[hash], storeFake.sessionByCredential[hash], user)
	cacheLegacyAuthentication(t, cacheB, storeFake.accessByHash[hash], storeFake.sessionByCredential[hash], user)

	// Both nodes have previously accepted the credential.
	_, err := serviceA.authenticateAccess(ctx, rawAccess)
	require.NoError(t, err)
	_, err = serviceB.authenticateAccess(ctx, rawAccess)
	require.NoError(t, err)

	// Commit revocation on node A only; intentionally skip peer fan-out.
	sessionID := ""
	for id, session := range storeFake.sessions {
		if session.UserID == user.ID {
			sessionID = id
			break
		}
	}
	require.NotEmpty(t, sessionID, "session not found")
	hashes, err := storeFake.Session().Withdraw(ctx, &store.SessionWithdrawal{ID: model.SessionID(sessionID), UserID: user.ID, RevokedAt: time.Now(), Reason: model.SessionRevocationInactiveUser})
	require.NoError(t, err)
	invalidator, err := newAuthenticationCacheInvalidator(authenticationCacheInvalidatorDependencies{Cache: cacheA, Diagnostics: &securityEffectsDiagnosticsFake{}})
	require.NoError(t, err)
	invalidator.InvalidateAccessCredentials(ctx, hashes)

	_, err = cacheB.Get(ctx, authenticationCachePrefix+hash)
	require.NoError(t, err)
	_, err = serviceB.authenticateAccess(ctx, rawAccess)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "node B auth after committed revocation with stale cache error = %v", err)
}

func TestDuplicateSessionRevocationPropagationIsIdempotent(t *testing.T) {
	t.Parallel()

	cache := newAuthenticationCacheFake()
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newTestRealtimeService(t, cache)
	require.NoError(t, realtime.AttachRealtimeClusterFanout(cluster))
	require.NoError(t, realtime.AttachRealtimeSink(sink))

	userID := model.NewID()
	sessionID := model.NewID()
	hash := model.HashToken(model.NewCredentialToken())
	ctx := context.Background()
	// Seed a cache entry that both propagations must delete.
	require.NoError(t, cache.SetAlways(ctx, authenticationCachePrefix+hash, []byte(`{}`), time.Hour))

	realtime.SessionsRevoked(ctx, model.UserID(userID), []model.SessionID{model.SessionID(sessionID)}, []string{hash})
	realtime.SessionsRevoked(ctx, model.UserID(userID), []model.SessionID{model.SessionID(sessionID)}, []string{hash})

	require.GreaterOrEqual(t, len(sink.sessionCloses), 2, "session closes")
	require.GreaterOrEqual(t, len(cluster.broadcasts), 2, "cluster broadcasts")
	// Second delete is a no-op success; cache must remain absent.
	_, err := cache.Get(ctx, authenticationCachePrefix+hash)
	require.ErrorIs(t, err, ErrAuthenticationCacheMiss, "cache after duplicate revocation")
}

func TestMissedAuthorizationInvalidationStillUsesCurrentStoreState(t *testing.T) {
	t.Parallel()

	// Authorization does not cache decisions. Ending a binding on one node
	// without delivering the cluster invalidation still denies the action on
	// the peer, because every Can() resolves active bindings from the store.
	institutionID := model.NewID()
	userID := model.NewID()
	roleID := model.NewID()
	bindingID := model.NewID()
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	role := &model.Role{
		ID:          model.RoleID(roleID),
		Name:        "recovery_admin",
		DisplayName: "Recovery Admin",
		Permissions: []string{string(model.ActionInstitutionManage)},
	}
	binding := &model.RoleBinding{
		ID:        model.RoleBindingID(bindingID),
		UserID:    model.UserID(userID),
		RoleID:    model.RoleID(roleID),
		ScopeType: model.RoleScopeInstitution,
		ScopeID:   institutionID,
		StartsAt:  model.TimeFromMillis(now.Add(-time.Hour).UnixMilli()),
	}
	institution := &model.Institution{ID: model.InstitutionID(institutionID), Name: "Recovery University"}

	root := &recoveryAuthorizationStore{
		institution: institution,
		roles:       map[string]*model.Role{roleID: role},
		bindings:    map[string]*model.RoleBinding{bindingID: binding},
	}
	resolver, err := newAccessScopeResolver(accessScopeResolverDependencies{Institutions: root.Institution(), AcademicUnits: recoveryAccessAcademicUnitStore{}, Classes: recoveryAccessClassStore{}, Users: recoveryAccessUserStore{}, ClassMembers: recoveryAccessClassMemberStore{}, Exams: &accessExamAuthoringStoreFake{}, Sittings: &accessExamSittingStoreFake{}, Submissions: &accessExamSubmissionStoreFake{}, AcademicPeriods: &accessAcademicPeriodStoreFake{}})
	require.NoError(t, err)
	audit, err := newAuditService(auditServiceDependencies{Audits: root.Audit(), Institutions: root.Institution(), NodeID: "node-test"})
	require.NoError(t, err)
	authz, err := newAccessControlService(accessControlServiceDependencies{Roles: root.Role(), Bindings: root.RoleBinding(), Resolver: resolver, Audit: audit})
	require.NoError(t, err)
	authz.now = func() time.Time { return now }

	principal := model.Principal{
		UserID:                 model.UserID(userID),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        now,
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID}
	ctx := context.Background()

	allowed, err := authz.Can(ctx, principal, model.ActionInstitutionManage, resource)
	require.NoError(t, err)
	require.True(t, allowed, "initial Can() = false, want true with active binding")

	// Simulate a peer that never receives InvalidateAuthorization: only the
	// durable binding is ended in the shared store.
	delete(root.bindings, bindingID)

	allowed, err = authz.Can(ctx, principal, model.ActionInstitutionManage, resource)
	require.NoError(t, err)
	require.False(t, allowed, "Can() after binding end without cluster message = true, want false")
}

type recoveryAccessAcademicUnitStore struct{ store.AcademicUnitStore }
type recoveryAccessClassStore struct{ store.ClassStore }
type recoveryAccessUserStore struct{ store.UserStore }
type recoveryAccessClassMemberStore struct{ store.ClassMemberStore }

func TestSessionExpiryRejectsDespitePositiveCache(t *testing.T) {
	t.Parallel()

	// Even without invalidation, an old positive cache entry cannot override
	// the Session's authoritative absolute or idle expiry.
	storeFake := newAuthenticationStoreFake()
	cache := newAuthenticationCacheFake()
	service := newTestAuthenticationServiceWithCache(t, storeFake, cache)
	service.deps.SessionPolicy.AccessTTL = time.Hour
	service.deps.SessionPolicy.IdleTTL = time.Hour
	service.deps.SessionPolicy.AbsoluteTTL = time.Hour

	fixedNow := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	service.deps.Now = func() time.Time { return fixedNow }

	_, rawAccess := seedAuthenticatedSession(t, service)
	ctx := context.Background()
	_, err := service.authenticateAccess(ctx, rawAccess)
	require.NoError(t, err)

	// Keep a valid snapshot while the durable Session expires.
	for _, session := range storeFake.sessions {
		for _, credential := range storeFake.accessByHash {
			if credential.SessionID != session.ID {
				continue
			}
			user := storeFake.users[session.UserID.String()]
			cacheLegacyAuthentication(t, cache, credential, session, user)
			session.ExpiresAt = fixedNow.Add(-time.Minute)
			session.IdleExpiresAt = fixedNow.Add(-time.Minute)
		}
	}
	_, err = service.authenticateAccess(ctx, rawAccess)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "auth with expired authoritative Session error = %v", err)
}

func TestDuplicateRealtimePeerPublicationDoesNotRebroadcast(t *testing.T) {
	t.Parallel()

	// Lost or duplicated cluster realtime publications must never rebroadcast
	// and must not invent durable security state. Peers only apply locally.
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	require.NoError(t, service.AttachRealtimeClusterFanout(cluster))
	require.NoError(t, service.AttachRealtimeSink(sink))

	unitID := model.NewID()
	event := realtime.RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	}
	require.NoError(t, service.PublishRealtimeEvent(context.Background(), event))
	require.Len(t, cluster.broadcasts, 1, "broadcasts")
	payload := cluster.broadcasts[0].data

	// Duplicate peer deliveries (Memberlist non-guarantee).
	peerHandler := cluster.handlers["websocket.publish"]
	require.NotNil(t, peerHandler, "ordinary publication peer handler was not registered")
	require.NoError(t, peerHandler(context.Background(), payload))
	require.NoError(t, peerHandler(context.Background(), payload))
	require.Len(t, cluster.broadcasts, 1, "peer path rebroadcast: broadcasts")
	// Local sink may observe duplicates; durable correctness is HTTP resync.
	require.GreaterOrEqual(t, len(sink.events), 3, "local events")
}

func seedAuthenticatedSession(
	t *testing.T,
	service *authenticationService,
) (*model.User, string) {
	t.Helper()
	password := "CorrectHorseBatteryStaple1!"
	suffix := model.NewID()
	user, err := service.CreateLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{
			Username: "cluster-user-" + suffix,
			Email:    "cluster-user-" + suffix + "@example.edu",
		},
		Password: password,
	})
	require.NoError(t, err)
	result, err := service.Login(context.Background(), invocation.Invocation{}, LoginCommand{
		LoginID:    user.Username,
		Password:   password,
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1",
	})
	require.NoError(t, err)
	require.NotNil(t, result.Tokens)
	require.NotEmpty(t, result.Tokens.AccessToken)
	return user, result.Tokens.AccessToken
}

// recoveryAuthorizationStore is a minimal root store for authorization
// recovery proofs. Only Institution, Role, and RoleBinding are exercised.
type recoveryAuthorizationStore struct {
	institution *model.Institution
	roles       map[string]*model.Role
	bindings    map[string]*model.RoleBinding
}

func (s *recoveryAuthorizationStore) File() store.FileStore { return nil }
func (s *recoveryAuthorizationStore) Job() store.JobStore   { return nil }

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
func (s *recoveryAuthorizationStore) Audit() store.AuditStore                           { return recoveryAuditStore{} }
func (s *recoveryAuthorizationStore) Installation() store.InstallationStore             { return nil }
func (s *recoveryAuthorizationStore) ClusterDiscovery() store.ClusterDiscoveryStore     { return nil }
func (s *recoveryAuthorizationStore) Ping(context.Context) error                        { return nil }
func (s *recoveryAuthorizationStore) GetDBSchemaVersion(context.Context) (int, error)   { return 0, nil }
func (s *recoveryAuthorizationStore) GetLocalSchemaVersion() (int, error)               { return 0, nil }
func (s *recoveryAuthorizationStore) ValidateSchema(context.Context) error              { return nil }
func (s *recoveryAuthorizationStore) Close() error                                      { return nil }

type recoveryInstitutionStore struct{ root *recoveryAuthorizationStore }
type recoveryAuditStore struct{ store.AuditStore }

func (s recoveryInstitutionStore) Get(_ context.Context, id model.InstitutionID) (*model.Institution, error) {
	if s.root.institution == nil || s.root.institution.ID != id {
		return nil, store.NewErrNotFound("institution", id.String())
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
func (recoveryInstitutionStore) Update(context.Context, *store.InstitutionUpdate) (*model.Institution, error) {
	return nil, errors.New("unused")
}
func (recoveryInstitutionStore) Archive(ctx context.Context, input *store.InstitutionArchive) error {
	return errors.New("unused")
}

type recoveryRoleStore struct{ root *recoveryAuthorizationStore }

func (s recoveryRoleStore) GetIncludingArchived(ctx context.Context, id model.RoleID) (*model.Role, error) {
	return s.Get(ctx, id)
}

func (s recoveryRoleStore) GetByIDs(_ context.Context, ids []model.RoleID) ([]*model.Role, error) {
	out := make([]*model.Role, 0, len(ids))
	for _, id := range ids {
		role, ok := s.root.roles[id.String()]
		if !ok {
			continue
		}
		cloned := *role
		out = append(out, &cloned)
	}
	return out, nil
}
func (recoveryRoleStore) Save(context.Context, *store.RoleCreation) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) SeedUpdate(context.Context, *model.Role) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) SeedBuiltIn(context.Context, *model.Role) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Get(context.Context, model.RoleID) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) GetByName(context.Context, string) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) List(context.Context) ([]*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Update(context.Context, *store.RoleUpdate) (*model.Role, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleStore) Archive(context.Context, *store.RoleArchive) (*model.Role, error) {
	return nil, errors.New("unused")
}

type recoveryRoleBindingStore struct{ root *recoveryAuthorizationStore }

func (s recoveryRoleBindingStore) ListActiveByUser(_ context.Context, userID model.UserID, at time.Time) ([]*model.RoleBinding, error) {
	out := make([]*model.RoleBinding, 0)
	for _, binding := range s.root.bindings {
		if binding.UserID != userID {
			continue
		}
		if !binding.IsActiveAt(at) {
			continue
		}
		cloned := *binding
		out = append(out, &cloned)
	}
	return out, nil
}
func (recoveryRoleBindingStore) Save(context.Context, *store.RoleBindingCreation) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) Get(context.Context, model.RoleBindingID) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) ListByUser(context.Context, model.UserID) ([]*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) ListVisibleByUser(context.Context, model.UserID, store.UserVisibilityScope) ([]*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
func (recoveryRoleBindingStore) End(context.Context, *store.RoleBindingEnd) (*model.RoleBinding, error) {
	return nil, errors.New("unused")
}
