//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPrivilegedAuditListingIsScopedAndDurablyAudited(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
	)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	require.NoError(t, err)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "security-auditor", Email: "security-auditor@example.edu",
		DisplayName: "Security Auditor",
	}, Password: password})
	require.NoError(t, appErr)
	login := loginIntegrationUser(
		t, helper.Handler(), user.Username, password,
		model.SessionClientCLI, "audit-cli",
	)

	denied := performJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/audits", nil,
		login.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusForbidden, denied.Code, "unbound audit listing status = %d: %s", denied.Code, denied.Body.String())

	role, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "security-auditor", DisplayName: "Security Auditor",
		Permissions: []string{string(model.ActionAuditView)},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis()),
	})
	require.NoError(t, err)

	allowed := performJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/audits?limit=20", nil,
		login.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, allowed.Code, "authorized audit listing status = %d: %s", allowed.Code, allowed.Body.String())
	var response struct {
		Events []struct {
			ActorID   string `json:"actor_id"`
			SessionID string `json:"session_id"`
			Action    string `json:"action"`
			Resource  struct {
				Type model.ResourceType `json:"type"`
				ID   string             `json:"id"`
			} `json:"resource"`
			Status    model.AuditStatus `json:"status"`
			RequestID string            `json:"request_id"`
			NodeID    string            `json:"node_id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(allowed.Body.Bytes(), &response))
	require.GreaterOrEqual(t, len(response.Events), 2, "audit events")
	decisions := map[string]bool{}
	for _, event := range response.Events {
		if event.Action == storetest.FixtureAuditAction {
			continue
		}
		if (event.Action == string(model.ActionAuditView) || event.Action == string(model.ActionAcademicAuditView)) &&
			event.ActorID == user.ID.String() &&
			event.Resource.ID == institution.ID.String() {
			decisions[event.Action+":"+string(event.Status)] = true
		}
		// A password login attempt is recorded before any Session exists; its
		// success projection names the issued Session instead.
		preSession := event.Action == "authentication.login"
		require.False(t, event.SessionID == "" && !preSession)
		require.NotEmpty(t, event.NodeID)
		require.NotEmpty(t, event.RequestID)
	}
	require.True(t, decisions[string(model.ActionAcademicAuditView)+":"+string(model.AuditStatusFail)])
	require.True(t, decisions[string(model.ActionAuditView)+":"+string(model.AuditStatusSuccess)])
}

func TestAuthorizationResolvesCurrentAcademicHierarchy(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
	)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	require.NoError(t, err)
	root, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "engineering", DisplayName: "Engineering",
	})
	require.NoError(t, err)
	child, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, ParentID: root.ID,
		Name: "computing", DisplayName: "Computing",
	})
	require.NoError(t, err)
	sibling, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "humanities", DisplayName: "Humanities",
	})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: child.ID, Name: "computer-science",
		DisplayName: "Computer Science",
	})
	require.NoError(t, err)
	siblingProgramme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: sibling.ID, Name: "history", DisplayName: "History",
	})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1",
	})
	require.NoError(t, err)
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "2026-2027", DisplayName: "2026-2027",
		StartsAt: model.TimeFromMillis(1_800_000_000_000), EndsAt: model.TimeFromMillis(1_830_000_000_000),
	})
	require.NoError(t, err)
	class, err := storetest.SaveClass(ctx, persistence, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "class-a", DisplayName: "Class A",
	})
	require.NoError(t, err)
	password := "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "hierarchy-teacher", Email: "hierarchy-teacher@example.edu",
		DisplayName: "Hierarchy Teacher",
	}, Password: password})
	require.NoError(t, err)
	login := loginIntegrationUser(
		t, helper.Handler(), user.Username, password,
		model.SessionClientCLI, "hierarchy-device",
	)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	_, err = persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: root.ID,
		UserID:         user.ID,
		StartsAt:       model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)
	// Keep permission results on a distinct error variable from CreateLocalUser
	// / AuthenticateAccess so typed-nil *apperror.Error values cannot poison later
	// nil checks through a shared interface variable.
	allowed, permErr := helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	require.NoError(t, permErr, "academic-unit membership unexpectedly granted class permission")
	require.False(t, allowed, "academic-unit membership unexpectedly granted class permission")
	role, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "academic-unit-teacher", DisplayName: "Academic Unit Teacher",
		Permissions: []string{
			string(model.ActionProgrammeView), string(model.ActionProgrammeLevelView),
			string(model.ActionClassView),
		},
	})
	require.NoError(t, err)
	binding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	require.NoError(t, permErr, "ancestor class permission")
	require.True(t, allowed, "ancestor class permission")
	for _, check := range []struct {
		action   model.Action
		resource model.Resource
	}{
		{model.ActionProgrammeView, model.Resource{Type: model.ResourceProgramme, ID: programme.ID.String()}},
		{model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgrammeLevel, ID: level.ID.String()}},
	} {
		t.Run(string(check.action), func(t *testing.T) {
			allowed, permErr = helper.App.Can(ctx, *principal, check.action, check.resource)
			require.NoError(t, permErr, "descendant %s permission", check.action)
			require.True(t, allowed, "descendant %s permission", check.action)
		})
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionProgrammeView,
		model.Resource{Type: model.ResourceProgramme, ID: siblingProgramme.ID.String()},
	)
	require.NoError(t, permErr, "sibling programme permission")
	require.False(t, allowed, "sibling programme permission")
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionInstitutionManage,
		model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
	)
	require.NoError(t, permErr, "lower-scope institution permission")
	require.False(t, allowed, "lower-scope institution permission")
	_, err = storetest.EndRoleBinding(ctx, persistence, binding.ID, model.GetMillis())
	require.NoError(t, err)
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	require.NoError(t, permErr, "ended binding permission")
	require.False(t, allowed, "ended binding permission")
}

func TestPrincipalPermissionAndUserVisibilityPolicies(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
	)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	require.NoError(t, err)
	password := "correct horse battery staple"
	viewer, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "directory-viewer", Email: "directory-viewer@example.edu",
		DisplayName: "Directory Viewer",
	}, Password: password})
	require.NoError(t, err)
	target, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "directory-target", Email: "directory-target@example.edu",
		DisplayName: "Directory Target",
	}, Password: password})
	require.NoError(t, err)
	login := loginIntegrationUser(
		t, helper.Handler(), viewer.Username, password,
		model.SessionClientCLI, "directory-device",
	)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	metadata := model.RequestMetadata{
		RequestID: model.NewID(),
		IPAddress: "127.0.0.1",
		UserAgent: "proctor-authorization-integration-test",
	}

	// Generic policy composition remains available through Can; contextual
	// teacher-to-student visibility is owned by the focused User Profile use case.
	allowed, permErr := helper.App.Can(
		ctx, *principal, model.ActionUserView,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()},
	)
	require.NoError(t, permErr, "self visibility")
	require.True(t, allowed, "self visibility")
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionUserManage,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()},
	)
	require.NoError(t, permErr, "self management without permission")
	require.False(t, allowed, "self management without permission")
	require.NoError(t, helper.App.Authorize(
		ctx, invocation.New(*principal, metadata), model.ActionUserProfilePictureManage,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()},
	))
	authErr := helper.App.Authorize(
		ctx, invocation.New(*principal, metadata), model.ActionUserProfilePictureManage,
		model.Resource{Type: model.ResourceUser, ID: target.ID.String()},
	)
	require.True(t, apperror.Is(authErr, "authorization.denied"), "cross-user profile-picture authorization = %v", authErr)
	_, err = helper.App.GetUserProfile(
		ctx, invocation.New(*principal, metadata), app.GetUserProfileQuery{ID: target.ID},
	)
	require.True(t, apperror.Is(err, "authorization.denied"), "unbound cross-user read error = %v", err)

	role, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "institution-directory-viewer", DisplayName: "Institution Directory Viewer",
		Permissions: []string{string(model.ActionUserView)},
	})
	require.NoError(t, err)
	binding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: viewer.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)

	visible, err := helper.App.GetUserProfile(
		ctx, invocation.New(*principal, metadata), app.GetUserProfileQuery{ID: target.ID},
	)
	require.NoError(t, err, "authorized cross-user read = %#v", visible)
	require.Equal(t, target.ID, visible.ID, "authorized cross-user read = %#v", visible)
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionUserManage,
		model.Resource{Type: model.ResourceUser, ID: target.ID.String()},
	)
	require.NoError(t, permErr, "view role unexpectedly granted management")
	require.False(t, allowed, "view role unexpectedly granted management")

	_, err = storetest.EndRoleBinding(ctx, persistence, binding.ID, model.GetMillis())
	require.NoError(t, err)
	_, err = helper.App.GetUserProfile(
		ctx, invocation.New(*principal, metadata), app.GetUserProfileQuery{ID: target.ID},
	)
	require.True(t, apperror.Is(err, "authorization.denied"), "ended binding cross-user read error = %v", err)

	events, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: viewer.ID,
		Action:  string(model.ActionUserView),
		Resource: &model.Resource{
			Type: model.ResourceUser,
			ID:   target.ID.String(),
		},
		Limit:      20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	statuses := map[model.AuditStatus]int{}
	for _, event := range events {
		statuses[event.Status]++
		require.Equal(t, model.ResourceUser, event.Resource.Type)
		require.Equal(t, target.ID.String(), event.Resource.ID)
		require.Equal(t, model.RoleScopeInstitution, event.ScopeType)
		require.Equal(t, institution.ID.String(), event.ScopeID)
	}
	require.Equal(t, 1, statuses[model.AuditStatusSuccess])
	require.Equal(t, 2, statuses[model.AuditStatusFail])
}

func TestAcademicUnitVisibilityIsBoundedAcrossUsersBindingsAndAudits(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	now := model.GetMillis()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	require.NoError(t, err)
	root, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "engineering", DisplayName: "Engineering",
	})
	require.NoError(t, err)
	child, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, ParentID: root.ID,
		Name: "computing", DisplayName: "Computing",
	})
	require.NoError(t, err)
	sibling, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "humanities", DisplayName: "Humanities",
	})
	require.NoError(t, err)
	otherRoot, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "medicine", DisplayName: "Medicine",
	})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: child.ID, Name: "computer-science", DisplayName: "Computer Science",
	})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1",
	})
	require.NoError(t, err)
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:  "2026-2027", DisplayName: "2026-2027",
		StartsAt: model.TimeFromMillis(now - 86_400_000),
		EndsAt:   model.TimeFromMillis(now + 31_536_000_000),
	})
	require.NoError(t, err)
	class, err := storetest.SaveClass(ctx, persistence, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "class-a", DisplayName: "Class A",
	})
	require.NoError(t, err)

	password := "correct horse battery staple"
	createUser := func(username string) *model.User {
		t.Helper()
		user, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
			Username: username, Email: username + "@example.edu", DisplayName: username,
			EmailVerified: true, Locale: "fr", Timezone: "Europe/Paris",
		}, Password: password})
		require.NoError(t, appErr)
		return user
	}
	viewer := createUser("scoped-viewer")
	unitUser := createUser("scoped-unit-user")
	classUser := createUser("scoped-class-user")
	bindingUser := createUser("scoped-binding-user")
	archivedRoleUser := createUser("scoped-archived-role-user")
	siblingUser := createUser("scoped-sibling-user")
	historicalUser := createUser("scoped-historical-user")

	for _, membership := range []*model.AcademicUnitMember{
		{AcademicUnitID: child.ID, UserID: unitUser.ID, StartsAt: model.TimeFromMillis(now - 10_000)},
		{AcademicUnitID: sibling.ID, UserID: siblingUser.ID, StartsAt: model.TimeFromMillis(now - 10_000)},
		{AcademicUnitID: child.ID, UserID: historicalUser.ID, StartsAt: model.TimeFromMillis(now - 20_000), EndsAt: model.OptionalTimeFromMillis(now - 10_000)},
	} {
		_, err := persistence.AcademicUnitMember().Save(ctx, membership)
		require.NoError(t, err)
	}
	_, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: classUser.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	_, err = persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: classUser.ID, StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)

	visibilityRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "academic-directory-auditor", DisplayName: "Academic Directory Auditor",
		Permissions: []string{
			string(model.ActionUserView), string(model.ActionRoleBindingView),
			string(model.ActionAcademicAuditView),
		},
	})
	require.NoError(t, err)
	targetRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "visibility-fixture-role", DisplayName: "Visibility Fixture Role",
		Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	archivedTargetRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "archived-visibility-fixture-role", DisplayName: "Archived Visibility Fixture Role",
		Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: archivedRoleUser.ID, RoleID: archivedTargetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 20_000),
	})
	require.NoError(t, err)
	_, err = storetest.ArchiveRole(ctx, persistence, archivedTargetRole.ID, model.GetMillis())
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: viewer.ID, RoleID: visibilityRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: viewer.ID, RoleID: visibilityRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: otherRoot.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	visibleBinding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 20_000),
	})
	require.NoError(t, err)
	historicalBinding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 40_000), EndsAt: model.OptionalTimeFromMillis(now - 30_000),
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: sibling.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)

	login := loginIntegrationUser(t, helper.Handler(), viewer.Username, password, model.SessionClientCLI, "scoped-device")
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, appErr)
	invocation := invocation.New(*principal, model.RequestMetadata{
		RequestID: model.NewID(), IPAddress: "127.0.0.1", UserAgent: "access-visibility-integration-test",
	})

	search := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users?limit=50", nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, search.Code, "scoped user search status = %d: %s", search.Code, search.Body.String())
	var directory []map[string]any
	require.NoError(t, json.Unmarshal(integrationListItems(t, search.Body.Bytes()), &directory))
	wanted := map[string]bool{
		unitUser.ID.String(): false, classUser.ID.String(): false, bindingUser.ID.String(): false,
	}
	for _, user := range directory {
		id, _ := user["id"].(string)
		if _, ok := wanted[id]; ok {
			wanted[id] = true
		}
		for _, field := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
			_, leaked := user[field]
			require.False(t, leaked, "scoped user %s leaked %s: %#v", id, field, user)
		}
		require.NotEqual(t, siblingUser.ID.String(), id)
		require.NotEqual(t, historicalUser.ID.String(), id)
		require.NotEqual(t, archivedRoleUser.ID.String(), id)
	}
	for id, seen := range wanted {
		assert.True(t, seen, "scoped search omitted visible user %s: %#v", id, directory)
	}

	classMembersRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "scoped-class-roster-viewer", DisplayName: "Scoped Class Roster Viewer",
		Permissions: []string{string(model.ActionClassMembersView)},
	})
	require.NoError(t, err)
	classMembersViewer := createUser("scoped-class-roster-viewer")
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: classMembersViewer.ID, RoleID: classMembersRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	classMembersLogin := loginIntegrationUser(
		t, helper.Handler(), classMembersViewer.Username, password,
		model.SessionClientCLI, "scoped-class-roster-device",
	)
	assertClassMemberDirectory := func(label, credential string) {
		t.Helper()
		response := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users?limit=50", nil, credential)
		require.Equal(t, http.StatusOK, response.Code, "%s class-member search status = %d: %s", label, response.Code, response.Body.String())
		var users []map[string]any
		require.NoError(t, json.Unmarshal(integrationListItems(t, response.Body.Bytes()), &users))
		seenClassMember := false
		for _, user := range users {
			id, _ := user["id"].(string)
			if id == classUser.ID.String() {
				seenClassMember = true
			}
			require.NotEqual(t, unitUser.ID.String(), id, "%s class-member search leaked non-roster User %s: %#v", label, id, users)
			require.NotEqual(t, bindingUser.ID.String(), id, "%s class-member search leaked non-roster User %s: %#v", label, id, users)
			require.NotEqual(t, archivedRoleUser.ID.String(), id, "%s class-member search leaked non-roster User %s: %#v", label, id, users)
			require.NotEqual(t, siblingUser.ID.String(), id, "%s class-member search leaked non-roster User %s: %#v", label, id, users)
			for _, field := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
				_, leaked := user[field]
				require.False(t, leaked, "%s class-member User %s leaked %s: %#v", label, id, field, user)
			}
		}
		require.True(t, seenClassMember, "%s class-member search omitted current Class member %s: %#v", label, classUser.ID, users)
	}
	assertClassMemberDirectory("academic-unit", classMembersLogin.Tokens.AccessToken)
	classMemberRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+classUser.ID.String(), nil, classMembersLogin.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, classMemberRead.Code, "class-member exact read status = %d: %s", classMemberRead.Code, classMemberRead.Body.String())
	classMemberPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+classUser.ID.String()+"/profile-picture?size=128", nil, classMembersLogin.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, classMemberPicture.Code, "class-member profile-picture status = %d: %s", classMemberPicture.Code, classMemberPicture.Body.String())
	for label, userID := range map[string]string{
		"academic-unit member": unitUser.ID.String(),
		"active Role holder":   bindingUser.ID.String(),
		"archived Role holder": archivedRoleUser.ID.String(),
	} {
		nonMemberRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+userID, nil, classMembersLogin.Tokens.AccessToken)
		require.Equal(t, http.StatusForbidden, nonMemberRead.Code, "class-member %s non-roster read status = %d: %s", label, nonMemberRead.Code, nonMemberRead.Body.String())
		nonMemberPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+userID+"/profile-picture?size=128", nil, classMembersLogin.Tokens.AccessToken)
		require.Equal(t, http.StatusForbidden, nonMemberPicture.Code, "class-member %s non-roster profile-picture status = %d: %s", label, nonMemberPicture.Code, nonMemberPicture.Body.String())
	}

	institutionClassMembersViewer := createUser("institution-class-roster-viewer")
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: institutionClassMembersViewer.ID, RoleID: classMembersRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	institutionClassMembersLogin := loginIntegrationUser(
		t, helper.Handler(), institutionClassMembersViewer.Username, password,
		model.SessionClientCLI, "institution-class-roster-device",
	)
	assertClassMemberDirectory("institution", institutionClassMembersLogin.Tokens.AccessToken)

	unitRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String(), nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, unitRead.Code, "descendant user read status = %d: %s", unitRead.Code, unitRead.Body.String())
	readDecisions, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: viewer.ID, Action: string(model.ActionUserView),
		Resource: &model.Resource{Type: model.ResourceUser, ID: unitUser.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, readDecisions, 1, "multi-root user read decision = %#v, want matched root %s", readDecisions, root.ID)
	require.Equal(t, model.AuditStatusSuccess, readDecisions[0].Status, "multi-root user read decision = %#v, want matched root %s", readDecisions, root.ID)
	require.Equal(t, model.RoleScopeAcademicUnit, readDecisions[0].ScopeType, "multi-root user read decision = %#v, want matched root %s", readDecisions, root.ID)
	require.Equal(t, root.ID.String(), readDecisions[0].ScopeID, "multi-root user read decision = %#v, want matched root %s", readDecisions, root.ID)
	disableAt := model.GetMillis()
	disableAudit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: viewer.ID, Action: string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: unitUser.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusAttempt, NodeID: "access-visibility-integration",
	})
	require.NoError(t, err)
	_, err = persistence.User().SetDisabled(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: unitUser.ID, ExpectedRevision: unitUser.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: disableAudit.ID, AuditAt: disableAt,
	}))
	require.NoError(t, err)
	for _, path := range []string{"/api/v1/users?limit=50", "/api/v1/users?limit=50&include_disabled=true"} {
		response := performJSONRequest(helper.Handler(), http.MethodGet, path, nil, login.Tokens.AccessToken)
		require.Equal(t, http.StatusOK, response.Code, "scoped disabled search %q status = %d: %s", path, response.Code, response.Body.String())
		var users []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(integrationListItems(t, response.Body.Bytes()), &users))
		for _, user := range users {
			require.NotEqual(t, unitUser.ID.String(), user.ID, "scoped disabled search %q exposed User", path)
		}
	}
	disabledRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String(), nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, disabledRead.Code, "disabled exact read status = %d: %s", disabledRead.Code, disabledRead.Body.String())
	disabledPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String()+"/profile-picture?size=128", nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, disabledPicture.Code, "disabled profile-picture status = %d: %s", disabledPicture.Code, disabledPicture.Body.String())
	globalViewer := createUser("institution-directory-viewer")
	globalRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "institution-directory-viewer", DisplayName: "Institution Directory Viewer",
		Permissions: []string{string(model.ActionUserView)},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: globalViewer.ID, RoleID: globalRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis()),
	})
	require.NoError(t, err)
	globalLogin := loginIntegrationUser(t, helper.Handler(), globalViewer.Username, password, model.SessionClientCLI, "institution-directory-device")
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/api/v1/users?limit=50"},
		{path: "/api/v1/users?limit=50&include_disabled=true", want: true},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := performJSONRequest(helper.Handler(), http.MethodGet, test.path, nil, globalLogin.Tokens.AccessToken)
			require.Equal(t, http.StatusOK, response.Code, "institution-wide disabled search %q status = %d: %s", test.path, response.Code, response.Body.String())
			var users []struct {
				ID         string    `json:"id"`
				DisabledAt time.Time `json:"disabled_at"`
			}
			require.NoError(t, json.Unmarshal(integrationListItems(t, response.Body.Bytes()), &users))
			found := false
			for _, user := range users {
				if user.ID == unitUser.ID.String() {
					found = true
					require.False(t, user.DisabledAt.IsZero())
				}
			}
			require.Equal(t, test.want, found, "institution-wide disabled search %q found", test.path)
		})
	}
	siblingRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+siblingUser.ID.String(), nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, siblingRead.Code, "sibling user read status = %d: %s", siblingRead.Code, siblingRead.Body.String())
	historicalRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+historicalUser.ID.String(), nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, historicalRead.Code, "historical user read status = %d: %s", historicalRead.Code, historicalRead.Body.String())

	bindings, listErr := helper.App.ListRoleBindings(ctx, invocation, app.ListRoleBindingsQuery{UserID: bindingUser.ID.String()})
	require.NoError(t, listErr)
	seenBindings := map[string]bool{}
	for _, binding := range bindings {
		seenBindings[binding.ID.String()] = true
		require.Equal(t, model.RoleScopeAcademicUnit, binding.ScopeType)
		require.Equal(t, child.ID.String(), binding.ScopeID)
	}
	require.True(t, seenBindings[visibleBinding.ID.String()])
	require.True(t, seenBindings[historicalBinding.ID.String()])

	saveAudit := func(action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string) string {
		t.Helper()
		event, err := persistence.Audit().Save(ctx, &model.AuditEvent{
			ActorID: viewer.ID, Action: string(action), Resource: resource,
			ScopeType: scopeType, ScopeID: scopeID, Status: model.AuditStatusSuccess,
			RequestID: model.NewID(), NodeID: "visibility-node", ClientType: "cli",
			AuthMethod: "bearer", IPAddress: "127.0.0.1", UserAgent: "sensitive-agent",
			Parameters: json.RawMessage(`{"fixture":true}`),
		})
		require.NoError(t, err)
		return event.ID.String()
	}
	childAuditID := saveAudit(model.ActionAcademicUnitManage, model.Resource{Type: model.ResourceAcademicUnit, ID: child.ID.String()}, model.RoleScopeAcademicUnit, child.ID.String())
	classAuditID := saveAudit(model.ActionClassManage, model.Resource{Type: model.ResourceClass, ID: class.ID.String()}, model.RoleScopeClass, class.ID.String())
	siblingAuditID := saveAudit(model.ActionAcademicUnitManage, model.Resource{Type: model.ResourceAcademicUnit, ID: sibling.ID.String()}, model.RoleScopeAcademicUnit, sibling.ID.String())
	securityAuditID := saveAudit(model.ActionExternalIdentityManage, model.Resource{Type: model.ResourceUser, ID: bindingUser.ID.String()}, model.RoleScopeAcademicUnit, child.ID.String())

	audits := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/audits?limit=100", nil, login.Tokens.AccessToken)
	require.Equal(t, http.StatusOK, audits.Code, "scoped audit list status = %d: %s", audits.Code, audits.Body.String())
	var auditBody struct {
		Events []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(audits.Body.Bytes(), &auditBody))
	seenAudits := map[string]bool{}
	for _, event := range auditBody.Events {
		id, _ := event["id"].(string)
		seenAudits[id] = true
		for _, field := range []string{"session_id", "request_id", "node_id", "client_type", "authentication_method", "ip_address", "user_agent"} {
			_, leaked := event[field]
			require.False(t, leaked, "scoped audit %s leaked %s: %#v", id, field, event)
		}
	}
	require.True(t, seenAudits[childAuditID])
	require.True(t, seenAudits[classAuditID])
	require.False(t, seenAudits[siblingAuditID])
	require.False(t, seenAudits[securityAuditID])
	institutionAcademicViewer := createUser("institution-academic-auditor")
	institutionAcademicRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "institution-academic-auditor", DisplayName: "Institution Academic Auditor",
		Permissions: []string{string(model.ActionAcademicAuditView)},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: institutionAcademicViewer.ID, RoleID: institutionAcademicRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis()),
	})
	require.NoError(t, err)
	institutionAcademicLogin := loginIntegrationUser(
		t, helper.Handler(), institutionAcademicViewer.Username, password,
		model.SessionClientCLI, "institution-academic-audit-device",
	)
	assertInstitutionAcademicAudits := func(label, credential string) {
		t.Helper()
		response := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/audits?limit=100", nil, credential)
		require.Equal(t, http.StatusOK, response.Code, "%s academic audit status = %d: %s", label, response.Code, response.Body.String())
		var body struct {
			Events []map[string]any `json:"items"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		seen := map[string]bool{}
		for _, event := range body.Events {
			id, _ := event["id"].(string)
			seen[id] = true
			for _, field := range []string{"session_id", "request_id", "node_id", "client_type", "authentication_method", "ip_address", "user_agent"} {
				_, leaked := event[field]
				require.False(t, leaked, "%s academic audit %s leaked %s: %#v", label, id, field, event)
			}
		}
		require.True(t, seen[childAuditID], "%s academic audit omitted cross-unit history: %#v", label, seen)
		require.True(t, seen[classAuditID], "%s academic audit omitted cross-unit history: %#v", label, seen)
		require.True(t, seen[siblingAuditID], "%s academic audit omitted cross-unit history: %#v", label, seen)
		require.False(t, seen[securityAuditID], "%s academic audit leaked security history: %#v", label, seen)
	}
	assertInstitutionAcademicAudits("session", institutionAcademicLogin.Tokens.AccessToken)
	createdToken := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/me/tokens", map[string]any{
		"description": "academic audit automation",
		"scopes":      []string{string(model.ActionAcademicAuditView)},
		"expires_at":  integrationWireTime(model.GetMillis() + 2*60*60*1000),
	}, institutionAcademicLogin.Tokens.AccessToken)
	require.Equal(t, http.StatusCreated, createdToken.Code, "create academic audit PAT status = %d: %s", createdToken.Code, createdToken.Body.String())
	var tokenBody struct {
		Credential string `json:"credential"`
	}
	require.NoError(t, json.Unmarshal(createdToken.Body.Bytes(), &tokenBody), "create academic audit PAT response = %s", createdToken.Body.String())
	require.NotEmpty(t, tokenBody.Credential, "create academic audit PAT response = %s", createdToken.Body.String())
	assertInstitutionAcademicAudits("personal access token", tokenBody.Credential)
	decisions, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: institutionAcademicViewer.ID, Action: string(model.ActionAcademicAuditView), Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(decisions), 2, "institution academic audit decisions")
	for _, decision := range decisions {
		require.Equal(t, model.AuditStatusSuccess, decision.Status)
		require.Equal(t, model.RoleScopeInstitution, decision.ScopeType)
		require.Equal(t, institution.ID.String(), decision.ScopeID)
	}
}
