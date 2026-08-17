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
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPrivilegedAuditListingIsScopedAndDurablyAudited(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
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
	statuses := map[model.AuditStatus]bool{}
	for _, event := range response.Events {
		if event.Action == string(model.ActionAuditView) &&
			event.ActorID == user.ID.String() &&
			event.Resource.ID == institution.ID.String() {
			statuses[event.Status] = true
		}
		if event.SessionID == "" || event.NodeID == "" || event.RequestID == "" {
			t.Fatalf("incomplete security audit event = %#v", event)
		}
	}
	if !statuses[model.AuditStatusFail] || !statuses[model.AuditStatusSuccess] {
		t.Fatalf("authorization decision statuses = %#v", statuses)
	}
}

func TestAuthorizationResolvesCurrentAcademicHierarchy(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
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
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: child.ID, Name: "computer-science",
		DisplayName: "Computer Science",
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
		Permissions: []string{string(model.ActionClassView)},
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
		Limit: 20,
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
