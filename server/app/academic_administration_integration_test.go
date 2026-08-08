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
	var installationWire struct {
		Institution *struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator *wireUserProfileResponse `json:"administrator"`
	}
	decodeIntegrationResponse(t, bootstrap, &installationWire)
	installation.Institution = &model.Institution{ID: model.InstitutionID(installationWire.Institution.ID)}
	installation.Administrator = installationWire.Administrator.model()
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
		t, handler, http.MethodPost, "/api/v1/academic-units/"+root.ID.String()+"/children",
		map[string]any{"name": "computing", "display_name": "Computing"},
		adminToken,
	)
	if child.ParentID != root.ID || child.InstitutionID != installation.Institution.ID {
		t.Fatalf("academic hierarchy = %#v", child)
	}
	programme := createIntegrationResource[model.Programme](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+child.ID.String()+"/programmes",
		map[string]any{"name": "computer-science", "display_name": "Computer Science"},
		adminToken,
	)
	level := createIntegrationResource[model.ProgrammeLevel](
		t, handler, http.MethodPost, "/api/v1/programmes/"+programme.ID.String()+"/levels",
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
		t, handler, http.MethodPost, "/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{
			"academic_period_id": period.ID.String(),
			"name":               "class-a", "display_name": "Class A",
		},
		adminToken,
	)
	secondClass := createIntegrationResource[model.Class](
		t, handler, http.MethodPost, "/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{
			"academic_period_id": period.ID.String(),
			"name":               "class-b", "display_name": "Class B",
		},
		adminToken,
	)

	student := createIntegrationUser(t, helper, "student-one", password)
	teacher := createIntegrationUser(t, helper, "teacher-one", password)
	unrelated := createIntegrationUser(t, helper, "student-unrelated", password)
	disabled := createIntegrationUser(t, helper, "disabled-user", password)

	createIntegrationResource[map[string]any](
		t, handler, http.MethodPost, "/api/v1/users/"+student.ID.String()+"/affiliations",
		map[string]any{"kind": model.AffiliationStudent, "start_at": now - 10_000},
		adminToken,
	)
	createIntegrationResource[map[string]any](
		t, handler, http.MethodPost, "/api/v1/users/"+teacher.ID.String()+"/affiliations",
		map[string]any{"kind": model.AffiliationTeacher, "start_at": now - 10_000},
		adminToken,
	)
	// Membership wire DTOs keep millis fields; decode into local shapes rather than domain models.
	type membershipWire struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
		EndAt  int64  `json:"end_at"`
	}
	type enrollmentWire struct {
		Membership membershipWire  `json:"membership"`
		Previous   *membershipWire `json:"previous,omitempty"`
	}
	unitMember := createIntegrationResource[membershipWire](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+child.ID.String()+"/members",
		map[string]any{"user_id": teacher.ID.String(), "start_at": now - 10_000},
		adminToken,
	)
	firstEnrollmentResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/classes/"+firstClass.ID.String()+"/members",
		map[string]any{"user_id": student.ID.String(), "start_at": now - 5_000}, adminToken,
	)
	if firstEnrollmentResponse.Code != http.StatusCreated {
		t.Fatalf("first enrollment = %d: %s; logs: %s", firstEnrollmentResponse.Code, firstEnrollmentResponse.Body.String(), helper.Logs.String())
	}
	var firstEnrollment enrollmentWire
	decodeIntegrationResponse(t, firstEnrollmentResponse, &firstEnrollment)
	if firstEnrollment.Previous != nil {
		t.Fatalf("first enrollment = %#v", &firstEnrollment)
	}
	transferResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/classes/"+secondClass.ID.String()+"/members",
		map[string]any{"user_id": student.ID.String(), "start_at": now - 1_000}, adminToken,
	)
	if transferResponse.Code != http.StatusCreated {
		t.Fatalf("transfer enrollment = %d: %s; logs: %s", transferResponse.Code, transferResponse.Body.String(), helper.Logs.String())
	}
	var transfer enrollmentWire
	decodeIntegrationResponse(t, transferResponse, &transfer)
	if transfer.Previous == nil ||
		transfer.Previous.ID != firstEnrollment.Membership.ID ||
		transfer.Previous.EndAt != now-1_000 {
		t.Fatalf("transfer = %#v", &transfer)
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
		UserID: teacher.ID, RoleID: teacherRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: root.ID.String(),
		StartsAt: model.TimeFromMillis(now - 10_000),
	}); err != nil {
		t.Fatal(err)
	}
	teacherLogin := loginIntegrationUser(
		t, handler, teacher.Username, password,
		model.SessionClientCLI, "teacher-cli",
	)
	visible := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+student.ID.String(), nil,
		teacherLogin.Tokens.AccessToken,
	)
	if visible.Code != http.StatusOK {
		t.Fatalf("teacher student visibility = %d: %s", visible.Code, visible.Body.String())
	}
	visibilityEvents, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		ActorId: teacher.ID.String(),
		Action:  string(model.ActionUserView),
		Resource: &model.Resource{
			Type: model.ResourceUser,
			ID:   student.ID.String(),
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visibilityEvents) != 1 ||
		visibilityEvents[0].Status != model.AuditStatusSuccess ||
		visibilityEvents[0].ScopeType != model.RoleScopeInstitution ||
		visibilityEvents[0].ScopeID != installation.Institution.ID.String() {
		t.Fatalf("teacher student visibility audit = %#v", visibilityEvents)
	}
	hidden := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+unrelated.ID.String(), nil,
		teacherLogin.Tokens.AccessToken,
	)
	if hidden.Code != http.StatusForbidden {
		t.Fatalf("unrelated student visibility = %d: %s", hidden.Code, hidden.Body.String())
	}
	patchUser := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/users/"+student.ID.String(),
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
	if len(foundUsers) != 1 || foundUsers[0].ID != student.ID {
		t.Fatalf("searched users = %#v", foundUsers)
	}
	endUnitMembership := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/academic-unit-members/"+unitMember.ID,
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
		"/api/v1/academic-units/"+child.ID.String()+"/children",
		strings.NewReader("{"),
	)
	malformed.Header.Set("Authorization", "Bearer "+teacherLogin.Tokens.AccessToken)
	malformed.Header.Set("Content-Type", "application/json")
	malformedResponse := httptest.NewRecorder()
	handler.ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"malformed academic unit body was not rejected at transport: %d: %s",
			malformedResponse.Code,
			malformedResponse.Body.String(),
		)
	}

	activeMembers := performJSONRequest(
		handler, http.MethodGet, "/api/v1/classes/"+secondClass.ID.String()+"/members", nil,
		adminToken,
	)
	if activeMembers.Code != http.StatusOK {
		t.Fatalf("active class members = %d: %s", activeMembers.Code, activeMembers.Body.String())
	}
	var members []membershipWire
	decodeIntegrationResponse(t, activeMembers, &members)
	if len(members) != 1 || members[0].UserID != student.ID.String() {
		t.Fatalf("active class members = %#v", members)
	}

	disabledLogin := loginIntegrationUser(
		t, handler, disabled.Username, password,
		model.SessionClientCLI, "disabled-cli",
	)
	disableResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/users/"+disabled.ID.String()+"/disable", nil,
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
	refreshRevoked := performJSONRequest(
		handler, http.MethodPost, "/api/v1/auth/refresh", nil,
		disabledLogin.Tokens.RefreshToken,
	)
	if refreshRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("disabled refresh remained valid = %d: %s", refreshRevoked.Code, refreshRevoked.Body.String())
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
		"/api/v1/users/"+student.ID.String()+"/sessions",
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
		"/api/v1/users/"+student.ID.String()+"/sessions",
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
		"/api/v1/users/"+student.ID.String()+"/sessions/"+firstStudentSession.Session.ID.String(),
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
		"/api/v1/users/"+student.ID.String()+"/sessions/revoke-all",
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
	var wire wireAcademicResourceResponse
	switch target := any(&result).(type) {
	case *model.AcademicUnit:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.AcademicUnit{
			ID: model.AcademicUnitID(wire.ID), InstitutionID: model.InstitutionID(wire.InstitutionID),
			ParentID: model.AcademicUnitID(wire.ParentID), Name: wire.Name,
			DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: model.TimeFromMillis(wire.CreateAt), UpdatedAt: model.TimeFromMillis(wire.UpdateAt),
			ArchivedAt: model.OptionalTimeFromMillis(wire.DeleteAt),
		}
	case *model.Programme:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.Programme{
			ID: model.ProgrammeID(wire.ID), AcademicUnitID: model.AcademicUnitID(wire.AcademicUnitID),
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: model.TimeFromMillis(wire.CreateAt), UpdatedAt: model.TimeFromMillis(wire.UpdateAt),
			ArchivedAt: model.OptionalTimeFromMillis(wire.DeleteAt),
		}
	case *model.ProgrammeLevel:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.ProgrammeLevel{
			ID: model.ProgrammeLevelID(wire.ID), ProgrammeID: model.ProgrammeID(wire.ProgrammeID),
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: model.TimeFromMillis(wire.CreateAt), UpdatedAt: model.TimeFromMillis(wire.UpdateAt),
			ArchivedAt: model.OptionalTimeFromMillis(wire.DeleteAt),
		}
	case *model.AcademicPeriod:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.AcademicPeriod{
			ID: model.AcademicPeriodID(wire.ID), InstitutionID: model.InstitutionID(wire.InstitutionID),
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			StartsAt: model.TimeFromMillis(wire.StartAt), EndsAt: model.TimeFromMillis(wire.EndAt),
			CreatedAt: model.TimeFromMillis(wire.CreateAt), UpdatedAt: model.TimeFromMillis(wire.UpdateAt),
			ArchivedAt: model.OptionalTimeFromMillis(wire.DeleteAt),
		}
	case *model.Class:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.Class{
			ID: model.ClassID(wire.ID), ProgrammeLevelID: model.ProgrammeLevelID(wire.ProgrammeLevelID),
			AcademicPeriodID: model.AcademicPeriodID(wire.AcademicPeriodID),
			Name:             wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: model.TimeFromMillis(wire.CreateAt), UpdatedAt: model.TimeFromMillis(wire.UpdateAt),
			ArchivedAt: model.OptionalTimeFromMillis(wire.DeleteAt),
		}
	default:
		decodeIntegrationResponse(t, response, &result)
	}
	return &result
}

type wireAcademicResourceResponse struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	InstitutionID    string `json:"institution_id"`
	ParentID         string `json:"parent_id"`
	AcademicUnitID   string `json:"academic_unit_id"`
	ProgrammeID      string `json:"programme_id"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
	StartAt          int64  `json:"start_at"`
	EndAt            int64  `json:"end_at"`
}

func decodeIntegrationResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	switch typed := target.(type) {
	case *[]*model.User:
		var wire []*wireUserProfileResponse
		if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		*typed = make([]*model.User, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, item.model())
		}
		return
	case *[]*model.Session:
		var wire []*wireSessionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		*typed = make([]*model.Session, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, item.model())
		}
		return
	case *[]*model.AcademicUnitMember:
		var wire []struct {
			ID             string `json:"id"`
			CreateAt       int64  `json:"create_at"`
			UpdateAt       int64  `json:"update_at"`
			DeleteAt       int64  `json:"delete_at"`
			AcademicUnitID string `json:"academic_unit_id"`
			UserID         string `json:"user_id"`
			StartAt        int64  `json:"start_at"`
			EndAt          int64  `json:"end_at"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		*typed = make([]*model.AcademicUnitMember, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, &model.AcademicUnitMember{
				ID: model.AcademicUnitMemberID(item.ID), AcademicUnitID: model.AcademicUnitID(item.AcademicUnitID),
				UserID: model.UserID(item.UserID), CreatedAt: model.TimeFromMillis(item.CreateAt),
				UpdatedAt: model.TimeFromMillis(item.UpdateAt), ArchivedAt: model.OptionalTimeFromMillis(item.DeleteAt),
				StartsAt: model.TimeFromMillis(item.StartAt), EndsAt: model.OptionalTimeFromMillis(item.EndAt),
			})
		}
		return
	}
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}
