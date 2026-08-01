//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAcademicMembershipAndUserAdministrationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	handler := helper.Handler()
	password := "correct horse battery staple"

	bootstrap := performJSONRequest(handler, http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"institution": map[string]any{
			"name": "northbridge", "display_name": "Northbridge University",
		},
		"administrator": map[string]any{
			"username": "academic-owner", "email": "academic-owner@example.edu",
		},
		"password": password,
	}, "")
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var installation model.InstallationBootstrapResult
	decodeIntegrationResponse(t, bootstrap, &installation)
	adminLogin := loginIntegrationUser(
		t, handler, installation.Administrator.Username, password,
		model.SessionClientCLI, "academic-owner-cli",
	)
	adminToken := adminLogin.Tokens.AccessToken

	root := createIntegrationResource[model.AcademicUnit](
		t, handler, http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "engineering", "display_name": "Engineering"},
		adminToken,
	)
	child := createIntegrationResource[model.AcademicUnit](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+root.Id+"/children",
		map[string]any{"name": "computing", "display_name": "Computing"},
		adminToken,
	)
	if child.ParentId != root.Id || child.InstitutionId != installation.Institution.Id {
		t.Fatalf("academic hierarchy = %#v", child)
	}
	programme := createIntegrationResource[model.Programme](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+child.Id+"/programmes",
		map[string]any{"name": "computer-science", "display_name": "Computer Science"},
		adminToken,
	)
	level := createIntegrationResource[model.ProgrammeLevel](
		t, handler, http.MethodPost, "/api/v1/programmes/"+programme.Id+"/levels",
		map[string]any{"name": "year-1", "display_name": "Year 1"},
		adminToken,
	)
	now := model.GetMillis()
	period := createIntegrationResource[model.AcademicPeriod](
		t, handler, http.MethodPost, "/api/v1/academic-periods",
		map[string]any{
			"name": "2026-2027", "display_name": "2026-2027",
			"start_at": now - 86_400_000, "end_at": now + 31_536_000_000,
		},
		adminToken,
	)
	firstClass := createIntegrationResource[model.Class](
		t, handler, http.MethodPost, "/api/v1/programme-levels/"+level.Id+"/classes",
		map[string]any{
			"academic_period_id": period.Id,
			"name":               "class-a", "display_name": "Class A",
		},
		adminToken,
	)
	secondClass := createIntegrationResource[model.Class](
		t, handler, http.MethodPost, "/api/v1/programme-levels/"+level.Id+"/classes",
		map[string]any{
			"academic_period_id": period.Id,
			"name":               "class-b", "display_name": "Class B",
		},
		adminToken,
	)

	student := createIntegrationUser(t, helper, "student-one", password)
	teacher := createIntegrationUser(t, helper, "teacher-one", password)
	unrelated := createIntegrationUser(t, helper, "student-unrelated", password)
	disabled := createIntegrationUser(t, helper, "disabled-user", password)

	createIntegrationResource[model.Affiliation](
		t, handler, http.MethodPost, "/api/v1/users/"+student.Id+"/affiliations",
		map[string]any{"kind": model.AffiliationStudent, "start_at": now - 10_000},
		adminToken,
	)
	createIntegrationResource[model.Affiliation](
		t, handler, http.MethodPost, "/api/v1/users/"+teacher.Id+"/affiliations",
		map[string]any{"kind": model.AffiliationTeacher, "start_at": now - 10_000},
		adminToken,
	)
	unitMember := createIntegrationResource[model.AcademicUnitMember](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+child.Id+"/members",
		map[string]any{"user_id": teacher.Id, "start_at": now - 10_000},
		adminToken,
	)
	firstEnrollment := createIntegrationResource[model.ClassEnrollment](
		t, handler, http.MethodPost, "/api/v1/classes/"+firstClass.Id+"/members",
		map[string]any{"user_id": student.Id, "start_at": now - 5_000},
		adminToken,
	)
	if firstEnrollment.Previous != nil {
		t.Fatalf("first enrollment = %#v", firstEnrollment)
	}
	transfer := createIntegrationResource[model.ClassEnrollment](
		t, handler, http.MethodPost, "/api/v1/classes/"+secondClass.Id+"/members",
		map[string]any{"user_id": student.Id, "start_at": now - 1_000},
		adminToken,
	)
	if transfer.Previous == nil ||
		transfer.Previous.Id != firstEnrollment.Membership.Id ||
		transfer.Previous.EndAt != now-1_000 {
		t.Fatalf("transfer = %#v", transfer)
	}

	teacherRole, err := persistence.Role().Save(context.Background(), &model.Role{
		Name: "class-teacher", DisplayName: "Class Teacher",
		Permissions: []string{
			string(model.ActionClassView),
			string(model.ActionClassMembersView),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(context.Background(), &model.RoleBinding{
		UserId: teacher.Id, RoleId: teacherRole.Id,
		ScopeType: model.RoleScopeAcademicUnit, ScopeId: root.Id,
		StartAt: now - 10_000,
	}); err != nil {
		t.Fatal(err)
	}
	teacherLogin := loginIntegrationUser(
		t, handler, teacher.Username, password,
		model.SessionClientCLI, "teacher-cli",
	)
	visible := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+student.Id, nil,
		teacherLogin.Tokens.AccessToken,
	)
	if visible.Code != http.StatusOK {
		t.Fatalf("teacher student visibility = %d: %s", visible.Code, visible.Body.String())
	}
	hidden := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+unrelated.Id, nil,
		teacherLogin.Tokens.AccessToken,
	)
	if hidden.Code != http.StatusForbidden {
		t.Fatalf("unrelated student visibility = %d: %s", hidden.Code, hidden.Body.String())
	}
	patchUser := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/users/"+student.Id,
		map[string]any{"display_name": "Student One Updated"},
		adminToken,
	)
	if patchUser.Code != http.StatusOK {
		t.Fatalf("patch user = %d: %s", patchUser.Code, patchUser.Body.String())
	}
	userSearch := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users?q=Student+One+Updated&limit=10", nil,
		adminToken,
	)
	if userSearch.Code != http.StatusOK {
		t.Fatalf("search users = %d: %s", userSearch.Code, userSearch.Body.String())
	}
	var foundUsers []*model.User
	decodeIntegrationResponse(t, userSearch, &foundUsers)
	if len(foundUsers) != 1 || foundUsers[0].Id != student.Id {
		t.Fatalf("searched users = %#v", foundUsers)
	}
	endUnitMembership := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/academic-unit-members/"+unitMember.Id,
		nil,
		adminToken,
	)
	if endUnitMembership.Code != http.StatusOK {
		t.Fatalf(
			"end academic unit membership = %d: %s",
			endUnitMembership.Code,
			endUnitMembership.Body.String(),
		)
	}
	malformed := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/academic-units/"+child.Id+"/children",
		strings.NewReader("{"),
	)
	malformed.Header.Set("Authorization", "Bearer "+teacherLogin.Tokens.AccessToken)
	malformed.Header.Set("Content-Type", "application/json")
	malformedResponse := httptest.NewRecorder()
	handler.ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"academic permission preflight followed body decode: %d: %s",
			malformedResponse.Code,
			malformedResponse.Body.String(),
		)
	}

	activeMembers := performJSONRequest(
		handler, http.MethodGet, "/api/v1/classes/"+secondClass.Id+"/members", nil,
		adminToken,
	)
	if activeMembers.Code != http.StatusOK {
		t.Fatalf("active class members = %d: %s", activeMembers.Code, activeMembers.Body.String())
	}
	var members []*model.ClassMember
	decodeIntegrationResponse(t, activeMembers, &members)
	if len(members) != 1 || members[0].UserId != student.Id {
		t.Fatalf("active class members = %#v", members)
	}

	disabledLogin := loginIntegrationUser(
		t, handler, disabled.Username, password,
		model.SessionClientCLI, "disabled-cli",
	)
	disableResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/users/"+disabled.Id+"/disable", nil,
		adminToken,
	)
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("disable user = %d: %s", disableResponse.Code, disableResponse.Body.String())
	}
	revoked := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/me", nil,
		disabledLogin.Tokens.AccessToken,
	)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session remained valid = %d: %s", revoked.Code, revoked.Body.String())
	}

	firstStudentSession := loginIntegrationUser(
		t, handler, student.Username, password,
		model.SessionClientCLI, "student-first-cli",
	)
	secondStudentSession := loginIntegrationUser(
		t, handler, student.Username, password,
		model.SessionClientCLI, "student-second-cli",
	)
	teacherSessionList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/"+student.Id+"/sessions",
		nil,
		teacherLogin.Tokens.AccessToken,
	)
	if teacherSessionList.Code != http.StatusForbidden {
		t.Fatalf(
			"teacher session list = %d: %s",
			teacherSessionList.Code,
			teacherSessionList.Body.String(),
		)
	}
	sessionList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/"+student.Id+"/sessions",
		nil,
		adminToken,
	)
	if sessionList.Code != http.StatusOK {
		t.Fatalf(
			"administrator session list = %d: %s",
			sessionList.Code,
			sessionList.Body.String(),
		)
	}
	var studentSessions []*model.Session
	decodeIntegrationResponse(t, sessionList, &studentSessions)
	if len(studentSessions) != 2 {
		t.Fatalf("administrator session list = %#v", studentSessions)
	}
	revokeOne := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/users/"+student.Id+"/sessions/"+firstStudentSession.Session.Id,
		nil,
		adminToken,
	)
	if revokeOne.Code != http.StatusNoContent {
		t.Fatalf(
			"administrator session revoke = %d: %s",
			revokeOne.Code,
			revokeOne.Body.String(),
		)
	}
	firstRevoked := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		firstStudentSession.Tokens.AccessToken,
	)
	if firstRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked student session = %d", firstRevoked.Code)
	}
	secondActive := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondStudentSession.Tokens.AccessToken,
	)
	if secondActive.Code != http.StatusOK {
		t.Fatalf("unrelated student session = %d", secondActive.Code)
	}
	revokeAll := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/"+student.Id+"/sessions/revoke-all",
		nil,
		adminToken,
	)
	if revokeAll.Code != http.StatusNoContent {
		t.Fatalf(
			"administrator revoke all sessions = %d: %s",
			revokeAll.Code,
			revokeAll.Body.String(),
		)
	}
	secondRevoked := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondStudentSession.Tokens.AccessToken,
	)
	if secondRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("student session survived revoke all = %d", secondRevoked.Code)
	}

	events, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	mutated := map[string]bool{}
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess && len(event.Result) != 0 {
			mutated[event.Action] = true
		}
	}
	for _, action := range []model.Action{
		model.ActionInstitutionManage,
		model.ActionAcademicUnitManage,
		model.ActionClassMembersManage,
		model.ActionUserManage,
		model.ActionSessionManage,
	} {
		if !mutated[string(action)] {
			t.Errorf("missing successful mutation audit for %s", action)
		}
	}
}

func createIntegrationUser(
	t *testing.T,
	helper *testlib.Helper,
	username string,
	password string,
) *model.User {
	t.Helper()
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username:    username,
		Email:       username + "@example.edu",
		DisplayName: username,
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	return user
}

func createIntegrationResource[T any](
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	token string,
) *T {
	t.Helper()
	response := performJSONRequest(handler, method, path, body, token)
	if response.Code != http.StatusCreated {
		t.Fatalf("%s %s = %d: %s", method, path, response.Code, response.Body.String())
	}
	var result T
	decodeIntegrationResponse(t, response, &result)
	return &result
}

func decodeIntegrationResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}
