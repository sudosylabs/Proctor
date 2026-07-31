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

	"github.com/sudosylabs/proctor/server/mlog"
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
}

type authenticationResponse struct {
	User    *model.User                 `json:"user,omitempty"`
	Session *model.Session              `json:"session"`
	Tokens  *model.AuthenticationTokens `json:"tokens,omitempty"`
}

func (a *API) InitAuthentication() error {
	if err := a.Register(
		a.BaseRoutes.Authentication,
		"/login",
		http.MethodPost,
		a.APIHandler(loginHandler(a.application, a.logger, a.cookies)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.Authentication,
		"/refresh",
		http.MethodPost,
		a.APIRefreshCredentialRequired(refreshHandler(a.application, a.logger, a.cookies)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.Authentication,
		"/logout",
		http.MethodPost,
		a.APISessionRequired(logoutHandler(a.application, a.logger, a.cookies)),
	)
}

func (a *API) InitUsers() error {
	if err := a.Register(
		a.BaseRoutes.CurrentUser,
		"",
		http.MethodGet,
		a.APISessionRequired(currentUserHandler(a.application, a.logger)),
	); err != nil {
		return err
	}
	return a.initUserAdministration()
}

func loginHandler(
	application Authentication,
	logger *mlog.Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input loginRequest
		if err := decodeRequestJSON(request, &input); err != nil {
			WriteError(writer, request, invalidRequestError("login", err))
			return
		}
		user, session, tokens, appErr := application.Login(
			request.Context(),
			input.LoginID,
			input.Password,
			input.ClientType,
			input.DeviceID,
			input.DeviceName,
			request.RemoteAddr,
		)
		if appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		if usesBrowserCookieTransport(input.ClientType) {
			cookies.attach(writer, tokens)
			tokens = nil
		}
		writeJSON(writer, http.StatusOK, authenticationResponse{
			User: user, Session: session, Tokens: tokens,
		})
	})
}

func usesBrowserCookieTransport(clientType model.SessionClientType) bool {
	return clientType == model.SessionClientDesktop ||
		clientType == model.SessionClientWeb
}

func refreshHandler(
	application Authentication,
	logger *mlog.Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		credential, ok := credentialFromContext(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		session, tokens, appErr := application.RefreshSession(
			request.Context(),
			credential.token,
		)
		if appErr != nil {
			if credential.source == credentialSourceCookie {
				cookies.clear(writer)
			}
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		if credential.source == credentialSourceCookie {
			cookies.attach(writer, tokens)
			tokens = nil
		}
		writeJSON(writer, http.StatusOK, authenticationResponse{
			Session: session, Tokens: tokens,
		})
	})
}

func logoutHandler(
	application Authentication,
	logger *mlog.Logger,
	cookies browserCookies,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		if appErr := application.Logout(request.Context(), principal); appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		if credentialSourceFromContext(request.Context()) == credentialSourceCookie {
			cookies.clear(writer)
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	})
}

func currentUserHandler(application Users, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		user, appErr := application.GetUserForPrincipal(
			request.Context(),
			principal,
			RequestMetadata(request.Context()),
			principal.UserId,
		)
		if appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writeJSON(writer, http.StatusOK, user)
	})
}

func requestAccessCredential(request *http.Request) (requestCredential, *model.AppError) {
	return requestCredentialFrom(request, BrowserAccessCookieName)
}

func requestRefreshCredential(request *http.Request) (requestCredential, *model.AppError) {
	return requestCredentialFrom(request, BrowserRefreshCookieName)
}

func requestCredentialFrom(
	request *http.Request,
	cookieName string,
) (requestCredential, *model.AppError) {
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

func optionalBearerCredential(request *http.Request) (string, bool, *model.AppError) {
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
) (string, *model.AppError) {
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
	return principal, ok && principal.IsValid()
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

func invalidRequestError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"request.invalid",
		nil,
		"",
		http.StatusBadRequest,
	).Wrap(err)
}

func authenticationRequiredError() *model.AppError {
	return model.NewAppError(
		"requireAuthentication",
		"authentication.required",
		nil,
		"",
		http.StatusUnauthorized,
	)
}

func ambiguousCredentialError() *model.AppError {
	return model.NewAppError(
		"requestCredentialFrom",
		"authentication.credential_ambiguous",
		nil,
		"",
		http.StatusBadRequest,
	)
}

func writeApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	logger *mlog.Logger,
	appErr *model.AppError,
) {
	if appErr.HTTPStatus() >= http.StatusInternalServerError {
		logger.ErrorContext(
			request.Context(),
			"application request failed",
			mlog.String("request_id", RequestID(request.Context())),
			mlog.String("error_id", appErr.ErrorCode()),
			mlog.Err(appErr),
		)
	}
	WriteError(writer, request, appErr)
}
