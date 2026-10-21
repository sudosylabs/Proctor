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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAcademicMembershipAndUserAdministrationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
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
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
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
	require.Equal(t, root.ID, child.ParentID)
	require.Equal(t, installation.Institution.ID, child.InstitutionID)
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
			"start_at": integrationWireTime(now - 86_400_000), "end_at": integrationWireTime(now + 31_536_000_000),
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
			"start_at": model.FormatRFC3339(period.EndsAt.Add(time.Hour)), "end_at": model.FormatRFC3339(period.EndsAt.AddDate(1, 0, 0)),
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
			"key": "json-staff", "user_id": jsonBatchUser.ID.String(), "affiliation_kind": "staff", "start_at": integrationWireTime(now - 2_000),
		}},
	}, adminToken, "academic-json-batch")
	require.Equal(t, http.StatusOK, jsonBatch.Code, "academic JSON batch")
	require.Contains(t, jsonBatch.Body.String(), `"succeeded":1`, "academic JSON batch")
	require.NotContains(t, jsonBatch.Body.String(), jsonBatchUser.Email, "academic JSON batch")
	jsonAffiliations, err := persistence.Affiliation().ListByUser(context.Background(), jsonBatchUser.ID)
	require.NoError(t, err, "JSON batch affiliations")
	require.Len(t, jsonAffiliations, 1, "JSON batch affiliations")
	require.Equal(t, model.AffiliationStaff, jsonAffiliations[0].Kind, "JSON batch affiliations")

	csvStart := model.NowUTC().Add(-time.Minute).Format(time.RFC3339)
	csvUpload := performCSVUpload(handler,
		"/api/v1/onboarding-imports?mode=academic_administration&scope_type=institution&scope_id="+installation.Institution.ID.String(),
		"operation,user_id,affiliation_kind,start_at,reference\n"+
			"affiliation.add,"+csvBatchUser.ID.String()+",staff,"+csvStart+",csv-staff\n",
		adminToken,
	)
	require.Equal(t, http.StatusAccepted, csvUpload.Code, "academic CSV upload = %d: %s", csvUpload.Code, csvUpload.Body.String())
	var csvImport onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, csvUpload, &csvImport)
	csvImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, csvImport.ID, "preview_ready")
	require.Equal(t, 1, csvImport.TotalRows)
	require.Equal(t, 1, csvImport.ValidRows)
	require.Len(t, csvImport.Rows, 1)
	require.Equal(t, "valid", csvImport.Rows[0].Status)
	csvCommit := performIdempotentJSONRequest(handler, "/api/v1/onboarding-imports/"+csvImport.ID+"/commit", map[string]any{
		"expected_revision": csvImport.Revision, "preview_digest": csvImport.PreviewDigest, "policy": "require_all_valid",
	}, adminToken, "academic-csv-commit")
	require.Equal(t, http.StatusAccepted, csvCommit.Code, "academic CSV commit = %d: %s", csvCommit.Code, csvCommit.Body.String())
	csvImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, csvImport.ID, "completed")
	require.Equal(t, 1, csvImport.SucceededRows)
	require.Equal(t, 0, csvImport.FailedRows)
	require.NotEmpty(t, csvImport.Rows[0].ResourceID)
	csvReport := performJSONRequest(handler, http.MethodGet, "/api/v1/onboarding-imports/"+csvImport.ID+"/report", nil, adminToken)
	require.Equal(t, http.StatusOK, csvReport.Code, "academic CSV report = %d %q headers=%v", csvReport.Code, csvReport.Body.String(), csvReport.Header())
	require.Contains(t, csvReport.Body.String(), csvImport.Rows[0].ResourceID, "academic CSV report = %d %q headers=%v", csvReport.Code, csvReport.Body.String(), csvReport.Header())
	require.NotContains(t, csvReport.Body.String(), csvBatchUser.Email, "academic CSV report = %d %q headers=%v", csvReport.Code, csvReport.Body.String(), csvReport.Header())
	require.Equal(t, "no-store", csvReport.Header().Get("Cache-Control"), "academic CSV report = %d %q headers=%v", csvReport.Code, csvReport.Body.String(), csvReport.Header())
	csvAffiliations, err := persistence.Affiliation().ListByUser(context.Background(), csvBatchUser.ID)
	require.NoError(t, err, "CSV batch affiliations")
	require.Len(t, csvAffiliations, 1, "CSV batch affiliations")
	require.Equal(t, model.AffiliationStaff, csvAffiliations[0].Kind, "CSV batch affiliations")

	cancelUpload := performCSVUpload(handler,
		"/api/v1/onboarding-imports?mode=academic_administration&scope_type=institution&scope_id="+installation.Institution.ID.String(),
		"operation,user_id,affiliation_kind,start_at,reference\n"+
			"affiliation.add,"+cancelBatchUser.ID.String()+",staff,"+csvStart+",cancel-staff\n",
		adminToken,
	)
	require.Equal(t, http.StatusAccepted, cancelUpload.Code, "cancelable CSV upload = %d: %s", cancelUpload.Code, cancelUpload.Body.String())
	var cancelImport onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, cancelUpload, &cancelImport)
	cancelImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, cancelImport.ID, "preview_ready")
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	secondary := testlib.Setup(t, testlib.WithStore(secondaryStore), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Cluster.NodeID = "academic-administration-cancel-secondary"
		cfg.Server.ListenAddress = "127.0.0.1:0"
	}))
	canceled := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/onboarding-imports/"+cancelImport.ID+"/cancel", nil, adminToken)
	require.Equal(t, http.StatusOK, canceled.Code, "cross-node CSV cancellation")
	require.Contains(t, canceled.Body.String(), `"state":"canceled"`, "cross-node CSV cancellation")
	cancelImport = waitForOnboardingImportState(t, handler, adminToken, onboardingImportsCollection, cancelImport.ID, "canceled")
	require.Equal(t, 0, cancelImport.SucceededRows)
	require.Equal(t, 0, cancelImport.FailedRows)

	createIntegrationResource[map[string]any](
		t, handler, http.MethodPost, "/api/v1/users/"+student.ID.String()+"/affiliations",
		map[string]any{"kind": model.AffiliationStudent, "start_at": integrationWireTime(now - 10_000)},
		adminToken,
	)
	createIntegrationResource[map[string]any](
		t, handler, http.MethodPost, "/api/v1/users/"+teacher.ID.String()+"/affiliations",
		map[string]any{"kind": model.AffiliationTeacher, "start_at": integrationWireTime(now - 10_000)},
		adminToken,
	)
	// Decode membership wire DTOs into local shapes rather than domain models.
	type membershipWire struct {
		ID     string    `json:"id"`
		UserID string    `json:"user_id"`
		EndAt  time.Time `json:"end_at"`
	}
	type enrollmentWire struct {
		Membership membershipWire  `json:"membership"`
		Previous   *membershipWire `json:"previous,omitempty"`
	}
	unitMember := createIntegrationResource[membershipWire](
		t, handler, http.MethodPost, "/api/v1/academic-units/"+child.ID.String()+"/members",
		map[string]any{"user_id": teacher.ID.String(), "start_at": integrationWireTime(now - 10_000)},
		adminToken,
	)
	firstEnrollmentResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/classes/"+firstClass.ID.String()+"/members",
		map[string]any{"user_id": student.ID.String(), "start_at": integrationWireTime(now - 5_000)}, adminToken,
	)
	require.Equal(t, http.StatusCreated, firstEnrollmentResponse.Code, "first enrollment = %d: %s; logs: %s", firstEnrollmentResponse.Code, firstEnrollmentResponse.Body.String(), helper.Logs.String())
	var firstEnrollment enrollmentWire
	decodeIntegrationResponse(t, firstEnrollmentResponse, &firstEnrollment)
	require.Nil(t, firstEnrollment.Previous, "first enrollment = %#v", &firstEnrollment)
	transferResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/classes/"+secondClass.ID.String()+"/members",
		map[string]any{"user_id": student.ID.String(), "start_at": integrationWireTime(now - 1_000)}, adminToken,
	)
	require.Equal(t, http.StatusCreated, transferResponse.Code, "transfer enrollment = %d: %s; logs: %s", transferResponse.Code, transferResponse.Body.String(), helper.Logs.String())
	var transfer enrollmentWire
	decodeIntegrationResponse(t, transferResponse, &transfer)
	require.NotNil(t, transfer.Previous)
	require.Equal(t, firstEnrollment.Membership.ID, transfer.Previous.ID)
	require.True(t, transfer.Previous.EndAt.Equal(time.UnixMilli(now-1_000)))
	effectiveAt := destinationPeriod.StartsAt.Add(time.Hour)
	dryRun := performJSONRequest(handler, http.MethodPost, "/api/v1/student-progressions", map[string]any{
		"source_period_id": period.ID.String(), "source_class_id": secondClass.ID.String(),
		"destination_period_id": destinationPeriod.ID.String(), "destination_class_id": destinationClass.ID.String(),
		"effective_at": effectiveAt.Format(time.RFC3339Nano),
	}, adminToken)
	require.Equal(t, http.StatusAccepted, dryRun.Code, "progression dry-run = %d: %s", dryRun.Code, dryRun.Body.String())
	var progression onboardingImportIntegrationResponse
	decodeIntegrationResponse(t, dryRun, &progression)
	progression = waitForOnboardingImportState(t, handler, adminToken, studentProgressionsCollection, progression.ID, "preview_ready")
	require.Equal(t, 1, progression.TotalRows)
	require.Equal(t, 1, progression.ValidRows)
	require.Len(t, progression.Rows, 1)
	require.Equal(t, "valid", progression.Rows[0].Status)
	committedProgression := performIdempotentJSONRequest(handler, "/api/v1/student-progressions/"+progression.ID+"/commit", map[string]any{
		"expected_revision": progression.Revision, "preview_digest": progression.PreviewDigest,
	}, adminToken, "academic-progression-commit")
	require.Equal(t, http.StatusAccepted, committedProgression.Code, "progression commit = %d: %s", committedProgression.Code, committedProgression.Body.String())
	progression = waitForOnboardingImportState(t, handler, adminToken, studentProgressionsCollection, progression.ID, "completed")
	require.Equal(t, 1, progression.SucceededRows)
	require.Equal(t, 0, progression.FailedRows)
	require.Len(t, progression.Rows, 1)
	require.NotEmpty(t, progression.Rows[0].ResourceID)
	progressionReport := performJSONRequest(handler, http.MethodGet, "/api/v1/student-progressions/"+progression.ID+"/report", nil, adminToken)
	require.Equal(t, http.StatusOK, progressionReport.Code, "progression report = %d %q headers=%v", progressionReport.Code, progressionReport.Body.String(), progressionReport.Header())
	require.Contains(t, progressionReport.Body.String(), progression.Rows[0].ResourceID, "progression report = %d %q headers=%v", progressionReport.Code, progressionReport.Body.String(), progressionReport.Header())
	require.NotContains(t, progressionReport.Body.String(), student.Email, "progression report = %d %q headers=%v", progressionReport.Code, progressionReport.Body.String(), progressionReport.Header())
	require.Equal(t, "no-store", progressionReport.Header().Get("Cache-Control"), "progression report = %d %q headers=%v", progressionReport.Code, progressionReport.Body.String(), progressionReport.Header())
	progressionHistory, err := persistence.ClassMember().ListByUser(context.Background(), student.ID)
	require.NoError(t, err)
	require.Len(t, progressionHistory, 3)
	require.Equal(t, destinationClass.ID, progressionHistory[0].ClassID)
	require.Equal(t, transfer.Membership.ID, progressionHistory[1].ID.String())
	require.False(t, progressionHistory[1].EndsAt.Valid)

	teacherRole, err := storetest.SaveRole(context.Background(), persistence, &model.Role{
		Name: "class-teacher", DisplayName: "Class Teacher",
		Permissions: []string{
			string(model.ActionClassView),
			string(model.ActionClassMembersView),
		},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(context.Background(), persistence, &model.RoleBinding{
		UserID: teacher.ID, RoleID: teacherRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: root.ID.String(),
		StartsAt: model.TimeFromMillis(now - 10_000),
	})
	require.NoError(t, err)
	teacherLogin := loginIntegrationUser(
		t, handler, teacher.Username, password,
		model.SessionClientCLI, "teacher-cli",
	)
	visible := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+student.ID.String(), nil,
		teacherLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, visible.Code, "teacher student visibility = %d: %s", visible.Code, visible.Body.String())
	visibilityEvents, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		ActorID: teacher.ID,
		Action:  string(model.ActionUserView),
		Resource: &model.Resource{
			Type: model.ResourceUser,
			ID:   student.ID.String(),
		},
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, visibilityEvents, 1)
	require.Equal(t, model.AuditStatusSuccess, visibilityEvents[0].Status)
	require.Equal(t, model.RoleScopeAcademicUnit, visibilityEvents[0].ScopeType)
	require.Equal(t, root.ID.String(), visibilityEvents[0].ScopeID)
	hidden := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/"+unrelated.ID.String(), nil,
		teacherLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusForbidden, hidden.Code, "unrelated student visibility = %d: %s", hidden.Code, hidden.Body.String())
	patchUser := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/users/"+student.ID.String(),
		map[string]any{"display_name": "Student One Updated"},
		adminToken,
	)
	require.Equal(t, http.StatusOK, patchUser.Code, "patch user = %d: %s", patchUser.Code, patchUser.Body.String())
	userSearch := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users?q=Student+One+Updated&limit=10", nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, userSearch.Code, "search users = %d: %s", userSearch.Code, userSearch.Body.String())
	var foundUsers []*model.User
	decodeIntegrationResponse(t, userSearch, &foundUsers)
	require.Len(t, foundUsers, 1)
	require.Equal(t, student.ID, foundUsers[0].ID)
	endUnitMembership := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/academic-unit-members/"+unitMember.ID,
		nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, endUnitMembership.Code, "end academic unit membership = %d: %s", endUnitMembership.Code, endUnitMembership.Body.String()+"; logs="+helper.Logs.String())
	malformed := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/academic-units/"+child.ID.String()+"/children",
		strings.NewReader("{"),
	)
	malformed.Header.Set("Authorization", "Bearer "+teacherLogin.Tokens.AccessToken)
	malformed.Header.Set("Content-Type", "application/json")
	malformedResponse := httptest.NewRecorder()
	handler.ServeHTTP(malformedResponse, malformed)
	require.Equal(t, http.StatusBadRequest, malformedResponse.Code, "malformed academic unit body was not rejected at transport: %d: %s", malformedResponse.Code, malformedResponse.Body.String())

	activeMembers := performJSONRequest(
		handler, http.MethodGet, "/api/v1/classes/"+secondClass.ID.String()+"/members", nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, activeMembers.Code, "active class members = %d: %s", activeMembers.Code, activeMembers.Body.String())
	var members []membershipWire
	decodeIntegrationResponse(t, activeMembers, &members)
	require.Len(t, members, 1)
	require.Equal(t, student.ID.String(), members[0].UserID)

	disabledLogin := loginIntegrationUser(
		t, handler, disabled.Username, password,
		model.SessionClientCLI, "disabled-cli",
	)
	disableResponse := performJSONRequest(
		handler, http.MethodPost, "/api/v1/users/"+disabled.ID.String()+"/disable", nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, disableResponse.Code, "disable user = %d: %s", disableResponse.Code, disableResponse.Body.String())
	revoked := performJSONRequest(
		handler, http.MethodGet, "/api/v1/users/me", nil,
		disabledLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, revoked.Code, "disabled session remained valid = %d: %s", revoked.Code, revoked.Body.String())
	refreshRevoked := performJSONRequest(
		handler, http.MethodPost, "/api/v1/auth/refresh", nil,
		disabledLogin.Tokens.RefreshToken,
	)
	require.Equal(t, http.StatusUnauthorized, refreshRevoked.Code, "disabled refresh remained valid = %d: %s", refreshRevoked.Code, refreshRevoked.Body.String())

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
	require.Equal(t, http.StatusForbidden, teacherSessionList.Code, "teacher session list = %d: %s", teacherSessionList.Code, teacherSessionList.Body.String())
	sessionList := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/"+student.ID.String()+"/sessions",
		nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, sessionList.Code, "administrator session list = %d: %s", sessionList.Code, sessionList.Body.String())
	var studentSessions []*model.Session
	decodeIntegrationResponse(t, sessionList, &studentSessions)
	require.Len(t, studentSessions, 2)
	revokeOne := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/users/"+student.ID.String()+"/sessions/"+firstStudentSession.Session.ID.String(),
		nil,
		adminToken,
	)
	require.Equal(t, http.StatusNoContent, revokeOne.Code, "administrator session revoke = %d: %s", revokeOne.Code, revokeOne.Body.String())
	firstRevoked := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		firstStudentSession.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, firstRevoked.Code, "revoked student session")
	secondActive := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondStudentSession.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, secondActive.Code, "unrelated student session")
	revokeAll := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/"+student.ID.String()+"/sessions/revoke-all",
		nil,
		adminToken,
	)
	require.Equal(t, http.StatusNoContent, revokeAll.Code, "administrator revoke all sessions = %d: %s", revokeAll.Code, revokeAll.Body.String())
	secondRevoked := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondStudentSession.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, secondRevoked.Code, "student session survived revoke all")
	endClassMembership := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/class-members/"+transfer.Membership.ID,
		nil,
		adminToken,
	)
	require.Equal(t, http.StatusOK, endClassMembership.Code, "end class membership = %d: %s; logs: %s", endClassMembership.Code, endClassMembership.Body.String(), helper.Logs.String())
	classNoticeKeys := []model.MailTemplateKey{
		model.MailTemplateAcademicClassEnrolled,
		model.MailTemplateAcademicClassTransferred,
		model.MailTemplateAcademicClassEnrollmentEnded,
	}
	classNotices, err := persistence.Mail().ListDeliveries(context.Background(), store.MailDeliveryListOptions{
		TemplateKeys: classNoticeKeys,
		Limit:        10,
	})
	require.NoError(t, err)
	classNoticeCounts := map[model.MailTemplateKey]int{}
	for _, delivery := range classNotices {
		require.Equal(t, student.ID, delivery.TargetUserID, "Class transition notice targeted")
		classNoticeCounts[delivery.TemplateKey]++
	}
	require.Len(t, classNotices, 4)
	require.Equal(t, 2, classNoticeCounts[model.MailTemplateAcademicClassEnrolled])
	require.Equal(t, 1, classNoticeCounts[model.MailTemplateAcademicClassTransferred])
	require.Equal(t, 1, classNoticeCounts[model.MailTemplateAcademicClassEnrollmentEnded])

	mutated := map[string]bool{}
	// Background work can push early mutations beyond the first page. Keep
	// pages small enough that this assertion also exercises cursor traversal.
	options := store.AuditListOptions{Limit: 20, Visibility: store.AuditVisibilityScope{InstitutionWide: true}}
	for {
		events, listErr := persistence.Audit().List(context.Background(), options)
		require.NoError(t, listErr)
		for _, event := range events {
			if event.Status == model.AuditStatusSuccess && len(event.Result) != 0 {
				mutated[event.Action] = true
			}
		}
		if len(events) < options.Limit {
			break
		}
		last := events[len(events)-1]
		options.BeforeTime, options.BeforeID = last.CreatedAt.UnixMilli(), last.ID
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
		assert.True(t, mutated[string(action)], "missing successful mutation audit for %s", action)
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
		require.Equal(t, http.StatusOK, response.Code, "get progression = %d: %s", response.Code, response.Body.String())
		var current onboardingImportIntegrationResponse
		decodeIntegrationResponse(t, response, &current)
		if current.State == wanted {
			return current
		}
		require.NotEqual(t, "failed", current.State, "progression reached %q while waiting for %q: %#v", current.State, wanted, current)
		require.NotEqual(t, "completed_with_errors", current.State, "progression reached %q while waiting for %q: %#v", current.State, wanted, current)
		require.NotEqual(t, "canceled", current.State, "progression reached %q while waiting for %q: %#v", current.State, wanted, current)
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow(t, fmt.Sprintf("progression %s did not reach %s", id, wanted))
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
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username:    username,
		Email:       username + "@example.edu",
		DisplayName: username,
	}, Password: password})
	require.NoError(t, appErr)
	return user
}

// integrationWireTime formats a Unix-millisecond fixture as an API timestamp.
func integrationWireTime(millis int64) string {
	return model.FormatRFC3339(time.UnixMilli(millis))
}

// integrationOptionalTime maps an omitted API timestamp to absence.
func integrationOptionalTime(value time.Time) model.OptionalTime {
	if value.IsZero() {
		return model.OptionalTime{}
	}
	return model.OptionalTimeFrom(value)
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
	require.Equal(t, http.StatusCreated, response.Code, "%s %s = %d: %s", method, path, response.Code, response.Body.String())
	var result T
	var wire wireAcademicResourceResponse
	switch target := any(&result).(type) {
	case *model.AcademicUnit:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.AcademicUnit{
			ID: model.AcademicUnitID(wire.ID), InstitutionID: model.InstitutionID(wire.InstitutionID),
			ParentID: model.AcademicUnitID(wire.ParentID), Name: wire.Name,
			DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt,
			ArchivedAt: integrationOptionalTime(wire.ArchivedAt),
		}
	case *model.Programme:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.Programme{
			ID: model.ProgrammeID(wire.ID), AcademicUnitID: model.AcademicUnitID(wire.AcademicUnitID),
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt,
			ArchivedAt: integrationOptionalTime(wire.ArchivedAt),
		}
	case *model.ProgrammeLevel:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.ProgrammeLevel{
			ID: model.ProgrammeLevelID(wire.ID), ProgrammeID: model.ProgrammeID(wire.ProgrammeID),
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt,
			ArchivedAt: integrationOptionalTime(wire.ArchivedAt),
		}
	case *model.AcademicPeriod:
		decodeIntegrationResponse(t, response, &wire)
		owner, err := parseIntegrationAcademicPeriodOwner(wire.OwnerType, wire.OwnerID)
		require.NoError(t, err)
		*target = model.AcademicPeriod{
			ID: model.AcademicPeriodID(wire.ID), Owner: owner,
			Name: wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			StartsAt: wire.StartAt, EndsAt: wire.EndAt,
			CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt,
			ArchivedAt: integrationOptionalTime(wire.ArchivedAt),
		}
	case *model.Class:
		decodeIntegrationResponse(t, response, &wire)
		*target = model.Class{
			ID: model.ClassID(wire.ID), ProgrammeLevelID: model.ProgrammeLevelID(wire.ProgrammeLevelID),
			AcademicPeriodID: model.AcademicPeriodID(wire.AcademicPeriodID),
			Name:             wire.Name, DisplayName: wire.DisplayName, Description: wire.Description,
			CreatedAt: wire.CreatedAt, UpdatedAt: wire.UpdatedAt,
			ArchivedAt: integrationOptionalTime(wire.ArchivedAt),
		}
	default:
		decodeIntegrationResponse(t, response, &result)
	}
	return &result
}

type wireAcademicResourceResponse struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ArchivedAt       time.Time `json:"archived_at"`
	InstitutionID    string    `json:"institution_id"`
	OwnerType        string    `json:"owner_type"`
	OwnerID          string    `json:"owner_id"`
	ParentID         string    `json:"parent_id"`
	AcademicUnitID   string    `json:"academic_unit_id"`
	ProgrammeID      string    `json:"programme_id"`
	ProgrammeLevelID string    `json:"programme_level_id"`
	AcademicPeriodID string    `json:"academic_period_id"`
	Name             string    `json:"name"`
	DisplayName      string    `json:"display_name"`
	Description      string    `json:"description"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
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
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
		*typed = make([]*model.User, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, item.model())
		}
		return
	case *[]*model.Session:
		var wire []*wireSessionResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
		*typed = make([]*model.Session, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, item.model())
		}
		return
	case *[]*model.AcademicUnitMember:
		var wire []struct {
			ID             string    `json:"id"`
			CreatedAt      time.Time `json:"created_at"`
			UpdatedAt      time.Time `json:"updated_at"`
			ArchivedAt     time.Time `json:"archived_at"`
			AcademicUnitID string    `json:"academic_unit_id"`
			UserID         string    `json:"user_id"`
			StartAt        time.Time `json:"start_at"`
			EndAt          time.Time `json:"end_at"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
		*typed = make([]*model.AcademicUnitMember, 0, len(wire))
		for _, item := range wire {
			*typed = append(*typed, &model.AcademicUnitMember{
				ID: model.AcademicUnitMemberID(item.ID), AcademicUnitID: model.AcademicUnitID(item.AcademicUnitID),
				UserID: model.UserID(item.UserID), CreatedAt: item.CreatedAt,
				UpdatedAt: item.UpdatedAt, ArchivedAt: integrationOptionalTime(item.ArchivedAt),
				StartsAt: item.StartAt, EndsAt: integrationOptionalTime(item.EndAt),
			})
		}
		return
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), target))
}
