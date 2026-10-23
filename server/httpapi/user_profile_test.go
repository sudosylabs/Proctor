// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type userProfileHTTPApplication struct {
	result             *model.User
	contextResult      *app.CurrentUserContextView
	contextCalls       int
	values             []*model.User
	searchQuery        app.SearchUsersQuery
	updateCommand      app.UpdateUserProfileCommand
	changeEmailCommand app.ChangeUserEmailCommand
	verifyEmailCommand app.VerifyUserEmailPrivilegedCommand
	uploadCommand      app.UploadProfilePictureCommand
	removeCommand      app.RemoveProfilePictureCommand
	pictureQuery       app.GetProfilePictureQuery
	pictureContent     *app.ProfilePictureContent
}

func (a *userProfileHTTPApplication) GetCurrentUserContext(context.Context, invocation.Invocation) (*app.CurrentUserContextView, error) {
	a.contextCalls++
	return a.contextResult, nil
}

func (a *userProfileHTTPApplication) ListCandidateExamActivity(context.Context, invocation.Invocation,
	attempt.CandidateActivityQuery,
) (attempt.CandidateActivityPage, error) {
	return attempt.CandidateActivityPage{}, nil
}

func (a *userProfileHTTPApplication) ChangeUserEmail(_ context.Context, _ invocation.Invocation, command app.ChangeUserEmailCommand) (*app.UserEmailState, error) {
	a.changeEmailCommand = command
	return &app.UserEmailState{UserID: a.result.ID, EmailVerified: a.result.EmailVerified}, nil
}

func (a *userProfileHTTPApplication) VerifyUserEmailPrivileged(_ context.Context, _ invocation.Invocation, command app.VerifyUserEmailPrivilegedCommand) (*app.UserEmailState, error) {
	a.verifyEmailCommand = command
	return &app.UserEmailState{UserID: a.result.ID, EmailVerified: a.result.EmailVerified}, nil
}

type accountStateHTTPApplication struct {
	AccountStateApplication
	result  *model.User
	command app.SetUserEnabledCommand
}

func (a *accountStateHTTPApplication) SetUserEnabled(_ context.Context, _ invocation.Invocation, command app.SetUserEnabledCommand) (*model.User, error) {
	a.command = command
	return a.result, nil
}

type sessionAdministrationHTTPApplication struct {
	SessionAdministrationApplication
	list             []*model.Session
	hasMore          bool
	listQuery        app.ListUserSessionsQuery
	revokeCommand    app.RevokeUserSessionCommand
	revokeAllCommand app.RevokeUserSessionsCommand
}

func (a *sessionAdministrationHTTPApplication) ListUserSessions(_ context.Context, _ invocation.Invocation, query app.ListUserSessionsQuery) (app.Page[*model.Session], error) {
	a.listQuery = query
	return app.Page[*model.Session]{Items: a.list, HasMore: a.hasMore}, nil
}

func (a *sessionAdministrationHTTPApplication) RevokeUserSession(_ context.Context, _ invocation.Invocation, command app.RevokeUserSessionCommand) error {
	a.revokeCommand = command
	return nil
}

func (a *sessionAdministrationHTTPApplication) RevokeUserSessions(_ context.Context, _ invocation.Invocation, command app.RevokeUserSessionsCommand) error {
	a.revokeAllCommand = command
	return nil
}

type roleHTTPApplication struct {
	RoleApplication
	list           []*model.Role
	result         *model.Role
	createCommand  app.CreateRoleCommand
	updateCommand  app.UpdateRoleCommand
	archiveCommand app.ArchiveRoleCommand
}

func (a *roleHTTPApplication) ListRoles(context.Context, invocation.Invocation, app.ListRolesQuery) ([]*model.Role, error) {
	return a.list, nil
}

func (a *roleHTTPApplication) GetRole(context.Context, invocation.Invocation, app.GetRoleQuery) (*model.Role, error) {
	return a.result, nil
}

func (a *roleHTTPApplication) CreateRole(_ context.Context, _ invocation.Invocation, command app.CreateRoleCommand) (*model.Role, error) {
	a.createCommand = command
	return a.result, nil
}

func (a *roleHTTPApplication) UpdateRole(_ context.Context, _ invocation.Invocation, command app.UpdateRoleCommand) (*model.Role, error) {
	a.updateCommand = command
	return a.result, nil
}

func (a *roleHTTPApplication) ArchiveRole(_ context.Context, _ invocation.Invocation, command app.ArchiveRoleCommand) error {
	a.archiveCommand = command
	return nil
}

type roleBindingHTTPApplication struct {
	RoleBindingApplication
	list   []*model.RoleBinding
	result *model.RoleBinding
}

func (a *roleBindingHTTPApplication) ListRoleBindings(context.Context, invocation.Invocation, app.ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	return a.list, nil
}

func (a *roleBindingHTTPApplication) CreateRoleBinding(context.Context, invocation.Invocation, app.CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return a.result, nil
}

func (a *roleBindingHTTPApplication) EndRoleBinding(context.Context, invocation.Invocation, app.EndRoleBindingCommand) (*model.RoleBinding, error) {
	return a.result, nil
}

type auditListingHTTPApplication struct {
	AuditListingApplication
	list []*model.AuditEvent
}

func (a *auditListingHTTPApplication) ListAuditEvents(context.Context, invocation.Invocation, app.ListAuditEventsQuery) (app.Page[*model.AuditEvent], error) {
	return app.Page[*model.AuditEvent]{Items: a.list}, nil
}

type bootstrapHTTPApplication struct {
	BootstrapApplication
	status *model.InstallationStatus
	result *model.InstallationBootstrapResult
}

func (a *bootstrapHTTPApplication) GetInstallationStatus(context.Context, invocation.Invocation, app.GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	if a.status != nil {
		return a.status, nil
	}
	return &model.InstallationStatus{Initialized: false}, nil
}

func (a *bootstrapHTTPApplication) BootstrapInstallation(context.Context, invocation.Invocation, app.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	return a.result, nil
}

type loginRevisionHTTPApplication struct {
	authenticationEntryApplication
	user *model.User
}

func (a *loginRevisionHTTPApplication) Login(
	context.Context,
	invocation.Invocation,
	app.LoginCommand,
) (*app.LoginResult, error) {
	return &app.LoginResult{
		User:    a.user,
		Session: &model.Session{ID: model.NewSessionID()},
		Tokens:  &model.AuthenticationTokens{AccessToken: "access"},
	}, nil
}

func (a *userProfileHTTPApplication) SearchUsers(_ context.Context, _ invocation.Invocation, query app.SearchUsersQuery) (app.Page[*model.User], error) {
	a.searchQuery = query
	return app.Page[*model.User]{Items: a.values}, nil
}
func (a *userProfileHTTPApplication) GetUserProfile(context.Context, invocation.Invocation, app.GetUserProfileQuery) (*model.User, error) {
	return a.result, nil
}
func (a *userProfileHTTPApplication) UpdateUserProfile(_ context.Context, _ invocation.Invocation, command app.UpdateUserProfileCommand) (*model.User, error) {
	a.updateCommand = command
	return a.result, nil
}
func (a *userProfileHTTPApplication) UploadProfilePicture(_ context.Context, _ invocation.Invocation, command app.UploadProfilePictureCommand) (*model.User, error) {
	a.uploadCommand = command
	return a.result, nil
}
func (a *userProfileHTTPApplication) RemoveProfilePicture(_ context.Context, _ invocation.Invocation, command app.RemoveProfilePictureCommand) (*model.User, error) {
	a.removeCommand = command
	return a.result, nil
}
func (a *userProfileHTTPApplication) GetProfilePicture(_ context.Context, _ invocation.Invocation, query app.GetProfilePictureQuery) (*app.ProfilePictureContent, error) {
	a.pictureQuery = query
	return a.pictureContent, nil
}

func TestCurrentUserContextHTTPReturnsClosedNoStoreDTO(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	userID := model.NewUserID()
	profiles := &userProfileHTTPApplication{contextResult: &app.CurrentUserContextView{
		UserID: userID, Username: "student", DisplayName: "Student",
		ProfilePictureReference: "/api/v1/users/" + userID.String() + "/profile-picture",
		AvailableProductAreas: []store.CurrentUserProductArea{
			store.CurrentUserProductAreaAccount, store.CurrentUserProductAreaSettings,
		},
		ManagementScopes: []store.CurrentUserManagementScope{{
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewAcademicUnitID().String(), DisplayName: "Engineering",
		}},
		SessionManagementAvailable: true,
	}}
	session := model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: session}, userProfileResource(profiles))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/context", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "context response = %d cache=%q calls=%d: %s", response.Code, response.Header().Get("Cache-Control"), profiles.contextCalls, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "context response = %d cache=%q calls=%d: %s", response.Code, response.Header().Get("Cache-Control"), profiles.contextCalls, response.Body.String())
	require.Equal(t, 1, profiles.contextCalls, "context response = %d cache=%q calls=%d: %s", response.Code, response.Header().Get("Cache-Control"), profiles.contextCalls, response.Body.String())
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	for _, required := range []string{"user", "no_current_affiliation", "no_assigned_access", "available_product_areas", "management_scopes", "management_scopes_has_more", "unresolved_attempt", "session_management_available", "current_desktop_registration_id"} {
		_, exists := body[required]
		require.True(t, exists, "context response omitted %q: %s", required, response.Body.String())
	}
	require.Equal(t, "null", string(body["unresolved_attempt"]))
	require.Equal(t, "null", string(body["current_desktop_registration_id"]))
}

func TestUserProfileHTTPUsesAllowlistedDTOAndRouteID(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewID()
	user := &model.User{ID: model.UserID(userID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 7, Username: "student", Email: "student@example.edu", DisplayName: "Student", Locale: "en", Timezone: "UTC", ProfilePictureChangedAt: model.OptionalTimeFromMillis(500)}
	profiles := &userProfileHTTPApplication{result: user}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+userID, strings.NewReader(`{"display_name":"Updated"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, model.UserID(userID), profiles.updateCommand.ID)
	require.NotNil(t, profiles.updateCommand.DisplayName)
	require.Equal(t, "Updated", *profiles.updateCommand.DisplayName)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	for _, forbidden := range []string{"revision", "password", "password_hash", "external_subject", "mfa_secret", "access_token", "refresh_token"} {
		_, exposed := body[forbidden]
		require.False(t, exposed, "sensitive field %q exposed: %#v", forbidden, body)
	}
	require.Equal(t, "/api/v1/users/"+userID+"/profile-picture?v=7", body["profile_picture_url"])
	newer := *user
	newer.Revision++
	require.NotEqual(t, userProfileResponseFromModel(&newer).ProfilePictureURL, userProfileResponseFromModel(user).ProfilePictureURL)
	uploadRequest := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+userID+"/profile-picture", strings.NewReader("image"))
	uploadRequest.Header.Set("Authorization", "Bearer credential")
	uploadRequest.Header.Set("Content-Type", "image/png")
	uploadRequest.Header.Set("If-Match", `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	uploadResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(uploadResponse, uploadRequest)
	require.Equal(t, http.StatusOK, uploadResponse.Code, "upload status = %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	uploaded, err := io.ReadAll(profiles.uploadCommand.Body)
	require.NoError(t, err)
	require.Equal(t, model.UserID(userID), profiles.uploadCommand.UserID, "upload command = %#v body", profiles.uploadCommand)
	require.Equal(t, int64(5), profiles.uploadCommand.Size, "upload command = %#v body", profiles.uploadCommand)
	require.Equal(t, strings.Repeat("a", 64), profiles.uploadCommand.ExpectedSHA256, "upload command = %#v body", profiles.uploadCommand)
	require.Equal(t, "image", string(uploaded), "upload command = %#v body", profiles.uploadCommand)
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID+"/profile-picture", nil)
	deleteRequest.Header.Set("Authorization", "Bearer credential")
	deleteRequest.Header.Set("If-Match", `"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`)
	deleteResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(deleteResponse, deleteRequest)
	require.Equal(t, http.StatusOK, deleteResponse.Code, "delete response/command = %d / %#v", deleteResponse.Code, profiles.removeCommand)
	require.Equal(t, model.UserID(userID), profiles.removeCommand.UserID, "delete response/command = %d / %#v", deleteResponse.Code, profiles.removeCommand)
	require.Equal(t, strings.Repeat("b", 64), profiles.removeCommand.ExpectedSHA256, "delete response/command = %d / %#v", deleteResponse.Code, profiles.removeCommand)
	profiles.pictureContent = &app.ProfilePictureContent{Body: io.NopCloser(strings.NewReader("webp")), MediaType: "image/webp", Size: 4, ETag: `"checksum"`}
	pictureRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/profile-picture?size=128", nil)
	pictureRequest.Header.Set("Authorization", "Bearer credential")
	pictureRequest.Header.Set("If-None-Match", `"different", W/"checksum"`)
	pictureResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(pictureResponse, pictureRequest)
	require.Equal(t, http.StatusNotModified, pictureResponse.Code, "profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
	require.Equal(t, model.UserID(userID), profiles.pictureQuery.UserID, "profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
	require.Equal(t, 128, profiles.pictureQuery.Size, "profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
	require.Equal(t, "private, max-age=86400", pictureResponse.Header().Get("Cache-Control"), "profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
	require.Equal(t, "4", pictureResponse.Header().Get("Content-Length"), "profile picture response = status %d headers %#v query %#v", pictureResponse.Code, pictureResponse.Header(), profiles.pictureQuery)
}

func TestGenericUserProfilePatchRejectsEmailSecurityFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	profiles := &userProfileHTTPApplication{result: &model.User{ID: model.NewUserID()}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userProfileResource(profiles))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/users/"+model.NewID(), strings.NewReader(`{"email":"redirect@example.edu","email_verified":true}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "security-field PATCH status=%d command=%#v body=%s", response.Code, profiles.updateCommand, response.Body.String())
	require.Empty(t, profiles.updateCommand.ID, "security-field PATCH status=%d command=%#v body=%s", response.Code, profiles.updateCommand, response.Body.String())
}

func TestEmailMutationResponsesUseNarrowAccountStateProjection(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	now := time.Now()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
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
			require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
			require.Equal(t, `"`+userID.String()+`"`, string(fields["id"]))
			require.Equal(t, fmt.Sprintf("%t", test.verified), string(fields["email_verified"]))
			for _, forbidden := range []string{"email", "username", "display_name", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at", "created_at", "updated_at", "archived_at"} {
				_, exposed := fields[forbidden]
				require.False(t, exposed, "email mutation exposed %q: %s", forbidden, response.Body.Bytes())
			}
		})
	}
}

func TestUserSearchTransportPreservesIncludeDisabledRequestForApplicationAuthorization(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
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
			require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
			require.Equal(t, test.includeDisabled, profiles.searchQuery.IncludeDisabled, "IncludeDisabled")
		})
	}
}

func TestScopedUserProfileOmitsAccountAndSecurityFields(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(userProfileResponseFromModel(&model.User{
		ID: model.NewUserID(), Username: "student", DisplayName: "Student",
	}))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	for _, forbidden := range []string{"email", "email_verified", "locale", "timezone", "last_login_at", "last_activity_at", "disabled_at"} {
		_, exposed := fields[forbidden]
		require.False(t, exposed, "scoped field %q exposed: %s", forbidden, encoded)
	}
}

func TestFullUserProfileRetainsFalseEmailVerification(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(userProfileResponseFromModel(&model.User{
		ID: model.NewUserID(), Username: "student", Email: "student@example.edu",
		DisplayName: "Student", EmailVerified: false, Locale: "fr", Timezone: "Europe/Paris",
	}))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.Equal(t, "false", string(fields["email_verified"]), "full unverified profile = %s, want explicit email_verified=false", encoded)
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
			require.Equal(t, test.wantErr, err != nil, "profilePictureIfMatch() = %q, %v", got, err)
			require.Equal(t, test.want, got, "profilePictureIfMatch() = %q, %v", got, err)
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
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	user, ok := body["user"].(map[string]any)
	require.True(t, ok, "user response = %#v", body["user"])
	_, exposed := user["revision"]
	require.False(t, exposed, "revision exposed by login: %#v", user)
}

func TestAccountDisableUsesApplicationCommandAndAllowlistedResponse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	userID := model.NewID()
	accounts := &accountStateHTTPApplication{result: &model.User{ID: model.UserID(userID), Revision: 4, Username: "student", DisabledAt: model.OptionalTimeFromMillis(500)}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(accounts, &sessionAdministrationHTTPApplication{}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+userID+"/disable", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, model.UserID(userID), accounts.command.ID)
	require.False(t, accounts.command.Enabled)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	_, exposed := body["revision"]
	require.False(t, exposed, "revision exposed: %#v", body)
}
