// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

type userProfileHTTPApplication struct {
	result        *model.User
	values        []*model.User
	searchQuery   application.SearchUsersQuery
	updateCommand application.UpdateUserProfileCommand
}

type accountStateHTTPApplication struct {
	AccountStateApplication
	result  *model.User
	command application.SetUserEnabledCommand
}

func (a *accountStateHTTPApplication) SetUserEnabled(_ context.Context, _ application.Invocation, command application.SetUserEnabledCommand) (*model.User, error) {
	a.command = command
	return a.result, nil
}

type sessionAdministrationHTTPApplication struct {
	SessionAdministrationApplication
	list             []*model.Session
	listQuery        application.ListUserSessionsQuery
	revokeCommand    application.RevokeUserSessionCommand
	revokeAllCommand application.RevokeUserSessionsCommand
}

func (a *sessionAdministrationHTTPApplication) ListUserSessions(_ context.Context, _ application.Invocation, query application.ListUserSessionsQuery) ([]*model.Session, error) {
	a.listQuery = query
	return a.list, nil
}

func (a *sessionAdministrationHTTPApplication) RevokeUserSession(_ context.Context, _ application.Invocation, command application.RevokeUserSessionCommand) error {
	a.revokeCommand = command
	return nil
}

func (a *sessionAdministrationHTTPApplication) RevokeUserSessions(_ context.Context, _ application.Invocation, command application.RevokeUserSessionsCommand) error {
	a.revokeAllCommand = command
	return nil
}

type roleHTTPApplication struct {
	RoleApplication
	list          []*model.Role
	result        *model.Role
	createCommand application.CreateRoleCommand
	updateCommand application.UpdateRoleCommand
	deleteCommand application.DeleteRoleCommand
}

func (a *roleHTTPApplication) ListRoles(context.Context, application.Invocation, application.ListRolesQuery) ([]*model.Role, error) {
	return a.list, nil
}

func (a *roleHTTPApplication) GetRole(context.Context, application.Invocation, application.GetRoleQuery) (*model.Role, error) {
	return a.result, nil
}

func (a *roleHTTPApplication) CreateRole(_ context.Context, _ application.Invocation, command application.CreateRoleCommand) (*model.Role, error) {
	a.createCommand = command
	return a.result, nil
}

func (a *roleHTTPApplication) UpdateRole(_ context.Context, _ application.Invocation, command application.UpdateRoleCommand) (*model.Role, error) {
	a.updateCommand = command
	return a.result, nil
}

func (a *roleHTTPApplication) DeleteRole(_ context.Context, _ application.Invocation, command application.DeleteRoleCommand) error {
	a.deleteCommand = command
	return nil
}

type roleBindingHTTPApplication struct {
	RoleBindingApplication
	list   []*model.RoleBinding
	result *model.RoleBinding
}

func (a *roleBindingHTTPApplication) ListRoleBindings(context.Context, application.Invocation, application.ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	return a.list, nil
}

func (a *roleBindingHTTPApplication) CreateRoleBinding(context.Context, application.Invocation, application.CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return a.result, nil
}

func (a *roleBindingHTTPApplication) EndRoleBinding(context.Context, application.Invocation, application.EndRoleBindingCommand) (*model.RoleBinding, error) {
	return a.result, nil
}

type auditListingHTTPApplication struct {
	AuditListingApplication
	list []*model.AuditEvent
}

func (a *auditListingHTTPApplication) ListAuditEvents(context.Context, application.Invocation, application.ListAuditEventsQuery) ([]*model.AuditEvent, error) {
	return a.list, nil
}

type bootstrapHTTPApplication struct {
	BootstrapApplication
	status *model.InstallationStatus
	result *model.InstallationBootstrapResult
}

func (a *bootstrapHTTPApplication) GetInstallationStatus(context.Context, application.GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	if a.status != nil {
		return a.status, nil
	}
	return &model.InstallationStatus{Initialized: false}, nil
}

func (a *bootstrapHTTPApplication) BootstrapInstallation(context.Context, application.Invocation, application.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	return a.result, nil
}

type loginRevisionHTTPApplication struct {
	Authentication
	user *model.User
}

func (a *loginRevisionHTTPApplication) Login(
	context.Context,
	application.Invocation,
	application.LoginCommand,
) (*application.LoginResult, error) {
	return &application.LoginResult{
		User:    a.user,
		Session: &model.Session{ID: model.NewSessionID()},
		Tokens:  &model.AuthenticationTokens{AccessToken: "access"},
	}, nil
}

func (a *userProfileHTTPApplication) SearchUsers(_ context.Context, _ application.Invocation, query application.SearchUsersQuery) ([]*model.User, error) {
	a.searchQuery = query
	return a.values, nil
}
func (a *userProfileHTTPApplication) GetUserProfile(context.Context, application.Invocation, application.GetUserProfileQuery) (*model.User, error) {
	return a.result, nil
}
func (a *userProfileHTTPApplication) UpdateUserProfile(_ context.Context, _ application.Invocation, command application.UpdateUserProfileCommand) (*model.User, error) {
	a.updateCommand = command
	return a.result, nil
}

func TestUserProfileHTTPUsesAllowlistedDTOAndRouteID(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	user := &model.User{ID: model.UserID(userID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 7, Username: "student", Email: "student@example.edu", DisplayName: "Student", Locale: "en", Timezone: "UTC"}
	profiles := &userProfileHTTPApplication{result: user}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: profiles, AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+userID, strings.NewReader(`{"display_name":"Updated"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if profiles.updateCommand.ID != userID || profiles.updateCommand.DisplayName == nil || *profiles.updateCommand.DisplayName != "Updated" {
		t.Fatalf("command = %#v", profiles.updateCommand)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"revision", "password", "password_hash", "external_subject", "mfa_secret", "access_token", "refresh_token"} {
		if _, exposed := body[forbidden]; exposed {
			t.Fatalf("sensitive field %q exposed: %#v", forbidden, body)
		}
	}
}

func TestLoginResponseDoesNotExposeUserRevision(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	application := &loginRevisionHTTPApplication{user: &model.User{
		ID: model.NewUserID(), Revision: 7, Username: "student",
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"login_id":"student","password":"password","client_type":"cli"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	loginHandler(application, logger, browserCookies{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("user response = %#v", body["user"])
	}
	if _, exposed := user["revision"]; exposed {
		t.Fatalf("revision exposed by login: %#v", user)
	}
}

func TestAccountDisableUsesApplicationCommandAndAllowlistedResponse(t *testing.T) {
	t.Parallel()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	accounts := &accountStateHTTPApplication{result: &model.User{ID: model.UserID(userID), Revision: 4, Username: "student", DisabledAt: model.OptionalTimeFromMillis(500)}}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport, AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{}, UserProfiles: &userProfileHTTPApplication{}, AccountStates: accounts, SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{}, BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065", MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+userID+"/disable", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if accounts.command.ID != userID || accounts.command.Enabled {
		t.Fatalf("command = %#v", accounts.command)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exposed := body["revision"]; exposed {
		t.Fatalf("revision exposed: %#v", body)
	}
}
