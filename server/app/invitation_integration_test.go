//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestStudentClassInvitationIssuesMailAndAcceptsAcrossNodes(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
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
			"start_at": now - 60_000, "end_at": now + 31_536_000_000}, token)
	class := createIntegrationResource[model.Class](t, primary.Handler(), http.MethodPost,
		"/api/v1/programme-levels/"+level.ID.String()+"/classes",
		map[string]any{"academic_period_id": period.ID.String(), "name": "class-a", "display_name": "Class A"}, token)

	issue := performJSONRequest(primary.Handler(), http.MethodPost,
		"/api/v1/classes/"+class.ID.String()+"/invitations/student",
		map[string]any{"email": " invited.student@example.edu ", "suggested_username": "invited-student",
			"suggested_display_name": "Invited Student", "suggested_locale": "en"}, token)
	if issue.Code != http.StatusCreated {
		t.Fatalf("issue invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	}
	if strings.Contains(issue.Body.String(), "invited.student@example.edu") || strings.Contains(issue.Body.String(), "token") {
		t.Fatalf("issue response disclosed mailbox or claim: %s", issue.Body.String())
	}

	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	if strings.Contains(primary.Logs.String(), claim) || strings.Contains(secondary.Logs.String(), claim) {
		t.Fatal("invitation claim appeared in logs")
	}
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/student-class/accept",
		map[string]any{"claim": claim, "password": "student correct horse battery staple", "username": "invited-student",
			"display_name": "Invited Student", "locale": "en", "timezone": "UTC"}, "")
	if accept.Code != http.StatusOK {
		t.Fatalf("accept invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	}

	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	if err != nil || accepted.State != model.InvitationAccepted || !accepted.AcceptedUserID.IsValid() {
		t.Fatalf("accepted invitation = %#v, %v", accepted, err)
	}
	user, err := primaryStore.User().Get(context.Background(), accepted.AcceptedUserID.String())
	if err != nil || user.Email != "invited.student@example.edu" || !user.EmailVerified {
		t.Fatalf("accepted user = %#v, %v", user, err)
	}
	members, err := primaryStore.ClassMember().ListByClass(context.Background(), class.ID.String(), model.GetMillis())
	if err != nil || len(members) != 1 || members[0].UserID != user.ID {
		t.Fatalf("class members = %#v, %v", members, err)
	}
	if notice := waitForInvitationDeliveryContaining(t, primary, secondary, "Invitation accepted"); notice == nil {
		t.Fatal("acceptance notice lacks semantic copy")
	}

	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/student-class/accept",
		map[string]any{"claim": claim, "password": "student correct horse battery staple", "username": "invited-student",
			"display_name": "Invited Student", "locale": "en", "timezone": "UTC"}, "")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("acceptance replay = %d: %s", replay.Code, replay.Body.String())
	}
}

func TestTeacherAcademicUnitInvitationIssuesMailAndAcceptsAcrossNodes(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	login := loginIntegrationUser(t, primary.Handler(), "teacher-invitation-admin", adminPassword, model.SessionClientCLI, "teacher-invitation-admin-cli")
	token := login.Tokens.AccessToken
	unit := createIntegrationResource[model.AcademicUnit](t, primary.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "teacher-science", "display_name": "Teacher Science"}, token)
	roleResponse := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "invited-teacher", "display_name": "Invited Teacher",
		"permissions": []string{string(model.ActionAcademicUnitView), string(model.ActionProgrammeView)},
	}, token)
	if roleResponse.Code != http.StatusCreated {
		t.Fatalf("create teacher Role = %d: %s", roleResponse.Code, roleResponse.Body.String())
	}
	var role struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(roleResponse.Body.Bytes(), &role); err != nil {
		t.Fatal(err)
	}
	issue := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/academic-units/"+unit.ID.String()+"/invitations/teacher",
		map[string]any{"email": " invited.teacher@example.edu ", "role_id": role.ID, "suggested_username": "invited-teacher",
			"suggested_display_name": "Invited Teacher", "suggested_locale": "en"}, token)
	if issue.Code != http.StatusCreated || strings.Contains(issue.Body.String(), "invited.teacher@example.edu") || strings.Contains(issue.Body.String(), "token") {
		t.Fatalf("issue teacher invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	}
	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept",
		map[string]any{"claim": claim, "password": "teacher correct horse battery staple", "username": "invited-teacher",
			"display_name": "Invited Teacher", "locale": "en", "timezone": "UTC"}, "")
	if accept.Code != http.StatusOK {
		t.Fatalf("accept teacher invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	}
	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	if err != nil || accepted.State != model.InvitationAccepted || !accepted.AcceptedAcademicUnitMemberID.IsValid() || !accepted.AcceptedRoleBindingID.IsValid() {
		t.Fatalf("accepted teacher invitation = %#v, %v", accepted, err)
	}
	binding, err := primaryStore.RoleBinding().Get(context.Background(), accepted.AcceptedRoleBindingID.String())
	if err != nil || binding.UserID != accepted.AcceptedUserID || binding.RoleID.String() != role.ID || binding.OriginInvitationID != accepted.ID {
		t.Fatalf("teacher package Role Binding = %#v, %v", binding, err)
	}
	if notice := waitForInvitationDeliveryContaining(t, primary, secondary, "Invitation accepted"); notice == nil {
		t.Fatal("teacher acceptance notice lacks semantic copy")
	}
	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/teacher-academic-unit/accept",
		map[string]any{"claim": claim, "password": "teacher correct horse battery staple", "username": "invited-teacher"}, "")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("teacher acceptance replay = %d: %s", replay.Code, replay.Body.String())
	}
}

func TestScopedRoleInvitationAcceptsAsExistingUserAcrossNodesWithoutProfileOrWelcomeMutation(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	login := loginIntegrationUser(t, primary.Handler(), "scoped-role-admin", adminPassword, model.SessionClientCLI, "scoped-role-admin-cli")
	token := login.Tokens.AccessToken
	unit := createIntegrationResource[model.AcademicUnit](t, primary.Handler(), http.MethodPost, "/api/v1/academic-units",
		map[string]any{"name": "scoped-role-science", "display_name": "Scoped Role Science"}, token)
	roleResponse := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "academic-auditor", "display_name": "Academic Auditor",
		"permissions": []string{string(model.ActionAcademicAuditView)},
	}, token)
	if roleResponse.Code != http.StatusCreated {
		t.Fatalf("create scoped Role = %d: %s", roleResponse.Code, roleResponse.Body.String())
	}
	var role struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(roleResponse.Body.Bytes(), &role); err != nil {
		t.Fatal(err)
	}
	issue := performJSONRequest(primary.Handler(), http.MethodPost,
		"/api/v1/academic-units/"+unit.ID.String()+"/invitations/role",
		map[string]any{"email": "proof-mailbox@example.edu", "role_id": role.ID}, token)
	if issue.Code != http.StatusCreated || strings.Contains(issue.Body.String(), "proof-mailbox@example.edu") || strings.Contains(issue.Body.String(), "claim") {
		t.Fatalf("issue scoped Role Invitation = %d: %s; logs=%s", issue.Code, issue.Body.String(), primary.Logs.String())
	}
	deliveries := waitForInvitationDeliveries(t, primary, secondary, 1)
	claim := credentialFromDelivery(t, deliveries[0])
	accept := performJSONRequest(secondary.Handler(), http.MethodPost, "/api/v1/invitations/academic-unit-role/accept",
		map[string]any{"claim": claim}, token)
	if accept.Code != http.StatusOK || !strings.Contains(accept.Body.String(), `"user_id":"`+login.User.ID.String()+`"`) ||
		strings.Contains(accept.Body.String(), "proof-mailbox@example.edu") || strings.Contains(accept.Body.String(), claim) {
		t.Fatalf("accept scoped Role Invitation = %d: %s; logs=%s", accept.Code, accept.Body.String(), secondary.Logs.String())
	}
	accepted, err := secondaryStore.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	if err != nil || accepted.State != model.InvitationAccepted || accepted.AcceptedUserID != login.User.ID || !accepted.AcceptedRoleBindingID.IsValid() ||
		accepted.AcceptedAffiliationID.IsValid() || accepted.AcceptedAcademicUnitMemberID.IsValid() {
		t.Fatalf("accepted scoped Role Invitation = %#v, %v", accepted, err)
	}
	current, err := primaryStore.User().Get(context.Background(), login.User.ID.String())
	if err != nil || current.Email != "canonical-admin@example.edu" || current.Username != "scoped-role-admin" {
		t.Fatalf("canonical User changed during scoped Role acceptance = %#v, %v", current, err)
	}
	time.Sleep(300 * time.Millisecond)
	if after := len(primary.Mailer.Deliveries()) + len(secondary.Mailer.Deliveries()); after != len(deliveries) {
		t.Fatalf("existing-User scoped Role acceptance emitted redundant mail: deliveries=%d, want %d", after, len(deliveries))
	}
	replay := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/invitations/academic-unit-role/accept",
		map[string]any{"claim": claim}, token)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("scoped Role acceptance replay = %d: %s", replay.Code, replay.Body.String())
	}
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
		if time.Now().After(deadline) {
			t.Fatalf("invitation mail deliveries = %d, want at least %d; primary logs=%s; secondary logs=%s",
				len(deliveries), count, primary.Logs.String(), secondary.Logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
