//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestInvitationBatchCommitsMixedRowsAndRecoversAcrossRequests(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	server := testlib.Setup(t, testlib.WithConfig(func(cfg *config.Config) {
		cfg.Cluster.NodeID = "invitation-batch-node"
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.PublicURL = "https://proctor.example.edu"
	}), testlib.WithStore(persistence))
	startIntegrationServer(t, server)

	const password = "bootstrap correct horse battery staple"
	bootstrap := performJSONRequest(server.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "batch-university", "display_name": "Batch University"},
		"administrator":    map[string]any{"username": "batch-admin", "email": "batch-admin@example.edu"},
		"password":         password,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	login := loginIntegrationUser(t, server.Handler(), "batch-admin", password, model.SessionClientCLI, "batch-admin-cli")
	token := login.Tokens.AccessToken
	unit := createIntegrationResource[model.AcademicUnit](t, server.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "batch-science", "display_name": "Batch Science"}, token)
	programme := createIntegrationResource[model.Programme](t, server.Handler(), http.MethodPost,
		"/api/v1/academic-units/"+unit.ID.String()+"/programmes", map[string]any{"name": "batch-computing", "display_name": "Batch Computing"}, token)
	level := createIntegrationResource[model.ProgrammeLevel](t, server.Handler(), http.MethodPost,
		"/api/v1/programmes/"+programme.ID.String()+"/levels", map[string]any{"name": "batch-year-one", "display_name": "Batch Year One"}, token)
	now := model.GetMillis()
	period := createIntegrationResource[model.AcademicPeriod](t, server.Handler(), http.MethodPost, "/api/v1/academic-periods",
		map[string]any{"owner_type": "academic_unit", "owner_id": unit.ID.String(), "name": "batch-2026", "display_name": "Batch 2026",
			"start_at": integrationWireTime(now - 60_000), "end_at": integrationWireTime(now + 31_536_000_000)}, token)
	class := createIntegrationResource[model.Class](t, server.Handler(), http.MethodPost,
		"/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{"academic_period_id": period.ID.String(), "name": "batch-class", "display_name": "Batch Class"}, token)

	invalidRoleID := model.NewRoleID().String()
	body := map[string]any{"operation": "student_class.create", "scope_type": "class", "scope_id": class.ID.String(),
		"items": []map[string]any{
			{"key": "row-1", "email": "batch-first@example.edu", "suggested_username": "batch-first"},
			{"key": "row-2", "email": "batch-invalid@example.edu", "role_id": invalidRoleID},
			{"key": "row-3", "email": " BATCH-FIRST@example.edu "},
			{"key": "row-4", "email": "batch-second@example.edu", "suggested_username": "batch-second"},
		}}
	first := performIdempotentJSONRequest(server.Handler(), "/api/v1/invitation-batches", body, token, "batch-request")
	require.Equal(t, http.StatusOK, first.Code, "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	require.Contains(t, first.Body.String(), `"succeeded":2`, "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	require.Contains(t, first.Body.String(), `"failed":2`, "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	require.Contains(t, first.Body.String(), `"error_code":"request.invalid"`, "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	require.Contains(t, first.Body.String(), `"error_code":"onboarding_batch.duplicate"`, "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	require.NotContains(t, first.Body.String(), "@example.edu", "first Invitation batch = %d: %s; logs=%s", first.Code, first.Body.String(), server.Logs.String())
	page, err := persistence.Invitation().List(context.Background(), store.InvitationListOptions{
		Visibility: store.InvitationVisibilityScope{ClassIDs: []model.ClassID{class.ID}}, Limit: 10})
	require.NoError(t, err, "persisted batch Invitations = %#v", page)
	require.Len(t, page.Items, 2, "persisted batch Invitations = %#v", page)
	var unsafeOutcomes int
	err = persistence.GetMaster().Get(context.Background(), &unsafeOutcomes, `SELECT COUNT(*) FROM command_outcomes
		WHERE operation LIKE 'invitation.%' AND (outcome::text LIKE '%claim_hash%' OR outcome::text LIKE ? OR outcome::text LIKE '%batch-first@example.edu%')`,
		"%"+page.Items[0].Invitation.ClaimHash+"%")
	require.NoError(t, err, "unsafe Invitation command outcomes")
	require.Equal(t, 0, unsafeOutcomes, "unsafe Invitation command outcomes")

	reordered := map[string]any{"operation": "student_class.create", "scope_type": "class", "scope_id": class.ID.String(),
		"items": []map[string]any{
			{"key": "row-3", "email": " BATCH-FIRST@example.edu "},
			{"key": "row-4", "email": "batch-second@example.edu", "suggested_username": "batch-second"},
			{"key": "row-2", "email": "batch-invalid@example.edu", "role_id": invalidRoleID},
			{"key": "row-1", "email": "batch-first@example.edu", "suggested_username": "batch-first"},
		}}
	replay := performIdempotentJSONRequest(server.Handler(), "/api/v1/invitation-batches", reordered, token, "batch-request")
	require.Equal(t, http.StatusOK, replay.Code, "replayed Invitation batch")
	require.Contains(t, replay.Body.String(), `"no_op":2`, "replayed Invitation batch")
	require.Contains(t, replay.Body.String(), `"failed":2`, "replayed Invitation batch")
	page, err = persistence.Invitation().List(context.Background(), store.InvitationListOptions{
		Visibility: store.InvitationVisibilityScope{ClassIDs: []model.ClassID{class.ID}}, Limit: 10})
	require.NoError(t, err, "replayed batch Invitations = %#v", page)
	require.Len(t, page.Items, 2, "replayed batch Invitations = %#v", page)

	changed := map[string]any{"operation": "student_class.create", "scope_type": "class", "scope_id": class.ID.String(),
		"items": []map[string]any{
			{"key": "row-1", "email": "changed@example.edu", "suggested_username": "changed"},
			{"key": "row-2", "email": "batch-invalid@example.edu", "role_id": model.NewRoleID().String()},
			{"key": "row-3", "email": "changed@example.edu"},
			{"key": "row-4", "email": "batch-second@example.edu", "suggested_username": "batch-second"},
		}}
	conflict := performIdempotentJSONRequest(server.Handler(), "/api/v1/invitation-batches", changed, token, "batch-request")
	require.Equal(t, http.StatusOK, conflict.Code, "changed Invitation batch reuse")
	require.Contains(t, conflict.Body.String(), `"error_code":"idempotency.conflict"`, "changed Invitation batch reuse")
	require.Contains(t, conflict.Body.String(), `"no_op":1`, "changed Invitation batch reuse")
}

func performIdempotentJSONRequest(handler http.Handler, path string, body any, bearer, key string) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestStudentClassInvitationIssuesMailAndAcceptsAcrossNodes(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	configure := func(nodeID string) func(*config.Config) {
		return func(cfg *config.Config) {
			cfg.Cluster.NodeID = nodeID
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}
	}
	primary := testlib.Setup(t, testlib.WithConfig(configure("invitation-node-a")), testlib.WithStore(primaryStore))
	secondary := testlib.Setup(t, testlib.WithConfig(configure("invitation-node-b")), testlib.WithStore(secondaryStore))
	startIntegrationServer(t, primary)
	startIntegrationServer(t, secondary)

	const adminPassword = "bootstrap correct horse battery staple"
	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "invitation-university", "display_name": "Invitation University"},
		"administrator":    map[string]any{"username": "invitation-admin", "email": "invitation-admin@example.edu"},
		"password":         adminPassword,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	login := loginIntegrationUser(t, primary.Handler(), "invitation-admin", adminPassword, model.SessionClientCLI, "invitation-admin-cli")
	token := login.Tokens.AccessToken

	unit := createIntegrationResource[model.AcademicUnit](t, primary.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "science", "display_name": "Science"}, token)
	programme := createIntegrationResource[model.Programme](t, primary.Handler(), http.MethodPost,
		"/api/v1/academic-units/"+unit.ID.String()+"/programmes",
		map[string]any{"name": "computing", "display_name": "Computing"}, token)
	level := createIntegrationResource[model.ProgrammeLevel](t, primary.Handler(), http.MethodPost,
		"/api/v1/programmes/"+programme.ID.String()+"/levels",
		map[string]any{"name": "year-one", "display_name": "Year One"}, token)
	now := model.GetMillis()
	period := createIntegrationResource[model.AcademicPeriod](t, primary.Handler(), http.MethodPost, "/api/v1/academic-periods",
		map[string]any{"owner_type": "academic_unit", "owner_id": unit.ID.String(), "name": "2026", "display_name": "2026",
			"start_at": integrationWireTime(now - 60_000), "end_at": integrationWireTime(now + 31_536_000_000)}, token)
	class := createIntegrationResource[model.Class](t, primary.Handler(), http.MethodPost,
		"/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{"academic_period_id": period.ID.String(), "name": "class-a", "display_name": "Class A"}, token)

	issue := performJSONRequest(primary.Handler(), http.MethodPost,
		"/api/v1/classes/"+class.ID.String()+"/invitations/student",
		map[string]any{"email": " invited.student@example.edu ", "suggested_username": "invited-student",
			"suggested_display_name": "Invited Student", "suggested_locale": "en"}, token)
	require.Equal(t, http.StatusCreated, issue.Code, "issue invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	require.NotContains(t, issue.Body.String(), "invited.student@example.edu")
	require.NotContains(t, issue.Body.String(), "token")

	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	require.NotContains(t, primary.Logs.String(), claim, "invitation claim appeared in logs")
	require.NotContains(t, secondary.Logs.String(), claim, "invitation claim appeared in logs")
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/student-class/accept",
		map[string]any{"claim": claim, "password": "student correct horse battery staple", "username": "invited-student",
			"display_name": "Invited Student", "locale": "en", "timezone": "UTC"}, "")
	require.Equal(t, http.StatusOK, accept.Code, "accept invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())

	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	require.NoError(t, err, "accepted invitation = %#v", accepted)
	require.Equal(t, model.InvitationAccepted, accepted.State, "accepted invitation = %#v", accepted)
	require.True(t, accepted.AcceptedUserID.IsValid(), "accepted invitation = %#v", accepted)
	user, err := primaryStore.User().Get(context.Background(), accepted.AcceptedUserID)
	require.NoError(t, err, "accepted user = %#v", user)
	require.Equal(t, "invited.student@example.edu", user.Email, "accepted user = %#v", user)
	require.True(t, user.EmailVerified, "accepted user = %#v", user)
	members, err := primaryStore.ClassMember().ListByClass(context.Background(), class.ID, model.GetMillis())
	require.NoError(t, err, "class members")
	require.Len(t, members, 1, "class members")
	require.Equal(t, user.ID, members[0].UserID, "class members")
	notice := waitForInvitationDeliveryContaining(t, primary, secondary, "Invitation accepted")
	require.NotNil(t, notice, "acceptance notice lacks semantic copy")

	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/student-class/accept",
		map[string]any{"claim": claim, "password": "student correct horse battery staple", "username": "invited-student",
			"display_name": "Invited Student", "locale": "en", "timezone": "UTC"}, "")
	require.Equal(t, http.StatusOK, replay.Code, "acceptance replay")
	require.Contains(t, replay.Body.String(), `"replayed":true`, "acceptance replay")
}

func TestTeacherAcademicUnitInvitationIssuesMailAndAcceptsAcrossNodes(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	configure := func(nodeID string) func(*config.Config) {
		return func(cfg *config.Config) {
			cfg.Cluster.NodeID = nodeID
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}
	}
	primary := testlib.Setup(t, testlib.WithConfig(configure("teacher-invitation-node-a")), testlib.WithStore(primaryStore))
	secondary := testlib.Setup(t, testlib.WithConfig(configure("teacher-invitation-node-b")), testlib.WithStore(secondaryStore))
	startIntegrationServer(t, primary)
	startIntegrationServer(t, secondary)

	const adminPassword = "bootstrap correct horse battery staple"
	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "teacher-invitation-university", "display_name": "Teacher Invitation University"},
		"administrator":    map[string]any{"username": "teacher-invitation-admin", "email": "teacher-invitation-admin@example.edu"},
		"password":         adminPassword,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	login := loginIntegrationUser(t, primary.Handler(), "teacher-invitation-admin", adminPassword, model.SessionClientCLI, "teacher-invitation-admin-cli")
	token := login.Tokens.AccessToken
	unit := createIntegrationResource[model.AcademicUnit](t, primary.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "teacher-science", "display_name": "Teacher Science"}, token)
	roleResponse := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "invited-teacher", "display_name": "Invited Teacher",
		"permissions": []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeView)},
	}, token)
	require.Equal(t, http.StatusCreated, roleResponse.Code, "create teacher Role = %d: %s", roleResponse.Code, roleResponse.Body.String())
	var role struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(roleResponse.Body.Bytes(), &role))
	issue := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/academic-units/"+unit.ID.String()+"/invitations/teacher",
		map[string]any{"email": " invited.teacher@example.edu ", "role_id": role.ID, "suggested_username": "invited-teacher",
			"suggested_display_name": "Invited Teacher", "suggested_locale": "en"}, token)
	require.Equal(t, http.StatusCreated, issue.Code, "issue teacher invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	require.NotContains(t, issue.Body.String(), "invited.teacher@example.edu", "issue teacher invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	require.NotContains(t, issue.Body.String(), "token", "issue teacher invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept",
		map[string]any{"claim": claim, "password": "teacher correct horse battery staple", "username": "invited-teacher",
			"display_name": "Invited Teacher", "locale": "en", "timezone": "UTC"}, "")
	require.Equal(t, http.StatusOK, accept.Code, "accept teacher invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	require.NoError(t, err, "accepted teacher invitation = %#v", accepted)
	require.Equal(t, model.InvitationAccepted, accepted.State, "accepted teacher invitation = %#v", accepted)
	require.True(t, accepted.AcceptedAcademicUnitMemberID.IsValid(), "accepted teacher invitation = %#v", accepted)
	require.True(t, accepted.AcceptedRoleBindingID.IsValid(), "accepted teacher invitation = %#v", accepted)
	binding, err := primaryStore.RoleBinding().Get(context.Background(), accepted.AcceptedRoleBindingID)
	require.NoError(t, err, "teacher package Role Binding = %#v", binding)
	require.Equal(t, accepted.AcceptedUserID, binding.UserID, "teacher package Role Binding = %#v", binding)
	require.Equal(t, role.ID, binding.RoleID.String(), "teacher package Role Binding = %#v", binding)
	require.Equal(t, accepted.ID, binding.OriginInvitationID, "teacher package Role Binding = %#v", binding)
	notice := waitForInvitationDeliveryContaining(t, primary, secondary, "Invitation accepted")
	require.NotNil(t, notice, "teacher acceptance notice lacks semantic copy")
	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept",
		map[string]any{"claim": claim, "password": "teacher correct horse battery staple", "username": "invited-teacher"}, "")
	require.Equal(t, http.StatusOK, replay.Code, "teacher acceptance replay")
	require.Contains(t, replay.Body.String(), `"replayed":true`, "teacher acceptance replay")
	waitForInvitationDeliveries(t, primary, secondary, 2)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 2, (len(primary.Mailer.Deliveries()) + len(secondary.Mailer.Deliveries())), "teacher invitation produced")
}

func TestScopedRoleInvitationAcceptsAsExistingUserAcrossNodesWithoutProfileOrWelcomeMutation(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	configure := func(nodeID string) func(*config.Config) {
		return func(cfg *config.Config) {
			cfg.Cluster.NodeID = nodeID
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Server.PublicURL = "https://proctor.example.edu"
			cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts = 20
			cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 100
		}
	}
	primary := testlib.Setup(t, testlib.WithConfig(configure("scoped-role-invitation-node-a")), testlib.WithStore(primaryStore))
	secondary := testlib.Setup(t, testlib.WithConfig(configure("scoped-role-invitation-node-b")), testlib.WithStore(secondaryStore))
	startIntegrationServer(t, primary)
	startIntegrationServer(t, secondary)

	const adminPassword = "bootstrap correct horse battery staple"
	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "scoped-role-university", "display_name": "Scoped Role University"},
		"administrator":    map[string]any{"username": "scoped-role-admin", "email": "canonical-admin@example.edu"},
		"password":         adminPassword,
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	login := loginIntegrationUser(t, primary.Handler(), "scoped-role-admin", adminPassword, model.SessionClientCLI, "scoped-role-admin-cli")
	token := login.Tokens.AccessToken
	unit := createIntegrationResource[model.AcademicUnit](t, primary.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "scoped-role-science", "display_name": "Scoped Role Science"}, token)
	roleResponse := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "academic-auditor", "display_name": "Academic Auditor",
		"permissions": []string{string(model.ActionAcademicAuditView)},
	}, token)
	require.Equal(t, http.StatusCreated, roleResponse.Code, "create scoped Role = %d: %s", roleResponse.Code, roleResponse.Body.String())
	var role struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(roleResponse.Body.Bytes(), &role))
	issue := performJSONRequest(primary.Handler(), http.MethodPost,
		"/api/v1/academic-units/"+unit.ID.String()+"/invitations/role",
		map[string]any{"email": "proof-mailbox@example.edu", "role_id": role.ID}, token)
	require.Equal(t, http.StatusCreated, issue.Code, "issue scoped Role Invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	require.NotContains(t, issue.Body.String(), "proof-mailbox@example.edu", "issue scoped Role Invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	require.NotContains(t, issue.Body.String(), "claim", "issue scoped Role Invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/academic-unit-role/accept",
		map[string]any{"claim": claim}, token)
	require.Equal(t, http.StatusOK, accept.Code, "accept scoped Role Invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	require.Contains(t, accept.Body.String(), `"user_id":"`+login.User.ID.String()+`"`, "accept scoped Role Invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	require.NotContains(t, accept.Body.String(), "proof-mailbox@example.edu", "accept scoped Role Invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	require.NotContains(t, accept.Body.String(), claim, "accept scoped Role Invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	require.NoError(t, err, "accepted scoped Role Invitation = %#v", accepted)
	require.Equal(t, model.InvitationAccepted, accepted.State, "accepted scoped Role Invitation = %#v", accepted)
	require.Equal(t, login.User.ID, accepted.AcceptedUserID, "accepted scoped Role Invitation = %#v", accepted)
	require.True(t, accepted.AcceptedRoleBindingID.IsValid(), "accepted scoped Role Invitation = %#v", accepted)
	require.False(t, accepted.AcceptedAffiliationID.IsValid(), "accepted scoped Role Invitation = %#v", accepted)
	require.False(t, accepted.AcceptedAcademicUnitMemberID.IsValid(), "accepted scoped Role Invitation = %#v", accepted)
	current, err := primaryStore.User().Get(context.Background(), login.User.ID)
	require.NoError(t, err, "canonical User changed during scoped Role acceptance = %#v", current)
	require.Equal(t, "canonical-admin@example.edu", current.Email, "canonical User changed during scoped Role acceptance = %#v", current)
	require.Equal(t, "scoped-role-admin", current.Username, "canonical User changed during scoped Role acceptance = %#v", current)
	time.Sleep(300 * time.Millisecond)
	require.Len(t, deliveries, (len(primary.Mailer.Deliveries()) + len(secondary.Mailer.Deliveries())), "existing-User scoped Role acceptance emitted redundant mail: deliveries")
	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/academic-unit-role/accept",
		map[string]any{"claim": claim}, token)
	require.Equal(t, http.StatusOK, replay.Code, "scoped Role acceptance replay")
	require.Contains(t, replay.Body.String(), `"replayed":true`, "scoped Role acceptance replay")
}

func waitForInvitationDeliveryContaining(t *testing.T, primary, secondary *testlib.Helper, text string) *mail.Delivery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, delivery := range append(primary.Mailer.Deliveries(), secondary.Mailer.Deliveries()...) {
			if strings.Contains(string(delivery.Data), text) {
				return &delivery
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForInvitationDeliveries(t *testing.T, primary, secondary *testlib.Helper, count int) []mail.Delivery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deliveries := append(primary.Mailer.Deliveries(), secondary.Mailer.Deliveries()...)
		if len(deliveries) >= count {
			return deliveries
		}
		require.False(t, time.Now().After(deadline), "invitation mail deliveries = %d, want at least %d; primary logs=%s; secondary logs=%s", len(deliveries), count, primary.Logs.String(), secondary.Logs.String())
		time.Sleep(20 * time.Millisecond)
	}
}
