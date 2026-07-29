// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestPrivilegedAuditListingIsScopedAndDurablyAudited(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithServerOptions(app.WithStore(persistence)),
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
		t, helper.Server.Handler(), user.Username, password,
		model.SessionClientCLI, "audit-cli",
	)

	denied := performJSONRequest(
		helper.Server.Handler(), http.MethodGet, "/api/v1/audits", nil,
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
		UserId: user.Id, RoleId: role.Id, ScopeType: model.RoleScopeInstitution,
		ScopeId: institution.Id, StartAt: model.GetMillis(),
	}); err != nil {
		t.Fatal(err)
	}

	allowed := performJSONRequest(
		helper.Server.Handler(), http.MethodGet, "/api/v1/audits?limit=20", nil,
		login.Tokens.AccessToken,
	)
	if allowed.Code != http.StatusOK {
		t.Fatalf("authorized audit listing status = %d: %s", allowed.Code, allowed.Body.String())
	}
	var response struct {
		Events []*model.AuditEvent `json:"events"`
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
			event.ActorId == user.Id &&
			event.Resource.Id == institution.Id {
			statuses[event.Status] = true
		}
		if event.SessionId == "" || event.NodeId == "" || event.RequestId == "" {
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
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithServerOptions(app.WithStore(persistence)),
	)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: institution.Id, Name: "engineering", DisplayName: "Engineering",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionId: institution.Id, ParentId: root.Id,
		Name: "computing", DisplayName: "Computing",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitId: child.Id, Name: "computer-science",
		DisplayName: "Computer Science",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeId: programme.Id, Name: "year-1", DisplayName: "Year 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionId: institution.Id, Name: "2026-2027", DisplayName: "2026-2027",
		StartAt: 1_800_000_000_000, EndAt: 1_830_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelId: level.Id, AcademicPeriodId: period.Id,
		Name: "class-a", DisplayName: "Class A",
	})
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "hierarchy-teacher", Email: "hierarchy-teacher@example.edu",
		DisplayName: "Hierarchy Teacher",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	login := loginIntegrationUser(
		t, helper.Server.Handler(), user.Username, password,
		model.SessionClientDesktop, "hierarchy-device",
	)
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{
		Name: "academic-unit-teacher", DisplayName: "Academic Unit Teacher",
		Permissions: []string{string(model.ActionClassView)},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserId: user.Id, RoleId: role.Id, ScopeType: model.RoleScopeAcademicUnit,
		ScopeId: root.Id, StartAt: model.GetMillis() - 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, appErr := helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, Id: class.Id},
	)
	if appErr != nil || !allowed {
		t.Fatalf("ancestor class permission = %v, %v", allowed, appErr)
	}
	allowed, appErr = helper.App.Can(
		ctx, *principal, model.ActionInstitutionManage,
		model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
	)
	if appErr != nil || allowed {
		t.Fatalf("lower-scope institution permission = %v, %v", allowed, appErr)
	}
	if _, err := persistence.RoleBinding().End(ctx, binding.Id, model.GetMillis()); err != nil {
		t.Fatal(err)
	}
	allowed, appErr = helper.App.Can(
		ctx, *principal, model.ActionClassView,
		model.Resource{Type: model.ResourceClass, Id: class.Id},
	)
	if appErr != nil || allowed {
		t.Fatalf("ended binding permission = %v, %v", allowed, appErr)
	}
}
