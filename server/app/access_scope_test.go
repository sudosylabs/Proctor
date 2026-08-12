// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAccessScopeResolverRejectsArchivedAndIncompatibleResources(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.NewInstitutionID(), ArchivedAt: model.OptionalTimeFrom(time.Now())}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: institution},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}); !Is(err, "resource.not_found") {
		t.Fatalf("archived institution error = %v", err)
	}
	if _, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceType("exam"), ID: model.NewId()}); !Is(err, "authorization.request.invalid") {
		t.Fatalf("incompatible resource error = %v", err)
	}
}

func TestAccessScopeConstraintsAreBoundedAndRespectPATCeiling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	institutionID := model.NewInstitutionID()
	rootID := model.NewAcademicUnitID()
	childID := model.NewAcademicUnitID()
	roleID := model.RoleID(model.NewId())
	userID := model.NewUserID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			childID.String(): {{ID: rootID, InstitutionID: institutionID}, {ID: childID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{string(model.ActionAcademicUnitView)}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{RoleID: roleID, UserID: userID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: childID.String()}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization.now = func() time.Time { return now }
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType:       model.CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token", ClientType: model.SessionClientCLI,
		CredentialScopes: []string{string(model.ActionAcademicUnitView)}, AcademicUnitID: rootID,
	}
	query, err := authorization.authorizedScopes(
		context.Background(), principal, model.ActionAcademicUnitView,
		model.ResourceAcademicUnit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.AcademicUnitRootIDs) != 1 || query.AcademicUnitRootIDs[0] != childID.String() || query.InstitutionWide {
		t.Fatalf("query = %#v", query)
	}
	principal.CredentialScopes = []string{string(model.ActionClassView)}
	query, err = authorization.authorizedScopes(context.Background(), principal, model.ActionAcademicUnitView, model.ResourceAcademicUnit)
	if err != nil || query.InstitutionWide || len(query.AcademicUnitRootIDs) != 0 {
		t.Fatalf("ceiling query=%#v err=%v", query, err)
	}
	if _, err = authorization.authorizedScopes(context.Background(), principal, model.ActionAcademicUnitView, model.ResourceClass); !Is(err, "authorization.request.invalid") {
		t.Fatalf("compatibility error = %v", err)
	}
}

func TestAccessScopeResolutionFailsClosedOnPersistenceFailure(t *testing.T) {
	t.Parallel()

	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{err: errors.New("database unavailable")},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceInstitution, ID: model.NewId()})
	if !Is(err, "authorization.unavailable") {
		t.Fatalf("error = %v, want authorization.unavailable", err)
	}
}

func TestAccessControlPreservesIntrinsicSelfRead(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.NewInstitutionID()}
	user := &model.User{
		ID: model.NewUserID(), Username: "self-reader", Email: "self@example.edu",
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), Revision: 1,
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: institution},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{},
		&accessUserStoreFake{user: user}, &accessClassMemberStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{}, &accessRoleBindingStoreFake{}, resolver,
		accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: user.ID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess,
		SessionID:      model.SessionID(model.NewId()), AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	resource := model.Resource{Type: model.ResourceUser, ID: user.ID.String()}
	allowed, err := access.Can(context.Background(), principal, model.ActionUserView, resource)
	if err != nil || !allowed {
		t.Fatalf("self read = %v, %v", allowed, err)
	}
	allowed, err = access.Can(context.Background(), principal, model.ActionUserManage, resource)
	if err != nil || allowed {
		t.Fatalf("self management = %v, %v", allowed, err)
	}
}

type accessInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
	err         error
}

func (s *accessInstitutionStoreFake) Get(context.Context, string) (*model.Institution, error) {
	return s.institution, s.err
}
func (s *accessInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, s.err
}

type accessAcademicUnitStoreFake struct {
	store.AcademicUnitStore
	ancestors map[string][]*model.AcademicUnit
}

func (s *accessAcademicUnitStoreFake) ListAncestors(_ context.Context, id string) ([]*model.AcademicUnit, error) {
	return s.ancestors[id], nil
}

type accessClassStoreFake struct{ store.ClassStore }
type accessUserStoreFake struct {
	store.UserStore
	user *model.User
}

func (s *accessUserStoreFake) Get(context.Context, string) (*model.User, error) {
	if s.user == nil {
		return nil, store.NewErrNotFound("user", "")
	}
	return s.user, nil
}

type accessClassMemberStoreFake struct{ store.ClassMemberStore }

type accessRoleStoreFake struct {
	store.RoleStore
	roles []*model.Role
}

func (s *accessRoleStoreFake) GetByIds(context.Context, []string) ([]*model.Role, error) {
	return s.roles, nil
}

type accessRoleBindingStoreFake struct {
	store.RoleBindingStore
	bindings []*model.RoleBinding
}

func (s *accessRoleBindingStoreFake) ListActiveByUser(context.Context, string, int64) ([]*model.RoleBinding, error) {
	return s.bindings, nil
}

type accessDecisionAuditFake struct{}

func (accessDecisionAuditFake) RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error {
	return nil
}

func (accessDecisionAuditFake) RecordUserSearchDecision(context.Context, model.Principal, model.Resource, model.RequestMetadata, bool) error {
	return nil
}
