// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go login/logout handlers,
// its session-authenticated boundary, and browser session-cookie behavior.
// Proctor keeps explicit catalog classification and an immutable request
// principal without Mattermost's product-specific handler context.

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

type authenticationEntryApplication interface {
	Login(context.Context, application.Invocation, application.LoginCommand) (*application.LoginResult, error)
	RefreshSession(context.Context, application.Invocation, application.RefreshSessionCommand) (*model.Session, *model.AuthenticationTokens, error)
	Logout(context.Context, application.Invocation, application.LogoutCommand) error
	RequestEmailVerification(context.Context, application.Invocation, application.RequestEmailVerificationCommand) error
	CompleteEmailVerification(context.Context, application.Invocation, application.CompleteEmailVerificationCommand) (*model.User, error)
	RequestPasswordReset(context.Context, application.Invocation, application.RequestPasswordResetCommand) error
	CompletePasswordReset(context.Context, application.Invocation, application.CompletePasswordResetCommand) (*model.User, error)
}

type authenticationResourceModule struct {
	authentication authenticationEntryApplication
	cookies        browserCookies
}

func authenticationResource(authentication authenticationEntryApplication, cookies browserCookies) resource {
	module := authenticationResourceModule{authentication: authentication, cookies: cookies}
	return newResource(
		"authentication",
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("login")), authenticationLoginErrorCodes(), module.login),
		refreshCredentialRoute(http.MethodPost, apiPath(literal("auth"), literal("refresh")), authenticationRefreshErrorCodes(), module.refresh),
		sessionRoute(http.MethodPost, apiPath(literal("auth"), literal("logout")), sessionAuthenticationMutationErrorCodes("authentication.internal"), module.logout),
		sessionRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("email-verification"), literal("request")),
			sessionAuthenticationMutationErrorCodes(
				"authentication.rate_limited", "authentication.rate_limit_unavailable",
				"authentication.account_recovery.unavailable",
			),
			module.requestEmailVerification,
		),
		publicRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("email-verification"), literal("complete")),
			[]string{
				"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
				"authentication.account_token.invalid", "authentication.account_recovery.unavailable",
			},
			module.completeEmailVerification,
		),
		publicRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("password-reset"), literal("request")),
			[]string{
				"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
				"authentication.account_recovery.unavailable",
			},
			module.requestPasswordReset,
		),
		publicRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("password-reset"), literal("complete")),
			[]string{
				"request.invalid", "authentication.password.invalid", "authentication.rate_limited",
				"authentication.rate_limit_unavailable", "authentication.account_token.invalid",
				"authentication.account_recovery.unavailable",
			},
			module.completePasswordReset,
		),
	)
}

func authenticationLoginErrorCodes() []string {
	return []string{
		"request.invalid", "authentication.client_type.invalid", "authentication.password.invalid",
		"authentication.invalid_credentials", "authentication.mfa.required", "authentication.mfa.invalid_code",
		"authentication.mfa.unavailable",
		"authentication.sessions.maximum_reached", "authentication.rate_limited",
		"authentication.rate_limit_unavailable", "authentication.internal",
	}
}

func authenticationRefreshErrorCodes() []string {
	return []string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authentication.csrf.invalid", "authentication.session.invalid", "authentication.internal",
	}
}

func sessionAuthenticationMutationErrorCodes(extra ...string) []string {
	codes := []string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authentication.csrf.invalid",
	}
	return append(codes, extra...)
}

func (module authenticationResourceModule) login(request operationRequest) (operationResult, error) {
	var input loginRequest
	if err := request.decodeJSON(&input, "login"); err != nil {
		return operationResult{}, err
	}
	result, err := module.authentication.Login(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.LoginCommand{
			LoginID: input.LoginID, Password: input.Password, ClientType: input.ClientType,
			DeviceID: input.DeviceID, DeviceName: input.DeviceName, MFACode: input.MFACode,
			Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	response := authenticationResponseFromLogin(result)
	headers := http.Header{"Cache-Control": {"no-store"}}
	if usesBrowserCookieTransport(input.ClientType) {
		headers = combineResponseHeaders(headers, captureResponseHeaders(func(writer http.ResponseWriter) {
			if result != nil {
				module.cookies.attach(writer, result.Tokens)
			}
		}))
		response.Tokens = nil
	}
	return jsonResult(http.StatusOK, response).withHeaders(headers), nil
}

func (module authenticationResourceModule) refresh(request operationRequest) (operationResult, error) {
	credential, ok := credentialFromContext(request.context)
	if !ok {
		return operationResult{}, authenticationRequiredError()
	}
	session, tokens, err := module.authentication.RefreshSession(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.RefreshSessionCommand{RefreshToken: credential.token},
	)
	if err != nil {
		if credential.source == credentialSourceCookie {
			return operationResult{}, errorWithHeaders(err, captureResponseHeaders(module.cookies.clear))
		}
		return operationResult{}, err
	}
	response := authenticationResponseFromRefresh(session, tokens)
	headers := http.Header{"Cache-Control": {"no-store"}}
	if credential.source == credentialSourceCookie {
		response.Tokens = nil
		headers = combineResponseHeaders(headers, captureResponseHeaders(func(writer http.ResponseWriter) {
			module.cookies.attach(writer, tokens)
		}))
	}
	return jsonResult(http.StatusOK, response).withHeaders(headers), nil
}

func (module authenticationResourceModule) logout(request operationRequest) (operationResult, error) {
	if err := module.authentication.Logout(request.context, request.invocation(), application.LogoutCommand{}); err != nil {
		return operationResult{}, err
	}
	result := noContentResult().withHeaders(http.Header{"Cache-Control": {"no-store"}})
	if credentialSourceFromContext(request.context) == credentialSourceCookie {
		result = result.withHeaders(combineResponseHeaders(result.headers, captureResponseHeaders(module.cookies.clear)))
	}
	return result, nil
}

func (module authenticationResourceModule) requestEmailVerification(request operationRequest) (operationResult, error) {
	err := module.authentication.RequestEmailVerification(
		request.context, request.invocation(),
		application.RequestEmailVerificationCommand{Source: request.request.RemoteAddr},
	)
	if err != nil {
		return operationResult{}, err
	}
	return statusResult(http.StatusAccepted), nil
}

func (module authenticationResourceModule) completeEmailVerification(request operationRequest) (operationResult, error) {
	var input emailVerificationCompletion
	if err := request.decodeJSON(&input, "complete_email_verification"); err != nil {
		return operationResult{}, err
	}
	_, err := module.authentication.CompleteEmailVerification(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.CompleteEmailVerificationCommand{Token: input.Token, Source: request.request.RemoteAddr},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (module authenticationResourceModule) requestPasswordReset(request operationRequest) (operationResult, error) {
	var input passwordResetRequest
	if err := request.decodeJSON(&input, "request_password_reset"); err != nil {
		return operationResult{}, err
	}
	if err := module.authentication.RequestPasswordReset(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.RequestPasswordResetCommand{Email: input.Email, Source: request.request.RemoteAddr},
	); err != nil {
		return operationResult{}, err
	}
	return statusResult(http.StatusAccepted), nil
}

func (module authenticationResourceModule) completePasswordReset(request operationRequest) (operationResult, error) {
	var input passwordResetCompletion
	if err := request.decodeJSON(&input, "complete_password_reset"); err != nil {
		return operationResult{}, err
	}
	_, err := module.authentication.CompletePasswordReset(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.CompletePasswordResetCommand{
			Token: input.Token, Password: input.Password, Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(captureResponseHeaders(module.cookies.clear)), nil
}

func usesBrowserCookieTransport(clientType model.SessionClientType) bool {
	return clientType == model.SessionClientDesktop ||
		clientType == model.SessionClientWeb
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
