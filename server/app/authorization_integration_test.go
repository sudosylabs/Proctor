//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPrivilegedAuditListingIsScopedAndDurablyAudited(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "security-auditor", Email: "security-auditor@example.edu",
		DisplayName: "Security Auditor",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	login := loginIntegrationUser(
		t, helper.Handler(), user.Username, password,
		model.SessionClientCLI, "audit-cli",
	)

	denied := performJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/audits", nil,
		login.Tokens.AccessToken,
	)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unbound audit listing status = %d: %s", denied.Code, denied.Body.String())
	}

	role, err := persistence.Role().Save(ctx, &model.Role{
		Name: "security-auditor", DisplayName: "Security Auditor",
		Permissions: []string{string(model.ActionAuditView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis()),
	}); err != nil {
		t.Fatal(err)
	}

	allowed := performJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/audits?limit=20", nil,
		login.Tokens.AccessToken,
	)
	if allowed.Code != http.StatusOK {
		t.Fatalf("authorized audit listing status = %d: %s", allowed.Code, allowed.Body.String())
	}
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
		} `json:"events"`
	}
	if err := json.Unmarshal(allowed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) < 2 {
		t.Fatalf("audit events = %#v", response.Events)
	}
	decisions := map[string]bool{}
	for _, event := range response.Events {
		if (event.Action == string(model.ActionAuditView) || event.Action == string(model.ActionAcademicAuditView)) &&
			event.ActorID == user.ID.String() &&
			event.Resource.ID == institution.ID.String() {
			decisions[event.Action+":"+string(event.Status)] = true
		}
		if event.SessionID == "" || event.NodeID == "" || event.RequestID == "" {
			t.Fatalf("incomplete security audit event = %#v", event)
		}
	}
	if !decisions[string(model.ActionAcademicAuditView)+":"+string(model.AuditStatusFail)] ||
		!decisions[string(model.ActionAuditView)+":"+string(model.AuditStatusSuccess)] {
		t.Fatalf("authorization decisions = %#v", decisions)
	}
}

func TestAuthorizationResolvesCurrentAcademicHierarchy(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	root, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "engineering", DisplayName: "Engineering",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, ParentID: root.ID,
		Name: "computing", DisplayName: "Computing",
	})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "humanities", DisplayName: "Humanities",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: child.ID, Name: "computer-science",
		DisplayName: "Computer Science",
	})
	if err != nil {
		t.Fatal(err)
	}
	siblingProgramme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: sibling.ID, Name: "history", DisplayName: "History",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "2026-2027", DisplayName: "2026-2027",
		StartsAt: model.TimeFromMillis(1_800_000_000_000), EndsAt: model.TimeFromMillis(1_830_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "class-a", DisplayName: "Class A",
	})
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "hierarchy-teacher", Email: "hierarchy-teacher@example.edu",
		DisplayName: "Hierarchy Teacher",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login := loginIntegrationUser(
		t, helper.Handler(), user.Username, password,
		model.SessionClientCLI, "hierarchy-device",
	)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: root.ID,
		UserID:         user.ID,
		StartsAt:       model.TimeFromMillis(model.GetMillis() - 1_000),
	}); err != nil {
		t.Fatal(err)
	}
	// Keep permission results on a distinct error variable from CreateLocalUser
	// / AuthenticateAccess so typed-nil *app.Error values cannot poison later
	// nil checks through a shared interface variable.
	allowed, permErr := helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("academic-unit membership unexpectedly granted class permission = %v, %v", allowed, permErr)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{
		Name: "academic-unit-teacher", DisplayName: "Academic Unit Teacher",
		Permissions: []string{
			string(model.ActionProgrammeView), string(model.ActionProgrammeLevelView),
			string(model.ActionClassView),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	if permErr != nil || !allowed {
		t.Fatalf("ancestor class permission = %v, %v", allowed, permErr)
	}
	for _, check := range []struct {
		action   model.Action
		resource model.Resource
	}{
		{model.ActionProgrammeView, model.Resource{Type: model.ResourceProgramme, ID: programme.ID.String()}},
		{model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgrammeLevel, ID: level.ID.String()}},
	} {
		allowed, permErr = helper.App.Can(ctx, *principal, check.action, check.resource)
		if permErr != nil || !allowed {
			t.Fatalf("descendant %s permission = %v, %v", check.action, allowed, permErr)
		}
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionProgrammeView,
		model.Resource{Type: model.ResourceProgramme, ID: siblingProgramme.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("sibling programme permission = %v, %v", allowed, permErr)
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionInstitutionManage,
		model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("lower-scope institution permission = %v, %v", allowed, permErr)
	}
	if _, err := persistence.RoleBinding().End(ctx, binding.ID.String(), model.GetMillis()); err != nil {
		t.Fatal(err)
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, ID: class.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("ended binding permission = %v, %v", allowed, permErr)
	}
}

func TestPrincipalPermissionAndUserVisibilityPolicies(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	viewer, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "directory-viewer", Email: "directory-viewer@example.edu",
		DisplayName: "Directory Viewer",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	target, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "directory-target", Email: "directory-target@example.edu",
		DisplayName: "Directory Target",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login := loginIntegrationUser(
		t, helper.Handler(), viewer.Username, password,
		model.SessionClientCLI, "directory-device",
	)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	metadata := model.RequestMetadata{
		RequestID: model.NewId(),
		IPAddress: "127.0.0.1",
		UserAgent: "proctor-authorization-integration-test",
	}

	// Generic policy composition remains available through Can; contextual
	// teacher-to-student visibility is owned by the focused User Profile use case.
	allowed, permErr := helper.App.Can(
		ctx, *principal, model.ActionUserView,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()},
	)
	if permErr != nil || !allowed {
		t.Fatalf("self visibility = %v, %v", allowed, permErr)
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionUserManage,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("self management without permission = %v, %v", allowed, permErr)
	}
	if authErr := helper.App.Authorize(
		ctx, *principal, model.ActionUserProfilePictureManage,
		model.Resource{Type: model.ResourceUser, ID: viewer.ID.String()}, metadata,
	); authErr != nil {
		t.Fatalf("self profile-picture authorization = %v", authErr)
	}
	if authErr := helper.App.Authorize(
		ctx, *principal, model.ActionUserProfilePictureManage,
		model.Resource{Type: model.ResourceUser, ID: target.ID.String()}, metadata,
	); !application.Is(authErr, "authorization.denied") {
		t.Fatalf("cross-user profile-picture authorization = %v", authErr)
	}
	if _, err := helper.App.GetUserProfile(
		ctx, application.NewInvocation(*principal, metadata), application.GetUserProfileQuery{ID: target.ID.String()},
	); !application.Is(err, "authorization.denied") {
		t.Fatalf("unbound cross-user read error = %v", err)
	}

	role, err := persistence.Role().Save(ctx, &model.Role{
		Name: "institution-directory-viewer", DisplayName: "Institution Directory Viewer",
		Permissions: []string{string(model.ActionUserView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: viewer.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	if err != nil {
		t.Fatal(err)
	}

	visible, err := helper.App.GetUserProfile(
		ctx, application.NewInvocation(*principal, metadata), application.GetUserProfileQuery{ID: target.ID.String()},
	)
	if err != nil || visible.ID != target.ID {
		t.Fatalf("authorized cross-user read = %#v, %v", visible, err)
	}
	allowed, permErr = helper.App.Can(
		ctx, *principal, model.ActionUserManage,
		model.Resource{Type: model.ResourceUser, ID: target.ID.String()},
	)
	if permErr != nil || allowed {
		t.Fatalf("view role unexpectedly granted management = %v, %v", allowed, permErr)
	}

	if _, err := persistence.RoleBinding().End(ctx, binding.ID.String(), model.GetMillis()); err != nil {
		t.Fatal(err)
	}
	if _, err := helper.App.GetUserProfile(
		ctx, application.NewInvocation(*principal, metadata), application.GetUserProfileQuery{ID: target.ID.String()},
	); !application.Is(err, "authorization.denied") {
		t.Fatalf("ended binding cross-user read error = %v", err)
	}

	events, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: viewer.ID.String(),
		Action:  string(model.ActionUserView),
		Resource: &model.Resource{
			Type: model.ResourceUser,
			ID:   target.ID.String(),
		},
		Limit:      20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[model.AuditStatus]int{}
	for _, event := range events {
		statuses[event.Status]++
		if event.Resource.Type != model.ResourceUser ||
			event.Resource.ID != target.ID.String() ||
			event.ScopeType != model.RoleScopeInstitution ||
			event.ScopeID != institution.ID.String() {
			t.Fatalf("user authorization audit scope = %#v", event)
		}
	}
	if statuses[model.AuditStatusSuccess] != 1 ||
		statuses[model.AuditStatusFail] != 2 {
		t.Fatalf("user authorization audit statuses = %#v", statuses)
	}
}

func TestAcademicUnitVisibilityIsBoundedAcrossUsersBindingsAndAudits(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	now := model.GetMillis()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "engineering", DisplayName: "Engineering",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, ParentID: root.ID,
		Name: "computing", DisplayName: "Computing",
	})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "humanities", DisplayName: "Humanities",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "medicine", DisplayName: "Medicine",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: child.ID, Name: "computer-science", DisplayName: "Computer Science",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:  "2026-2027", DisplayName: "2026-2027",
		StartsAt: model.TimeFromMillis(now - 86_400_000),
		EndsAt:   model.TimeFromMillis(now + 31_536_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "class-a", DisplayName: "Class A",
	})
	if err != nil {
		t.Fatal(err)
	}

	password := "correct horse battery staple"
	createUser := func(username string) *model.User {
		t.Helper()
		user, appErr := helper.App.CreateLocalUser(ctx, &model.User{
			Username: username, Email: username + "@example.edu", DisplayName: username,
			EmailVerified: true, Locale: "fr", Timezone: "Europe/Paris",
		}, password)
		if appErr != nil {
			t.Fatal(appErr)
		}
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
		if _, err := persistence.AcademicUnitMember().Save(ctx, membership); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: classUser.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: classUser.ID, StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}

	visibilityRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "academic-directory-auditor", DisplayName: "Academic Directory Auditor",
		Permissions: []string{
			string(model.ActionUserView), string(model.ActionRoleBindingView),
			string(model.ActionAcademicAuditView),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "visibility-fixture-role", DisplayName: "Visibility Fixture Role",
		Permissions: []string{string(model.ActionClassView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	archivedTargetRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "archived-visibility-fixture-role", DisplayName: "Archived Visibility Fixture Role",
		Permissions: []string{string(model.ActionClassView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: archivedRoleUser.ID, RoleID: archivedTargetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 20_000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.Role().Archive(ctx, archivedTargetRole.ID.String(), model.GetMillis()); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: viewer.ID, RoleID: visibilityRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: viewer.ID, RoleID: visibilityRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: otherRoot.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	visibleBinding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 20_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	historicalBinding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: child.ID.String(), StartsAt: model.TimeFromMillis(now - 40_000), EndsAt: model.OptionalTimeFromMillis(now - 30_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: sibling.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: bindingUser.ID, RoleID: targetRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}

	login := loginIntegrationUser(t, helper.Handler(), viewer.Username, password, model.SessionClientCLI, "scoped-device")
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{
		RequestID: model.NewId(), IPAddress: "127.0.0.1", UserAgent: "access-visibility-integration-test",
	})

	search := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users?limit=50", nil, login.Tokens.AccessToken)
	if search.Code != http.StatusOK {
		t.Fatalf("scoped user search status = %d: %s", search.Code, search.Body.String())
	}
	var directory []map[string]any
	if err := json.Unmarshal(search.Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		unitUser.ID.String(): false, classUser.ID.String(): false, bindingUser.ID.String(): false,
	}
	for _, user := range directory {
		id, _ := user["id"].(string)
		if _, ok := wanted[id]; ok {
			wanted[id] = true
		}
		for _, field := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
			if _, leaked := user[field]; leaked {
				t.Fatalf("scoped user %s leaked %s: %#v", id, field, user)
			}
		}
		if id == siblingUser.ID.String() || id == historicalUser.ID.String() || id == archivedRoleUser.ID.String() {
			t.Fatalf("unauthorized user returned by scoped search: %#v", user)
		}
	}
	for id, seen := range wanted {
		if !seen {
			t.Errorf("scoped search omitted visible user %s: %#v", id, directory)
		}
	}

	classMembersRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "scoped-class-roster-viewer", DisplayName: "Scoped Class Roster Viewer",
		Permissions: []string{string(model.ActionClassMembersView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	classMembersViewer := createUser("scoped-class-roster-viewer")
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: classMembersViewer.ID, RoleID: classMembersRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: root.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	classMembersLogin := loginIntegrationUser(
		t, helper.Handler(), classMembersViewer.Username, password,
		model.SessionClientCLI, "scoped-class-roster-device",
	)
	assertClassMemberDirectory := func(label, credential string) {
		t.Helper()
		response := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users?limit=50", nil, credential)
		if response.Code != http.StatusOK {
			t.Fatalf("%s class-member search status = %d: %s", label, response.Code, response.Body.String())
		}
		var users []map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil {
			t.Fatal(err)
		}
		seenClassMember := false
		for _, user := range users {
			id, _ := user["id"].(string)
			if id == classUser.ID.String() {
				seenClassMember = true
			}
			if id == unitUser.ID.String() || id == bindingUser.ID.String() ||
				id == archivedRoleUser.ID.String() || id == siblingUser.ID.String() {
				t.Fatalf("%s class-member search leaked non-roster User %s: %#v", label, id, users)
			}
			for _, field := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
				if _, leaked := user[field]; leaked {
					t.Fatalf("%s class-member User %s leaked %s: %#v", label, id, field, user)
				}
			}
		}
		if !seenClassMember {
			t.Fatalf("%s class-member search omitted current Class member %s: %#v", label, classUser.ID, users)
		}
	}
	assertClassMemberDirectory("academic-unit", classMembersLogin.Tokens.AccessToken)
	classMemberRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+classUser.ID.String(), nil, classMembersLogin.Tokens.AccessToken)
	if classMemberRead.Code != http.StatusOK {
		t.Fatalf("class-member exact read status = %d: %s", classMemberRead.Code, classMemberRead.Body.String())
	}
	classMemberPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+classUser.ID.String()+"/profile-picture?size=128", nil, classMembersLogin.Tokens.AccessToken)
	if classMemberPicture.Code != http.StatusOK {
		t.Fatalf("class-member profile-picture status = %d: %s", classMemberPicture.Code, classMemberPicture.Body.String())
	}
	for label, userID := range map[string]string{
		"academic-unit member": unitUser.ID.String(),
		"active Role holder":   bindingUser.ID.String(),
		"archived Role holder": archivedRoleUser.ID.String(),
	} {
		nonMemberRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+userID, nil, classMembersLogin.Tokens.AccessToken)
		if nonMemberRead.Code != http.StatusForbidden {
			t.Fatalf("class-member %s non-roster read status = %d: %s", label, nonMemberRead.Code, nonMemberRead.Body.String())
		}
		nonMemberPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+userID+"/profile-picture?size=128", nil, classMembersLogin.Tokens.AccessToken)
		if nonMemberPicture.Code != http.StatusForbidden {
			t.Fatalf("class-member %s non-roster profile-picture status = %d: %s", label, nonMemberPicture.Code, nonMemberPicture.Body.String())
		}
	}

	institutionClassMembersViewer := createUser("institution-class-roster-viewer")
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: institutionClassMembersViewer.ID, RoleID: classMembersRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	institutionClassMembersLogin := loginIntegrationUser(
		t, helper.Handler(), institutionClassMembersViewer.Username, password,
		model.SessionClientCLI, "institution-class-roster-device",
	)
	assertClassMemberDirectory("institution", institutionClassMembersLogin.Tokens.AccessToken)

	unitRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String(), nil, login.Tokens.AccessToken)
	if unitRead.Code != http.StatusOK {
		t.Fatalf("descendant user read status = %d: %s", unitRead.Code, unitRead.Body.String())
	}
	readDecisions, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: viewer.ID.String(), Action: string(model.ActionUserView),
		Resource: &model.Resource{Type: model.ResourceUser, ID: unitUser.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(readDecisions) != 1 || readDecisions[0].Status != model.AuditStatusSuccess ||
		readDecisions[0].ScopeType != model.RoleScopeAcademicUnit || readDecisions[0].ScopeID != root.ID.String() {
		t.Fatalf("multi-root user read decision = %#v, want matched root %s", readDecisions, root.ID)
	}
	disableAt := model.GetMillis()
	disableAudit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: viewer.ID, Action: string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: unitUser.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusAttempt, NodeID: "access-visibility-integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: unitUser.ID.String(), ExpectedRevision: unitUser.Revision, Disabled: true,
		ChangedAt: disableAt, RevocationReason: "visibility regression fixture",
		AuditEventID: disableAudit.ID.String(), AuditAt: disableAt,
	})); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/users?limit=50", "/api/v1/users?limit=50&include_disabled=true"} {
		response := performJSONRequest(helper.Handler(), http.MethodGet, path, nil, login.Tokens.AccessToken)
		if response.Code != http.StatusOK {
			t.Fatalf("scoped disabled search %q status = %d: %s", path, response.Code, response.Body.String())
		}
		var users []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil {
			t.Fatal(err)
		}
		for _, user := range users {
			if user.ID == unitUser.ID.String() {
				t.Fatalf("scoped disabled search %q exposed User %s", path, user.ID)
			}
		}
	}
	disabledRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String(), nil, login.Tokens.AccessToken)
	if disabledRead.Code != http.StatusForbidden {
		t.Fatalf("disabled exact read status = %d: %s", disabledRead.Code, disabledRead.Body.String())
	}
	disabledPicture := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+unitUser.ID.String()+"/profile-picture?size=128", nil, login.Tokens.AccessToken)
	if disabledPicture.Code != http.StatusForbidden {
		t.Fatalf("disabled profile-picture status = %d: %s", disabledPicture.Code, disabledPicture.Body.String())
	}
	globalViewer := createUser("institution-directory-viewer")
	globalRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "institution-directory-viewer", DisplayName: "Institution Directory Viewer",
		Permissions: []string{string(model.ActionUserView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: globalViewer.ID, RoleID: globalRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis()),
	}); err != nil {
		t.Fatal(err)
	}
	globalLogin := loginIntegrationUser(t, helper.Handler(), globalViewer.Username, password, model.SessionClientCLI, "institution-directory-device")
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/api/v1/users?limit=50"},
		{path: "/api/v1/users?limit=50&include_disabled=true", want: true},
	} {
		response := performJSONRequest(helper.Handler(), http.MethodGet, test.path, nil, globalLogin.Tokens.AccessToken)
		if response.Code != http.StatusOK {
			t.Fatalf("institution-wide disabled search %q status = %d: %s", test.path, response.Code, response.Body.String())
		}
		var users []struct {
			ID         string `json:"id"`
			DisabledAt int64  `json:"disabled_at"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, user := range users {
			if user.ID == unitUser.ID.String() {
				found = true
				if user.DisabledAt == 0 {
					t.Fatalf("institution-wide disabled projection omitted disabled_at: %#v", user)
				}
			}
		}
		if found != test.want {
			t.Fatalf("institution-wide disabled search %q found=%t, want %t", test.path, found, test.want)
		}
	}
	siblingRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+siblingUser.ID.String(), nil, login.Tokens.AccessToken)
	if siblingRead.Code != http.StatusForbidden {
		t.Fatalf("sibling user read status = %d: %s", siblingRead.Code, siblingRead.Body.String())
	}
	historicalRead := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/users/"+historicalUser.ID.String(), nil, login.Tokens.AccessToken)
	if historicalRead.Code != http.StatusForbidden {
		t.Fatalf("historical user read status = %d: %s", historicalRead.Code, historicalRead.Body.String())
	}

	bindings, listErr := helper.App.ListRoleBindings(ctx, invocation, application.ListRoleBindingsQuery{UserID: bindingUser.ID.String()})
	if listErr != nil {
		t.Fatal(listErr)
	}
	seenBindings := map[string]bool{}
	for _, binding := range bindings {
		seenBindings[binding.ID.String()] = true
		if binding.ScopeType != model.RoleScopeAcademicUnit || binding.ScopeID != child.ID.String() {
			t.Fatalf("out-of-scope binding returned: %#v", binding)
		}
	}
	if !seenBindings[visibleBinding.ID.String()] || !seenBindings[historicalBinding.ID.String()] {
		t.Fatalf("visible binding history = %#v", seenBindings)
	}

	saveAudit := func(action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string) string {
		t.Helper()
		event, err := persistence.Audit().Save(ctx, &model.AuditEvent{
			ActorID: viewer.ID, Action: string(action), Resource: resource,
			ScopeType: scopeType, ScopeID: scopeID, Status: model.AuditStatusSuccess,
			RequestID: model.NewId(), NodeID: "visibility-node", ClientType: "cli",
			AuthMethod: "bearer", IPAddress: "127.0.0.1", UserAgent: "sensitive-agent",
			Parameters: json.RawMessage(`{"fixture":true}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return event.ID.String()
	}
	childAuditID := saveAudit(model.ActionAcademicUnitManage, model.Resource{Type: model.ResourceAcademicUnit, ID: child.ID.String()}, model.RoleScopeAcademicUnit, child.ID.String())
	classAuditID := saveAudit(model.ActionClassManage, model.Resource{Type: model.ResourceClass, ID: class.ID.String()}, model.RoleScopeClass, class.ID.String())
	siblingAuditID := saveAudit(model.ActionAcademicUnitManage, model.Resource{Type: model.ResourceAcademicUnit, ID: sibling.ID.String()}, model.RoleScopeAcademicUnit, sibling.ID.String())
	securityAuditID := saveAudit(model.ActionExternalIdentityManage, model.Resource{Type: model.ResourceUser, ID: bindingUser.ID.String()}, model.RoleScopeAcademicUnit, child.ID.String())

	audits := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/audits?limit=100", nil, login.Tokens.AccessToken)
	if audits.Code != http.StatusOK {
		t.Fatalf("scoped audit list status = %d: %s", audits.Code, audits.Body.String())
	}
	var auditBody struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(audits.Body.Bytes(), &auditBody); err != nil {
		t.Fatal(err)
	}
	seenAudits := map[string]bool{}
	for _, event := range auditBody.Events {
		id, _ := event["id"].(string)
		seenAudits[id] = true
		for _, field := range []string{"session_id", "request_id", "node_id", "client_type", "authentication_method", "ip_address", "user_agent"} {
			if _, leaked := event[field]; leaked {
				t.Fatalf("scoped audit %s leaked %s: %#v", id, field, event)
			}
		}
	}
	if !seenAudits[childAuditID] || !seenAudits[classAuditID] {
		t.Fatalf("scoped audit list omitted descendant history: %#v", seenAudits)
	}
	if seenAudits[siblingAuditID] || seenAudits[securityAuditID] {
		t.Fatalf("scoped audit list leaked sibling/security history: %#v", seenAudits)
	}
	institutionAcademicViewer := createUser("institution-academic-auditor")
	institutionAcademicRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "institution-academic-auditor", DisplayName: "Institution Academic Auditor",
		Permissions: []string{string(model.ActionAcademicAuditView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: institutionAcademicViewer.ID, RoleID: institutionAcademicRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis()),
	}); err != nil {
		t.Fatal(err)
	}
	institutionAcademicLogin := loginIntegrationUser(
		t, helper.Handler(), institutionAcademicViewer.Username, password,
		model.SessionClientCLI, "institution-academic-audit-device",
	)
	assertInstitutionAcademicAudits := func(label, credential string) {
		t.Helper()
		response := performJSONRequest(helper.Handler(), http.MethodGet, "/api/v1/audits?limit=100", nil, credential)
		if response.Code != http.StatusOK {
			t.Fatalf("%s academic audit status = %d: %s", label, response.Code, response.Body.String())
		}
		var body struct {
			Events []map[string]any `json:"events"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, event := range body.Events {
			id, _ := event["id"].(string)
			seen[id] = true
			for _, field := range []string{"session_id", "request_id", "node_id", "client_type", "authentication_method", "ip_address", "user_agent"} {
				if _, leaked := event[field]; leaked {
					t.Fatalf("%s academic audit %s leaked %s: %#v", label, id, field, event)
				}
			}
		}
		if !seen[childAuditID] || !seen[classAuditID] || !seen[siblingAuditID] {
			t.Fatalf("%s academic audit omitted cross-unit history: %#v", label, seen)
		}
		if seen[securityAuditID] {
			t.Fatalf("%s academic audit leaked security history: %#v", label, seen)
		}
	}
	assertInstitutionAcademicAudits("session", institutionAcademicLogin.Tokens.AccessToken)
	createdToken := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/users/me/tokens", map[string]any{
		"description": "academic audit automation",
		"scopes":      []string{string(model.ActionAcademicAuditView)},
		"expires_at":  model.GetMillis() + 2*60*60*1000,
	}, institutionAcademicLogin.Tokens.AccessToken)
	if createdToken.Code != http.StatusCreated {
		t.Fatalf("create academic audit PAT status = %d: %s", createdToken.Code, createdToken.Body.String())
	}
	var tokenBody struct {
		Credential string `json:"credential"`
	}
	if err := json.Unmarshal(createdToken.Body.Bytes(), &tokenBody); err != nil || tokenBody.Credential == "" {
		t.Fatalf("create academic audit PAT response = %s, %v", createdToken.Body.String(), err)
	}
	assertInstitutionAcademicAudits("personal access token", tokenBody.Credential)
	decisions, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: institutionAcademicViewer.ID.String(), Action: string(model.ActionAcademicAuditView), Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) < 2 {
		t.Fatalf("institution academic audit decisions = %#v, want Session and PAT decisions", decisions)
	}
	for _, decision := range decisions {
		if decision.Status != model.AuditStatusSuccess || decision.ScopeType != model.RoleScopeInstitution ||
			decision.ScopeID != institution.ID.String() {
			t.Fatalf("institution academic audit decision scope = %#v", decision)
		}
	}
}
