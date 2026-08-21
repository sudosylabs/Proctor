// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userProfileHTTPApplication struct {
	result             *model.User
	values             []*model.User
	searchQuery        application.SearchUsersQuery
	updateCommand      application.UpdateUserProfileCommand
	changeEmailCommand application.ChangeUserEmailCommand
	verifyEmailCommand application.VerifyUserEmailPrivilegedCommand
	uploadCommand      application.UploadProfilePictureCommand
	removeCommand      application.RemoveProfilePictureCommand
	pictureQuery       application.GetProfilePictureQuery
	pictureContent     *application.ProfilePictureContent
}

func (a *userProfileHTTPApplication) ChangeUserEmail(_ context.Context, _ application.Invocation, command application.ChangeUserEmailCommand) (*application.UserEmailState, error) {
	a.changeEmailCommand = command
	return &application.UserEmailState{UserID: a.result.ID, EmailVerified: a.result.EmailVerified}, nil
}

func (a *userProfileHTTPApplication) VerifyUserEmailPrivileged(_ context.Context, _ application.Invocation, command application.VerifyUserEmailPrivilegedCommand) (*application.UserEmailState, error) {
	a.verifyEmailCommand = command
	return &application.UserEmailState{UserID: a.result.ID, EmailVerified: a.result.EmailVerified}, nil
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
	list           []*model.Role
	result         *model.Role
	createCommand  application.CreateRoleCommand
	updateCommand  application.UpdateRoleCommand
	archiveCommand application.ArchiveRoleCommand
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

func (a *roleHTTPApplication) ArchiveRole(_ context.Context, _ application.Invocation, command application.ArchiveRoleCommand) error {
	a.archiveCommand = command
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
func (a *userProfileHTTPApplication) UploadProfilePicture(_ context.Context, _ application.Invocation, command application.UploadProfilePictureCommand) (*model.User, error) {
	a.uploadCommand = command
	return a.result, nil
}
func (a *userProfileHTTPApplication) RemoveProfilePicture(_ context.Context, _ application.Invocation, command application.RemoveProfilePictureCommand) (*model.User, error) {
	a.removeCommand = command
	return a.result, nil
}
func (a *userProfileHTTPApplication) GetProfilePicture(_ context.Context, _ application.Invocation, query application.GetProfilePictureQuery) (*application.ProfilePictureContent, error) {
	a.pictureQuery = query
	return a.pictureContent, nil
}

func TestUserProfileHTTPUsesAllowlistedDTOAndRouteID(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	user := &model.User{ID: model.UserID(userID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 7, Username: "student", Email: "student@example.edu", DisplayName: "Student", Locale: "en", Timezone: "UTC", ProfilePictureChangedAt: model.OptionalTimeFromMillis(500)}
	profiles := &userProfileHTTPApplication{result: user}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))
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
	if body["profile_picture_url"] != "/api/v1/users/"+userID+"/profile-picture?v=7" {
		t.Fatalf("profile picture URL = %#v", body["profile_picture_url"])
	}
	newer := *user
	newer.Revision++
	if first, second := userProfileResponseFromModel(user).ProfilePictureURL, userProfileResponseFromModel(&newer).ProfilePictureURL; first == second {
		t.Fatalf("successive visible revisions reused cache URL %q", first)
	}
	uploadRequest := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+userID+"/profile-picture", strings.NewReader("image"))
	uploadRequest.Header.Set("Authorization", "Bearer credential")
	uploadRequest.Header.Set("Content-Type", "image/png")
	uploadRequest.Header.Set("If-Match", `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	uploadResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	uploaded, err := io.ReadAll(profiles.uploadCommand.Body)
	if err != nil {
		t.Fatal(err)
	}
	if profiles.uploadCommand.UserID != userID || profiles.uploadCommand.Size != 5 || profiles.uploadCommand.ExpectedSHA256 != strings.Repeat("a", 64) || string(uploaded) != "image" {
		t.Fatalf("upload command = %#v body = %q", profiles.uploadCommand, uploaded)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID+"/profile-picture", nil)
	deleteRequest.Header.Set("Authorization", "Bearer credential")
	deleteRequest.Header.Set("If-Match", `"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`)
	deleteResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK || profiles.removeCommand.UserID != userID || profiles.removeCommand.ExpectedSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("delete response/command = %d / %#v", deleteResponse.Code, profiles.removeCommand)
	}
	profiles.pictureContent = &application.ProfilePictureContent{Body: io.NopCloser(strings.NewReader("webp")), MediaType: "image/webp", Size: 4, ETag: `"checksum"`}
	pictureRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/profile-picture?size=128", nil)
	pictureRequest.Header.Set("Authorization", "Bearer credential")
	pictureRequest.Header.Set("If-None-Match", `"different", W/"checksum"`)
	pictureResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(pictureResponse, pictureRequest)
	if pictureResponse.Code != http.StatusNotModified || profiles.pictureQuery.UserID != userID || profiles.pictureQuery.Size != 128 || pictureResponse.Header().Get("Cache-Control") != "private, max-age=86400" || pictureResponse.Header().Get("Content-Length") != "4" {
		t.Fatalf("profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
	}
}

func TestGenericUserProfilePatchRejectsEmailSecurityFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	profiles := &userProfileHTTPApplication{result: &model.User{ID: model.NewUserID()}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+model.NewId(), strings.NewReader(`{"email":"redirect@example.edu","email_verified":true}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || profiles.updateCommand.ID != "" {
		t.Fatalf("security-field PATCH status=%d command=%#v body=%s", response.Code, profiles.updateCommand, response.Body.String())
	}
}

func TestEmailMutationResponsesUseNarrowAccountStateProjection(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now), ClientType: model.SessionClientWeb}
	userID := model.NewUserID()
	profiles := &userProfileHTTPApplication{result: &model.User{ID: userID, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Revision: 9, Username: "private-user", Email: "private@example.edu", EmailVerified: false,
		DisplayName: "Private User", Locale: "fr", Timezone: "Europe/Paris",
		LastLoginAt: model.OptionalTimeFrom(now.Add(-time.Minute)), LastActivityAt: model.OptionalTimeFrom(now)}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))

	for _, test := range []struct {
		name, method, path, body string
		verified                 bool
	}{
		{name: "change", method: http.MethodPut, path: "/api/v1/users/" + userID.String() + "/email", body: `{"email":"new@example.edu"}`},
		{name: "privileged verify", method: http.MethodPost, path: "/api/v1/users/" + userID.String() + "/email/verify", verified: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			profiles.result.EmailVerified = test.verified
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer credential")
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["id"]) != `"`+userID.String()+`"` || string(fields["email_verified"]) != fmt.Sprintf("%t", test.verified) {
				t.Fatalf("narrow state response = %s", response.Body.Bytes())
			}
			for _, forbidden := range []string{"email", "username", "display_name", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at", "create_at", "update_at", "delete_at"} {
				if _, exposed := fields[forbidden]; exposed {
					t.Fatalf("email mutation exposed %q: %s", forbidden, response.Body.Bytes())
				}
			}
		})
	}
}

func TestUserSearchTransportPreservesIncludeDisabledRequestForApplicationAuthorization(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	profiles := &userProfileHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))
	for _, test := range []struct {
		name            string
		query           string
		includeDisabled bool
	}{
		{name: "default", query: "?limit=10"},
		{name: "explicit", query: "?limit=10&include_disabled=true", includeDisabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/users"+test.query, nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if profiles.searchQuery.IncludeDisabled != test.includeDisabled {
				t.Fatalf("IncludeDisabled = %t, want %t", profiles.searchQuery.IncludeDisabled, test.includeDisabled)
			}
		})
	}
}

func TestScopedUserProfileOmitsAccountAndSecurityFields(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(userProfileResponseFromModel(&model.User{
		ID: model.NewUserID(), Username: "student", DisplayName: "Student",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
		if _, exposed := fields[forbidden]; exposed {
			t.Fatalf("scoped field %q exposed: %s", forbidden, encoded)
		}
	}
}

func TestFullUserProfileRetainsFalseEmailVerification(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(userProfileResponseFromModel(&model.User{
		ID: model.NewUserID(), Username: "student", Email: "student@example.edu",
		DisplayName: "Student", EmailVerified: false, Locale: "fr", Timezone: "Europe/Paris",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["email_verified"]) != "false" {
		t.Fatalf("full unverified profile = %s, want explicit email_verified=false", encoded)
	}
}

func TestProfilePictureIfMatchRequiresOneCanonicalStrongETag(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	for _, test := range []struct {
		name     string
		header   string
		required bool
		want     string
		wantErr  bool
	}{
		{name: "strong", header: `"` + checksum + `"`, required: true, want: checksum},
		{name: "optional omitted"},
		{name: "required omitted", required: true, wantErr: true},
		{name: "weak", header: `W/"` + checksum + `"`, required: true, wantErr: true},
		{name: "wildcard", header: "*", required: true, wantErr: true},
		{name: "list", header: `"` + checksum + `", "` + checksum + `"`, required: true, wantErr: true},
		{name: "unquoted", header: checksum, required: true, wantErr: true},
		{name: "noncanonical", header: `"` + strings.Repeat("A", 64) + `"`, required: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := profilePictureIfMatch(test.header, test.required)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("profilePictureIfMatch() = %q, %v", got, err)
			}
		})
	}
}

func TestLoginResponseDoesNotExposeUserRevision(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
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
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		authenticationResource(application, browserCookies{}),
	)
	httpAPI.ServeHTTP(response, request)
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
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewId()
	accounts := &accountStateHTTPApplication{result: &model.User{ID: model.UserID(userID), Revision: 4, Username: "student", DisabledAt: model.OptionalTimeFromMillis(500)}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(accounts, &sessionAdministrationHTTPApplication{}))
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
