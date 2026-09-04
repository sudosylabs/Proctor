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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
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
	helper := testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
	}))
	startIntegrationServer(t, helper)
	handler := helper.Handler()
	password := "correct horse battery staple"

	bootstrap := performJSONRequest(handler, http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
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
			"owner_type": "academic_unit", "owner_id": child.ID.String(),
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
	destinationPeriod := createIntegrationResource[model.AcademicPeriod](
		t, handler, http.MethodPost, "/api/v1/academic-periods",
		map[string]any{
			"owner_type": "academic_unit", "owner_id": child.ID.String(),
			"name": "2027-2028", "display_name": "2027-2028",
			"start_at": period.EndsAt.Add(time.Hour).UnixMilli(), "end_at": period.EndsAt.AddDate(1, 0, 0).UnixMilli(),
		},
		adminToken,
	)
	destinationClass := createIntegrationResource[model.Class](
		t, handler, http.MethodPost, "/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{
			"academic_period_id": destinationPeriod.ID.String(),
			"name":               "class-c", "display_name": "Class C",
		},
		adminToken,
	)

	student := createIntegrationUser(t, helper, "student-one", password)
	teacher := createIntegrationUser(t, helper, "teacher-one", password)
	unrelated := createIntegrationUser(t, helper, "student-unrelated", password)
	disabled := createIntegrationUser(t, helper, "disabled-user", password)
	jsonBatchUser := createIntegrationUser(t, helper, "json-batch-user", password)
	csvBatchUser := createIntegrationUser(t, helper, "csv-batch-user", password)
	cancelBatchUser := createIntegrationUser(t, helper, "cancel-batch-user", password)

	jsonBatch := performIdempotentJSONRequest(handler, "/api/v1/academic-administration-batches", map[string]any{
		"operation": "affiliation.add", "scope_type": "institution", "scope_id": installation.Institution.ID.String(),
		"items": []map[string]any{{
			"key": "json-staff", "user_id": jsonBatchUser.ID.String(), "affiliation_kind": "staff", "start_at": now - 2_000,
		}},
	}, adminToken, "academic-json-batch")
	if jsonBatch.Code != http.StatusOK || !strings.Contains(jsonBatch.Body.String(), `"succeeded":1`) ||
		strings.Contains(jsonBatch.Body.String(), jsonBatchUser.Email) {
		t.Fatalf("academic JSON batch = %d: %s", jsonBatch.Code, jsonBatch.Body.String())
	}
	jsonAffiliations, err := persistence.Affiliation().ListByUser(context.Background(), jsonBatchUser.ID.String())
	if err != nil || len(jsonAffiliations) != 1 || jsonAffiliations[0].Kind != model.AffiliationStaff {
		t.Fatalf("JSON batch affiliations = %#v, %v", jsonAffiliations, err)
	}

	csvStart := model.NowUTC().Add(-time.Minute).Format(time.RFC3339)
	csvUpload := performCSVUpload(handler,
		"/api/v1/onboarding-imports?mode=academic_administration&scope_type=institution&scope_id="+installation.Institution.ID.String(),
		"operation,user_id,affiliation_kind,start_at,reference\n"+
			"affiliation.add,"+csvBatchUser.ID.String()+",staff,"+csvStart+",csv-staff\n",
		adminToken,
	)
	if csvUpload.Code != http.StatusAccepted {
		t.Fatalf("academic CSV upload = %d: %s", csvUpload.Code, csvUpload.Body.String())
	}
	var csvImport onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, csvUpload, &csvImport)
	csvImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, csvImport.ID, "preview_ready")
	if csvImport.TotalRows != 1 || csvImport.ValidRows != 1 || len(csvImport.Rows) != 1 || csvImport.Rows[0].Status != "valid" {
		t.Fatalf("academic CSV preview = %#v", csvImport)
	}
	csvCommit := performIdempotentJSONRequest(handler, "/api/v1/onboarding-imports/"+csvImport.ID+"/commit", map[string]any{
		"expected_revision": csvImport.Revision, "preview_digest": csvImport.PreviewDigest, "policy": "require_all_valid",
	}, adminToken, "academic-csv-commit")
	if csvCommit.Code != http.StatusAccepted {
		t.Fatalf("academic CSV commit = %d: %s", csvCommit.Code, csvCommit.Body.String())
	}
	csvImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, csvImport.ID, "completed")
	if csvImport.SucceededRows != 1 || csvImport.FailedRows != 0 || csvImport.Rows[0].ResourceID == "" {
		t.Fatalf("academic CSV completion = %#v", csvImport)
	}
	csvReport := performJSONRequest(handler, http.MethodGet, "/api/v1/onboarding-imports/"+csvImport.ID+"/report", nil, adminToken)
	if csvReport.Code != http.StatusOK || !strings.Contains(csvReport.Body.String(), csvImport.Rows[0].ResourceID) ||
		strings.Contains(csvReport.Body.String(), csvBatchUser.Email) || csvReport.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("academic CSV report = %d %q headers=%v", csvReport.Code, csvReport.Body.String(), csvReport.Header())
	}
	csvAffiliations, err := persistence.Affiliation().ListByUser(context.Background(), csvBatchUser.ID.String())
	if err != nil || len(csvAffiliations) != 1 || csvAffiliations[0].Kind != model.AffiliationStaff {
		t.Fatalf("CSV batch affiliations = %#v, %v", csvAffiliations, err)
	}

	cancelUpload := performCSVUpload(handler,
		"/api/v1/onboarding-imports?mode=academic_administration&scope_type=institution&scope_id="+installation.Institution.ID.String(),
		"operation,user_id,affiliation_kind,start_at,reference\n"+
			"affiliation.add,"+cancelBatchUser.ID.String()+",staff,"+csvStart+",cancel-staff\n",
		adminToken,
	)
	if cancelUpload.Code != http.StatusAccepted {
		t.Fatalf("cancelable CSV upload = %d: %s", cancelUpload.Code, cancelUpload.Body.String())
	}
	var cancelImport onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, cancelUpload, &cancelImport)
	cancelImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, cancelImport.ID, "preview_ready")
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Cluster.NodeID = "academic-administration-cancel-secondary"
		cfg.Server.ListenAddress = "127.0.0.1:0"
	}))
	canceled := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/onboarding-imports/"+cancelImport.ID+"/cancel", nil, adminToken)
	if canceled.Code != http.StatusOK || !strings.Contains(canceled.Body.String(), `"state":"canceled"`) {
		t.Fatalf("cross-node CSV cancellation = %d: %s", canceled.Code, canceled.Body.String())
	}
	cancelImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, cancelImport.ID, "canceled")
	if cancelImport.SucceededRows != 0 || cancelImport.FailedRows != 0 {
		t.Fatalf("canceled CSV import = %#v", cancelImport)
	}

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
	effectiveAt := destinationPeriod.StartsAt.Add(time.Hour)
	dryRun := performJSONRequest(handler, http.MethodPost, "/api/v1/student-progressions", map[string]any{
		"source_period_id": period.ID.String(), "source_class_id": secondClass.ID.String(),
		"destination_period_id": destinationPeriod.ID.String(), "destination_class_id": destinationClass.ID.String(),
		"effective_at": effectiveAt.Format(time.RFC3339Nano),
	}, adminToken)
	if dryRun.Code != http.StatusAccepted {
		t.Fatalf("progression dry-run = %d: %s", dryRun.Code, dryRun.Body.String())
	}
	var progression onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, dryRun, &progression)
	progression = waitForOnboardingImportState(t, handler, adminToken, studentProgressionsCollection, progression.ID, "preview_ready")
	if progression.TotalRows != 1 || progression.ValidRows != 1 || len(progression.Rows) != 1 || progression.Rows[0].Status != "valid" {
		t.Fatalf("progression preview = %#v", progression)
	}
	committedProgression := performIdempotentJSONRequest(handler, "/api/v1/student-progressions/"+progression.ID+"/commit", map[string]any{
		"expected_revision": progression.Revision, "preview_digest": progression.PreviewDigest,
	}, adminToken, "academic-progression-commit")
	if committedProgression.Code != http.StatusAccepted {
		t.Fatalf("progression commit = %d: %s", committedProgression.Code, committedProgression.Body.String())
	}
	progression = waitForOnboardingImportState(t, handler, adminToken, studentProgressionsCollection, progression.ID, "completed")
	if progression.SucceededRows != 1 || progression.FailedRows != 0 || len(progression.Rows) != 1 || progression.Rows[0].ResourceID == "" {
		t.Fatalf("completed progression = %#v", progression)
	}
	progressionReport := performJSONRequest(handler, http.MethodGet, "/api/v1/student-progressions/"+progression.ID+"/report", nil, adminToken)
	if progressionReport.Code != http.StatusOK || !strings.Contains(progressionReport.Body.String(), progression.Rows[0].ResourceID) ||
		strings.Contains(progressionReport.Body.String(), student.Email) || progressionReport.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("progression report = %d %q headers=%v", progressionReport.Code, progressionReport.Body.String(), progressionReport.Header())
	}
	progressionHistory, err := persistence.ClassMember().ListByUser(context.Background(), student.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(progressionHistory) != 3 || progressionHistory[0].ClassID != destinationClass.ID || progressionHistory[1].ID.String() != transfer.Membership.ID || progressionHistory[1].EndsAt.Valid {
		t.Fatalf("progression history = %#v", progressionHistory)
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
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visibilityEvents) != 1 ||
		visibilityEvents[0].Status != model.AuditStatusSuccess ||
		visibilityEvents[0].ScopeType != model.RoleScopeAcademicUnit ||
		visibilityEvents[0].ScopeID != root.ID.String() {
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
			endUnitMembership.Body.String()+"; logs="+helper.Logs.String(),
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
	endClassMembership := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/class-members/"+transfer.Membership.ID,
		nil,
		adminToken,
	)
	if endClassMembership.Code != http.StatusOK {
		t.Fatalf(
			"end class membership = %d: %s; logs: %s",
			endClassMembership.Code,
			endClassMembership.Body.String(),
			helper.Logs.String(),
		)
	}
	classNoticeKeys := []model.MailTemplateKey{
		model.MailTemplateAcademicClassEnrolled,
		model.MailTemplateAcademicClassTransferred,
		model.MailTemplateAcademicClassEnrollmentEnded,
	}
	classNotices, err := persistence.Mail().ListDeliveries(context.Background(), store.MailDeliveryListOptions{
		TemplateKeys: classNoticeKeys,
		Limit:        10,
	})
	if err != nil {
		t.Fatal(err)
	}
	classNoticeCounts := map[model.MailTemplateKey]int{}
	for _, delivery := range classNotices {
		if delivery.TargetUserID != student.ID {
			t.Fatalf("Class transition notice targeted %s, want %s", delivery.TargetUserID, student.ID)
		}
		classNoticeCounts[delivery.TemplateKey]++
	}
	if len(classNotices) != 4 ||
		classNoticeCounts[model.MailTemplateAcademicClassEnrolled] != 2 ||
		classNoticeCounts[model.MailTemplateAcademicClassTransferred] != 1 ||
		classNoticeCounts[model.MailTemplateAcademicClassEnrollmentEnded] != 1 {
		t.Fatalf("Class transition notices = %#v", classNotices)
	}

	mutated := map[string]bool{}
	// Background work can push early mutations beyond the first page. Keep
	// pages small enough that this assertion also exercises cursor traversal.
	options := store.AuditListOptions{Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}}
	for {
		events, listErr := persistence.Audit().List(context.Background(), options)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, event := range events {
			if event.Status == model.AuditStatusSuccess && len(event.Result) != 0 {
				mutated[event.Action] = true
			}
		}
		if len(events) < options.Limit {
			break
		}
		last := events[len(events)-1]
		options.BeforeTime, options.BeforeId = last.CreatedAt.UnixMilli(), last.ID.String()
	}
	for _, action := range []model.Action{
		model.ActionAcademicUnitManage,
		model.ActionAcademicUnitMembersManage,
		model.ActionProgrammeManage,
		model.ActionProgrammeLevelManage,
		model.ActionAcademicPeriodManage,
		model.ActionClassManage,
		model.ActionClassMembersManage,
		model.ActionUserManage,
		model.ActionSessionManage,
	} {
		if !mutated[string(action)] {
			t.Errorf("missing successful mutation audit for %s", action)
		}
	}
}

type onboardingImportIntegrationResponse struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	PreviewDigest string `json:"preview_digest"`
	Revision      int64  `json:"revision"`
	TotalRows     int    `json:"total_rows"`
	ValidRows     int    `json:"valid_rows"`
	SucceededRows int    `json:"succeeded_rows"`
	FailedRows    int    `json:"failed_rows"`
	Rows          []struct {
		Status     string `json:"status"`
		ResourceID string `json:"resource_id"`
	} `json:"rows"`
}

type onboardingImportHTTPCollection string

const (
	onboardingImportsCollection   onboardingImportHTTPCollection = "onboarding-imports"
	studentProgressionsCollection onboardingImportHTTPCollection = "student-progressions"
)

func waitForOnboardingImportState(t *testing.T, handler http.Handler, token string, collection onboardingImportHTTPCollection, id, wanted string) onboardingImportIntegrationResponse {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := performJSONRequest(handler, http.MethodGet, "/api/v1/"+string(collection)+"/"+id, nil, token)
		if response.Code != http.StatusOK {
			t.Fatalf("get progression = %d: %s", response.Code, response.Body.String())
		}
		var current onboardingImportIntegrationResponse
		decodeIntegrationResponse(t, response, &current)
		if current.State == wanted {
			return current
		}
		if current.State == "failed" || current.State == "completed_with_errors" || current.State == "canceled" {
			t.Fatalf("progression reached %q while waiting for %q: %#v", current.State, wanted, current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("progression %s did not reach %s", id, wanted)
	return onboardingImportIntegrationResponse{}
}

func performCSVUpload(handler http.Handler, path, body, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "text/csv; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
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
		owner, err := parseIntegrationAcademicPeriodOwner(wire.OwnerType, wire.OwnerID)
		if err != nil {
			t.Fatal(err)
		}
		*target = model.AcademicPeriod{
			ID: model.AcademicPeriodID(wire.ID), Owner: owner,
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
	OwnerType        string `json:"owner_type"`
	OwnerID          string `json:"owner_id"`
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

func parseIntegrationAcademicPeriodOwner(ownerType, ownerID string) (model.AcademicPeriodOwner, error) {
	switch model.ResourceType(ownerType) {
	case model.ResourceInstitution:
		id, err := model.ParseInstitutionID(ownerID)
		return model.NewInstitutionAcademicPeriodOwner(id), err
	case model.ResourceAcademicUnit:
		id, err := model.ParseAcademicUnitID(ownerID)
		return model.NewAcademicUnitAcademicPeriodOwner(id), err
	default:
		return model.AcademicPeriodOwner{}, fmt.Errorf("unknown academic period owner type %q", ownerType)
	}
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
