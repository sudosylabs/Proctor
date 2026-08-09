// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go login/logout handlers,
// its APISessionRequired boundary, and browser session-cookie behavior.
// Proctor keeps explicit wrappers and an immutable request principal without
// Mattermost's product-specific handler context.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type principalContextKey struct{}
type credentialContextKey struct{}
type credentialSourceContextKey struct{}

type credentialSource string

const (
	credentialSourceBearer credentialSource = "bearer"
	credentialSourceCookie credentialSource = "cookie"
)

type requestCredential struct {
	token  string
	source credentialSource
}

type loginRequest struct {
	LoginID    string                  `json:"login_id"`
	Password   string                  `json:"password"`
	ClientType model.SessionClientType `json:"client_type"`
	DeviceID   string                  `json:"device_id,omitempty"`
	DeviceName string                  `json:"device_name,omitempty"`
	MFACode    string                  `json:"mfa_code,omitempty"`
}

type passwordResetRequest struct {
	Email string `json:"email"`
}

type passwordResetCompletion struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type emailVerificationCompletion struct {
	Token string `json:"token"`
}

// authenticationResponse is the transport-owned login/refresh success body.
// Field names match the historical v1 envelope so cookie and bearer clients
// keep working while domain models no longer serialize directly.
type authenticationResponse struct {
	User    *userProfileResponse          `json:"user,omitempty"`
	Session *sessionResponse              `json:"session"`
	Tokens  *authenticationTokensResponse `json:"tokens,omitempty"`
}

type sessionResponse struct {
	ID                     string `json:"id"`
	CreateAt               int64  `json:"create_at"`
	UpdateAt               int64  `json:"update_at"`
	DeleteAt               int64  `json:"delete_at"`
	UserID                 string `json:"user_id"`
	ClientType             string `json:"client_type"`
	DeviceID               string `json:"device_id,omitempty"`
	DeviceName             string `json:"device_name,omitempty"`
	AuthenticationMethod   string `json:"authentication_method"`
	AuthenticationStrength string `json:"authentication_strength"`
	AuthenticatedAt        int64  `json:"authenticated_at"`
	MFACompletedAt         int64  `json:"mfa_completed_at,omitempty"`
	LastActivityAt         int64  `json:"last_activity_at"`
	IdleExpiresAt          int64  `json:"idle_expires_at"`
	ExpiresAt              int64  `json:"expires_at"`
	RevokedAt              int64  `json:"revoked_at,omitempty"`
	RevocationReason       string `json:"revocation_reason,omitempty"`
}

type authenticationTokensResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

func sessionResponseFromModel(session *model.Session) *sessionResponse {
	if session == nil {
		return nil
	}
	return &sessionResponse{
		ID:                     session.ID.String(),
		CreateAt:               model.MillisFromTime(session.CreatedAt),
		UpdateAt:               model.MillisFromTime(session.UpdatedAt),
		DeleteAt:               session.ArchivedAt.Millis(),
		UserID:                 session.UserID.String(),
		ClientType:             string(session.ClientType),
		DeviceID:               session.DeviceID,
		DeviceName:             session.DeviceName,
		AuthenticationMethod:   session.AuthenticationMethod,
		AuthenticationStrength: string(session.AuthenticationStrength),
		AuthenticatedAt:        model.MillisFromTime(session.AuthenticatedAt),
		MFACompletedAt:         session.MFACompletedAt.Millis(),
		LastActivityAt:         model.MillisFromTime(session.LastActivityAt),
		IdleExpiresAt:          model.MillisFromTime(session.IdleExpiresAt),
		ExpiresAt:              model.MillisFromTime(session.ExpiresAt),
		RevokedAt:              session.RevokedAt.Millis(),
		RevocationReason:       session.RevocationReason,
	}
}

func authenticationTokensResponseFromModel(tokens *model.AuthenticationTokens) *authenticationTokensResponse {
	if tokens == nil {
		return nil
	}
	return &authenticationTokensResponse{
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		AccessExpiresAt:  model.MillisFromTime(tokens.AccessExpiresAt),
		RefreshExpiresAt: model.MillisFromTime(tokens.RefreshExpiresAt),
	}
}

func authenticationResponseFromLogin(result *application.LoginResult) authenticationResponse {
	if result == nil {
		return authenticationResponse{}
	}
	var user *userProfileResponse
	if result.User != nil {
		mapped := userProfileResponseFromModel(result.User)
		user = &mapped
	}
	return authenticationResponse{
		User:    user,
		Session: sessionResponseFromModel(result.Session),
		Tokens:  authenticationTokensResponseFromModel(result.Tokens),
	}
}

func authenticationResponseFromRefresh(
	session *model.Session,
	tokens *model.AuthenticationTokens,
) authenticationResponse {
	return authenticationResponse{
		Session: sessionResponseFromModel(session),
		Tokens:  authenticationTokensResponseFromModel(tokens),
	}
}

func (a *API) InitAuthentication() error {
	routes := []struct {
		path    string
		handler *Handler
	}{
		{"/login", a.APIHandler(loginHandler(a.application, a.logger, a.cookies))},
		{
			"/refresh",
			a.APIRefreshCredentialRequired(
				refreshHandler(a.application, a.logger, a.cookies),
			),
		},
		{
			"/logout",
			a.APISessionRequired(
				logoutHandler(a.application, a.logger, a.cookies),
			),
		},
		{
			"/email-verification/request",
			a.APISessionRequired(http.HandlerFunc(a.requestEmailVerification)),
		},
		{
			"/email-verification/complete",
			a.APIHandler(http.HandlerFunc(a.completeEmailVerification)),
		},
		{
			"/password-reset/request",
			a.APIHandler(http.HandlerFunc(a.requestPasswordReset)),
		},
		{
			"/password-reset/complete",
			a.APIHandler(http.HandlerFunc(a.completePasswordReset)),
		},
	}
	for _, route := range routes {
		if err := a.Register(
			a.BaseRoutes.Authentication,
			route.path,
			http.MethodPost,
			route.handler,
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) InitUsers() error {
	if err := a.Register(
		a.BaseRoutes.CurrentUser,
		"",
		http.MethodGet,
		a.APIPrincipalRequired(currentUserHandler(a.userProfiles, a.logger)),
	); err != nil {
		return err
	}
	return a.initUserAdministration()
}

func loginHandler(
	auth Authentication,
	logger Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input loginRequest
		if err := decodeRequestJSON(request, &input); err != nil {
			WriteError(writer, request, invalidRequestError("login", err))
			return
		}
		result, err := auth.Login(
			request.Context(),
			application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
			application.LoginCommand{
				LoginID:    input.LoginID,
				Password:   input.Password,
				ClientType: input.ClientType,
				DeviceID:   input.DeviceID,
				DeviceName: input.DeviceName,
				MFACode:    input.MFACode,
				Source:     request.RemoteAddr,
			},
		)
		if err != nil {
			writeApplicationError(writer, request, logger, err)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		response := authenticationResponseFromLogin(result)
		if usesBrowserCookieTransport(input.ClientType) {
			if result != nil {
				cookies.attach(writer, result.Tokens)
			}
			response.Tokens = nil
		}
		writeJSON(writer, http.StatusOK, response)
	})
}

func usesBrowserCookieTransport(clientType model.SessionClientType) bool {
	return clientType == model.SessionClientDesktop ||
		clientType == model.SessionClientWeb
}

func (a *API) requestEmailVerification(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	if err := a.application.RequestEmailVerification(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.RequestEmailVerificationCommand{Source: request.RemoteAddr},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.WriteHeader(http.StatusAccepted)
}

func (a *API) completeEmailVerification(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input emailVerificationCompletion
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("complete_email_verification", err))
		return
	}
	if _, err := a.application.CompleteEmailVerification(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.CompleteEmailVerificationCommand{Token: input.Token, Source: request.RemoteAddr},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) requestPasswordReset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input passwordResetRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("request_password_reset", err))
		return
	}
	if err := a.application.RequestPasswordReset(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.RequestPasswordResetCommand{Email: input.Email, Source: request.RemoteAddr},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.WriteHeader(http.StatusAccepted)
}

func (a *API) completePasswordReset(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input passwordResetCompletion
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("complete_password_reset", err))
		return
	}
	if _, err := a.application.CompletePasswordReset(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.CompletePasswordResetCommand{
			Token: input.Token, Password: input.Password, Source: request.RemoteAddr,
		},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	a.cookies.clear(writer)
	writer.WriteHeader(http.StatusNoContent)
}

func refreshHandler(
	auth Authentication,
	logger Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		credential, ok := credentialFromContext(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		session, tokens, err := auth.RefreshSession(
			request.Context(),
			application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
			application.RefreshSessionCommand{RefreshToken: credential.token},
		)
		if err != nil {
			if credential.source == credentialSourceCookie {
				cookies.clear(writer)
			}
			writeApplicationError(writer, request, logger, err)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		response := authenticationResponseFromRefresh(session, tokens)
		if credential.source == credentialSourceCookie {
			cookies.attach(writer, tokens)
			response.Tokens = nil
		}
		writeJSON(writer, http.StatusOK, response)
	})
}

func logoutHandler(
	auth Authentication,
	logger Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		if err := auth.Logout(
			request.Context(),
			application.NewInvocation(principal, RequestMetadata(request.Context())),
			application.LogoutCommand{},
		); err != nil {
			writeApplicationError(writer, request, logger, err)
			return
		}
		if credentialSourceFromContext(request.Context()) == credentialSourceCookie {
			cookies.clear(writer)
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	})
}

func currentUserHandler(profiles UserProfileApplication, logger Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		user, err := profiles.GetUserProfile(request.Context(), application.NewInvocation(principal, RequestMetadata(request.Context())), application.GetUserProfileQuery{ID: principal.UserID.String()})
		if err != nil {
			writeApplicationError(writer, request, logger, err)
			return
		}
		writeJSON(writer, http.StatusOK, userProfileResponseFromModel(user))
	})
}

func requestAccessCredential(request *http.Request) (requestCredential, error) {
	return requestCredentialFrom(request, BrowserAccessCookieName)
}

func requestRefreshCredential(request *http.Request) (requestCredential, error) {
	return requestCredentialFrom(request, BrowserRefreshCookieName)
}

func requestCredentialFrom(
	request *http.Request,
	cookieName string,
) (requestCredential, error) {
	bearer, hasBearer, appErr := optionalBearerCredential(request)
	if appErr != nil {
		return requestCredential{}, appErr
	}
	cookie, appErr := singleCookieValue(request, cookieName)
	if appErr != nil {
		return requestCredential{}, appErr
	}
	if hasBearer && cookie != "" {
		return requestCredential{}, ambiguousCredentialError()
	}
	if hasBearer {
		return requestCredential{token: bearer, source: credentialSourceBearer}, nil
	}
	if cookie != "" {
		return requestCredential{token: cookie, source: credentialSourceCookie}, nil
	}
	return requestCredential{}, authenticationRequiredError()
}

func optionalBearerCredential(request *http.Request) (string, bool, error) {
	values := request.Header.Values("Authorization")
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, authenticationRequiredError()
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false, authenticationRequiredError()
	}
	return parts[1], true, nil
}

func singleCookieValue(
	request *http.Request,
	name string,
) (string, error) {
	var value string
	for _, cookie := range request.Cookies() {
		if cookie.Name != name {
			continue
		}
		if value != "" || cookie.Value == "" {
			return "", ambiguousCredentialError()
		}
		value = cookie.Value
	}
	return value, nil
}

func Principal(ctx context.Context) (model.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(model.Principal)
	return principal, ok && principal.Validate() == nil
}

func credentialFromContext(ctx context.Context) (requestCredential, bool) {
	credential, ok := ctx.Value(credentialContextKey{}).(requestCredential)
	return credential, ok && credential.token != "" && credential.source != ""
}

func credentialSourceFromContext(ctx context.Context) credentialSource {
	source, _ := ctx.Value(credentialSourceContextKey{}).(credentialSource)
	return source
}

func decodeRequestJSON(request *http.Request, target any) error {
	if request.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func invalidRequestError(where string, err error) error {
	_ = where
	out := application.NewError("request.invalid")
	if err != nil {
		out = out.Wrap(err)
	}
	return out
}

func authenticationRequiredError() error {
	return application.NewError("authentication.required")
}

func ambiguousCredentialError() error {
	return application.NewError("authentication.credential_ambiguous").WithField("field", "credential")
}

func writeApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	logger Logger,
	appErr error,
) {
	if applicationErrorRequiresLogging(appErr) {
		logger.ErrorContext(
			request.Context(),
			"application request failed",
			logString("request_id", RequestID(request.Context())),
			logString("error_id", applicationErrorCode(appErr)),
			logErr(appErr),
		)
	}
	WriteError(writer, request, appErr)
}

func applicationErrorCode(err error) string {
	var failure applicationFailure
	if errors.As(err, &failure) {
		return failure.Code()
	}
	return "internal"
}

func applicationErrorRequiresLogging(err error) bool {
	var failure applicationFailure
	if errors.As(err, &failure) {
		mapping, ok := applicationErrorMappings[failure.Code()]
		return !ok || mapping.status >= http.StatusInternalServerError
	}
	return true
}
