// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/user.go login/logout handlers,
// its session-authenticated boundary, and browser session-cookie behavior.
// Proctor keeps explicit catalog classification and an immutable request
// principal without Mattermost's product-specific handler context.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type principalContextKey struct{}
type credentialContextKey struct{}
type credentialSourceContextKey struct{}

type credentialSource string

const (
	credentialSourceBearer credentialSource = "bearer"
	credentialSourceCookie credentialSource = "cookie"
	credentialSourceDPoP   credentialSource = "dpop"
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

type publicRegistrationRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Password  string `json:"password"`
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
	ID                      string `json:"id"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
	ArchivedAt              string `json:"archived_at,omitempty"`
	UserID                  string `json:"user_id"`
	MFARecoveryRequired     bool   `json:"mfa_recovery_required"`
	ClientType              string `json:"client_type"`
	DesktopRegistrationID   string `json:"desktop_registration_id,omitempty"`
	DesktopRelease          string `json:"desktop_release,omitempty"`
	DesktopBuildID          string `json:"desktop_build_id,omitempty"`
	DesktopPlatform         string `json:"desktop_platform,omitempty"`
	DesktopArchitecture     string `json:"desktop_architecture,omitempty"`
	DesktopRealtimeProtocol int    `json:"desktop_realtime_protocol,omitempty"`
	DeviceID                string `json:"device_id,omitempty"`
	DeviceName              string `json:"device_name,omitempty"`
	AuthenticationMethod    string `json:"authentication_method"`
	AuthenticationStrength  string `json:"authentication_strength"`
	AuthenticatedAt         string `json:"authenticated_at"`
	ReauthenticatedAt       string `json:"reauthenticated_at,omitempty"`
	MFACompletedAt          string `json:"mfa_completed_at,omitempty"`
	LastActivityAt          string `json:"last_activity_at"`
	IdleExpiresAt           string `json:"idle_expires_at"`
	ExpiresAt               string `json:"expires_at"`
	RevokedAt               string `json:"revoked_at,omitempty"`
	RevocationReasonCode    string `json:"revocation_reason_code,omitempty"`
	RevocationReason        string `json:"revocation_reason,omitempty"`
}

type authenticationTokensResponse struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  string `json:"access_expires_at"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
}

func sessionResponseFromModel(request *http.Request, session *model.Session) *sessionResponse {
	if session == nil {
		return nil
	}
	return &sessionResponse{
		ID:                    session.ID.String(),
		CreatedAt:             model.FormatRFC3339(session.CreatedAt),
		UpdatedAt:             model.FormatRFC3339(session.UpdatedAt),
		ArchivedAt:            session.ArchivedAt.FormatRFC3339(),
		UserID:                session.UserID.String(),
		MFARecoveryRequired:   session.MFARecoveryRequired,
		ClientType:            string(session.ClientType),
		DesktopRegistrationID: session.DesktopRegistrationID.String(),
		DesktopRelease:        session.DesktopRelease, DesktopBuildID: session.DesktopBuildID,
		DesktopPlatform: string(session.DesktopPlatform), DesktopArchitecture: string(session.DesktopArchitecture),
		DesktopRealtimeProtocol: session.DesktopRealtimeProtocol,
		DeviceID:                session.DeviceID,
		DeviceName:              session.DeviceName,
		AuthenticationMethod:    session.AuthenticationMethod,
		AuthenticationStrength:  string(session.AuthenticationStrength),
		AuthenticatedAt:         model.FormatRFC3339(session.AuthenticatedAt),
		ReauthenticatedAt:       session.ReauthenticatedAt.FormatRFC3339(),
		MFACompletedAt:          session.MFACompletedAt.FormatRFC3339(),
		LastActivityAt:          model.FormatRFC3339(session.LastActivityAt),
		IdleExpiresAt:           model.FormatRFC3339(session.IdleExpiresAt),
		ExpiresAt:               model.FormatRFC3339(session.ExpiresAt),
		RevokedAt:               session.RevokedAt.FormatRFC3339(),
		RevocationReasonCode:    string(session.RevocationReason),
		RevocationReason:        localizedSessionRevocationReason(request, session.RevocationReason),
	}
}

func localizedSessionRevocationReason(request *http.Request, reason model.SessionRevocationReason) string {
	if !reason.IsValid() {
		return ""
	}
	fallback := sessionRevocationReasonPresentation(reason)
	return translatedRequestText(request, sessionRevocationLocalizationID(reason), fallback)
}

func sessionRevocationReasonPresentation(reason model.SessionRevocationReason) string {
	switch reason {
	case model.SessionRevocationUserLogout:
		return "Signed out by the user."
	case model.SessionRevocationUserSession:
		return "Revoked by the user."
	case model.SessionRevocationUserAllSessions:
		return "All sessions were revoked by the user."
	case model.SessionRevocationAdministratorSession:
		return "Revoked by an administrator."
	case model.SessionRevocationAdministratorAllSessions:
		return "All sessions were revoked by an administrator."
	case model.SessionRevocationAccountDisabled:
		return "The account was disabled."
	case model.SessionRevocationPasswordRemoved:
		return "Password authentication was removed."
	case model.SessionRevocationExternalIdentityUnlinked:
		return "The external identity was unlinked."
	case model.SessionRevocationMFAReset:
		return "Multi-factor authentication was reset."
	case model.SessionRevocationPasswordReset:
		return "The password was reset."
	case model.SessionRevocationRefreshReplay:
		return "Refresh credential replay was detected."
	case model.SessionRevocationInactiveUser:
		return "The account is inactive."
	case model.SessionRevocationAuthenticationAuditFailed:
		return "Authentication audit completion failed."
	case model.SessionRevocationAccessPolicyChanged:
		return "The authentication access policy changed."
	case model.SessionRevocationDesktopRegistration:
		return "The Desktop Registration was revoked."
	case model.SessionRevocationAttemptSessionLock:
		return "Signed out when an exam attempt became active."
	default:
		return ""
	}
}

func authenticationTokensResponseFromModel(tokens *model.AuthenticationTokens) *authenticationTokensResponse {
	if tokens == nil {
		return nil
	}
	return &authenticationTokensResponse{
		TokenType:        tokens.TokenType,
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		AccessExpiresAt:  model.FormatRFC3339(tokens.AccessExpiresAt),
		RefreshExpiresAt: model.FormatRFC3339(tokens.RefreshExpiresAt),
	}
}

func authenticationResponseFromLogin(request *http.Request, result *app.LoginResult) authenticationResponse {
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
		Session: sessionResponseFromModel(request, result.Session),
		Tokens:  authenticationTokensResponseFromModel(result.Tokens),
	}
}

func authenticationResponseFromRefresh(
	request *http.Request,
	session *model.Session,
	tokens *model.AuthenticationTokens,
) authenticationResponse {
	return authenticationResponse{
		Session: sessionResponseFromModel(request, session),
		Tokens:  authenticationTokensResponseFromModel(tokens),
	}
}

type authenticationEntryApplication interface {
	RegisterLocalUser(context.Context, app.Invocation, app.RegisterLocalUserCommand) error
	Login(context.Context, app.Invocation, app.LoginCommand) (*app.LoginResult, error)
	RefreshSession(context.Context, app.Invocation, app.RefreshSessionCommand) (*model.Session, *model.AuthenticationTokens, error)
	Logout(context.Context, app.Invocation, app.LogoutCommand) error
	RequestEmailVerification(context.Context, app.Invocation, app.RequestEmailVerificationCommand) error
	CompleteEmailVerification(context.Context, app.Invocation, app.CompleteEmailVerificationCommand) (*model.User, error)
	RequestPasswordReset(context.Context, app.Invocation, app.RequestPasswordResetCommand) error
	CompletePasswordReset(context.Context, app.Invocation, app.CompletePasswordResetCommand) (*model.User, error)
}

type authenticationResourceModule struct {
	authentication authenticationEntryApplication
	cookies        browserCookies
}

func authenticationResource(authentication authenticationEntryApplication, cookies browserCookies) resource {
	module := authenticationResourceModule{authentication: authentication, cookies: cookies}
	return newResource(
		"authentication",
		publicRoute(
			http.MethodPost, apiPath(literal("auth"), literal("register")),
			[]string{
				"request.invalid", "authentication.password.invalid", "service.busy", "authentication.registration.invalid",
				"authentication.registration.invitation_required", "authentication.registration.unavailable",
				"authentication.rate_limited", "authentication.rate_limit_unavailable",
			},
			module.register,
		),
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("login")), authenticationLoginErrorCodes(), module.login),
		refreshCredentialRoute(http.MethodPost, apiPath(literal("auth"), literal("refresh")), authenticationRefreshErrorCodes(), module.refresh),
		mfaRecoverySessionRoute(http.MethodPost, apiPath(literal("auth"), literal("logout")), sessionAuthenticationMutationErrorCodes("authentication.internal"), module.logout),
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
				"request.invalid", "authentication.password.invalid", "service.busy", "authentication.rate_limited",
				"authentication.rate_limit_unavailable", "authentication.account_token.invalid",
				"authentication.account_recovery.unavailable",
			},
			module.completePasswordReset,
		),
	)
}

func (m authenticationResourceModule) register(request operationRequest) (operationResult, error) {
	var input publicRegistrationRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	if err := m.authentication.RegisterLocalUser(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		app.RegisterLocalUserCommand{
			Username: input.Username, Email: input.Email, FirstName: input.FirstName,
			LastName: input.LastName, Password: input.Password,
			Source: request.metadata.IPAddress,
		},
	); err != nil {
		return operationResult{}, err
	}
	return statusResult(http.StatusAccepted).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func authenticationLoginErrorCodes() []string {
	return []string{
		"request.invalid", "authentication.client_type.invalid", "authentication.password.invalid", "service.busy",
		"authentication.credentials_invalid", "authentication.mfa.required", "authentication.mfa.code_invalid",
		"authentication.mfa.unavailable",
		"authentication.sessions.maximum_reached", "authentication.desktop_authorization.account_session_locked", "authentication.rate_limited",
		"authentication.rate_limit_unavailable", "authentication.internal", "audit.unavailable",
	}
}

func authenticationRefreshErrorCodes() []string {
	return []string{
		"authentication.token_invalid", "authentication.session.invalid", "authentication.internal",
		"authentication.dpop.invalid", "authentication.dpop.replayed", "authentication.dpop.use_nonce",
		"authentication.dpop.unavailable",
	}
}

func sessionAuthenticationMutationErrorCodes(extra ...string) []string {
	return append([]string(nil), extra...)
}

func (m authenticationResourceModule) login(request operationRequest) (operationResult, error) {
	var input loginRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	result, err := m.authentication.Login(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		app.LoginCommand{
			LoginID: input.LoginID, Password: input.Password, ClientType: input.ClientType,
			DeviceID: input.DeviceID, DeviceName: input.DeviceName, MFACode: input.MFACode,
			Source: request.metadata.IPAddress,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	response := authenticationResponseFromLogin(request.request, result)
	headers := http.Header{"Cache-Control": {"no-store"}}
	if usesBrowserCookieTransport(input.ClientType) {
		headers = combineResponseHeaders(headers, captureResponseHeaders(func(writer http.ResponseWriter) {
			if result != nil {
				m.cookies.attach(writer, result.Tokens)
			}
		}))
		response.Tokens = nil
	}
	return jsonResult(http.StatusOK, response).withHeaders(headers), nil
}

func (m authenticationResourceModule) refresh(request operationRequest) (operationResult, error) {
	credential, ok := credentialFromContext(request.context)
	if !ok {
		return operationResult{}, authenticationRequiredError()
	}
	command := app.RefreshSessionCommand{RefreshToken: credential.token}
	if credential.source == credentialSourceDPoP {
		proof, proofErr := requestDPoPProof(request.request)
		if proofErr != nil {
			return operationResult{}, errorWithHeaders(proofErr, dpopErrorHeaders(proofErr))
		}
		command.DPoP = &app.DPoPRequestProof{
			Proof: proof, Method: request.request.Method, Path: request.request.URL.EscapedPath(),
		}
	}
	session, tokens, err := m.authentication.RefreshSession(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		command,
	)
	if err != nil {
		headers := http.Header{}
		if command.DPoP != nil {
			headers = dpopErrorHeaders(err)
		}
		if credential.source == credentialSourceCookie {
			headers = combineResponseHeaders(headers, captureResponseHeaders(m.cookies.clear))
		}
		return operationResult{}, errorWithHeaders(err, headers)
	}
	response := authenticationResponseFromRefresh(request.request, session, tokens)
	headers := http.Header{"Cache-Control": {"no-store"}}
	if credential.source == credentialSourceCookie {
		response.Tokens = nil
		headers = combineResponseHeaders(headers, captureResponseHeaders(func(writer http.ResponseWriter) {
			m.cookies.attach(writer, tokens)
		}))
	}
	return jsonResult(http.StatusOK, response).withHeaders(headers), nil
}

func dpopErrorHeaders(err error) http.Header {
	headers := http.Header{"WWW-Authenticate": {`DPoP error="invalid_dpop_proof"`}}
	if nonce, ok := app.DPoPChallengeNonce(err); ok {
		headers.Set("DPoP-Nonce", nonce)
		headers.Set("WWW-Authenticate", `DPoP error="use_dpop_nonce"`)
	}
	return headers
}

func (m authenticationResourceModule) logout(request operationRequest) (operationResult, error) {
	if err := m.authentication.Logout(request.context, request.invocation(), app.LogoutCommand{}); err != nil {
		return operationResult{}, err
	}
	result := noContentResult().withHeaders(http.Header{"Cache-Control": {"no-store"}})
	if credentialSourceFromContext(request.context) == credentialSourceCookie {
		result = result.withHeaders(combineResponseHeaders(result.headers, captureResponseHeaders(m.cookies.clear)))
	}
	return result, nil
}

func (m authenticationResourceModule) requestEmailVerification(request operationRequest) (operationResult, error) {
	err := m.authentication.RequestEmailVerification(
		request.context, request.invocation(),
		app.RequestEmailVerificationCommand{Source: request.metadata.IPAddress},
	)
	if err != nil {
		return operationResult{}, err
	}
	return statusResult(http.StatusAccepted), nil
}

func (m authenticationResourceModule) completeEmailVerification(request operationRequest) (operationResult, error) {
	var input emailVerificationCompletion
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	_, err := m.authentication.CompleteEmailVerification(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		app.CompleteEmailVerificationCommand{Token: input.Token, Source: request.metadata.IPAddress},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m authenticationResourceModule) requestPasswordReset(request operationRequest) (operationResult, error) {
	var input passwordResetRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	if err := m.authentication.RequestPasswordReset(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		app.RequestPasswordResetCommand{Email: input.Email, Source: request.metadata.IPAddress},
	); err != nil {
		return operationResult{}, err
	}
	return statusResult(http.StatusAccepted), nil
}

func (m authenticationResourceModule) completePasswordReset(request operationRequest) (operationResult, error) {
	var input passwordResetCompletion
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	_, err := m.authentication.CompletePasswordReset(
		request.context,
		app.NewInvocation(model.Principal{}, request.metadata),
		app.CompletePasswordResetCommand{
			Token: input.Token, Password: input.Password, Source: request.metadata.IPAddress,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(captureResponseHeaders(m.cookies.clear)), nil
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
	token, source, hasAuthorization, appErr := optionalAuthorizationCredential(request)
	if appErr != nil {
		return requestCredential{}, appErr
	}
	cookie, appErr := singleCookieValue(request, cookieName)
	if appErr != nil {
		return requestCredential{}, appErr
	}
	if hasAuthorization && cookie != "" {
		return requestCredential{}, ambiguousCredentialError()
	}
	if hasAuthorization {
		return requestCredential{token: token, source: source}, nil
	}
	if cookie != "" {
		return requestCredential{token: cookie, source: credentialSourceCookie}, nil
	}
	return requestCredential{}, authenticationRequiredError()
}

func optionalBearerCredential(request *http.Request) (string, bool, error) {
	token, source, ok, err := optionalAuthorizationCredential(request)
	if err != nil || !ok {
		return token, ok, err
	}
	if source != credentialSourceBearer {
		return "", false, authenticationRequiredError()
	}
	return token, true, nil
}

func optionalAuthorizationCredential(request *http.Request) (string, credentialSource, bool, error) {
	values := request.Header.Values("Authorization")
	if len(values) == 0 {
		return "", "", false, nil
	}
	if len(values) != 1 {
		return "", "", false, authenticationRequiredError()
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false, authenticationRequiredError()
	}
	switch {
	case strings.EqualFold(parts[0], "Bearer"):
		return parts[1], credentialSourceBearer, true, nil
	case parts[0] == "DPoP":
		return parts[1], credentialSourceDPoP, true, nil
	default:
		return "", "", false, authenticationRequiredError()
	}
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
	return principal, ok && principal.ValidateMFARecovery() == nil
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
	if err := requireJSONMediaType(request); err != nil {
		return err
	}
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

func invalidRequestError(err error) error {
	out := apperror.New("request.invalid")
	if err != nil {
		out = out.Wrap(err)
	}
	return out
}

func authenticationRequiredError() error {
	return apperror.New("authentication.required")
}

func ambiguousCredentialError() error {
	return apperror.New("authentication.credential_ambiguous").WithField("field", "credential")
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
	return serverInternalCode
}

func applicationErrorRequiresLogging(err error) bool {
	var failure applicationFailure
	if errors.As(err, &failure) {
		// Capacity refusals are expected overload outcomes counted by the owning
		// work pool. Logging each refusal would amplify a request burst.
		if failure.Code() == "service.busy" {
			return false
		}
		mapping, ok := applicationErrorMappings[failure.Code()]
		return !ok || mapping.status >= http.StatusInternalServerError
	}
	return true
}
