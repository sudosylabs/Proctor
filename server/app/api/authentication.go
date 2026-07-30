// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/api4/user.go login/logout handlers
// and its APISessionRequired boundary. Proctor keeps explicit wrappers and a
// request principal without Mattermost's product-specific handler context.

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
	Tokens  *model.AuthenticationTokens `json:"tokens"`
}

func (a *API) InitAuthentication() error {
	if err := a.Register(
		a.BaseRoutes.Authentication,
		"/login",
		http.MethodPost,
		a.APIHandler(loginHandler(a.application, a.logger)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.Authentication,
		"/refresh",
		http.MethodPost,
		a.APIRefreshCredentialRequired(refreshHandler(a.application, a.logger)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.Authentication,
		"/logout",
		http.MethodPost,
		a.APISessionRequired(logoutHandler(a.application, a.logger)),
	)
}

func (a *API) InitUsers() error {
	return a.Register(
		a.BaseRoutes.CurrentUser,
		"",
		http.MethodGet,
		a.APISessionRequired(currentUserHandler(a.application, a.logger)),
	)
}

func loginHandler(application Authentication, logger *mlog.Logger) http.Handler {
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
		writeJSON(writer, http.StatusOK, authenticationResponse{
			User: user, Session: session, Tokens: tokens,
		})
	})
}

func refreshHandler(application Authentication, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		token, ok := credentialFromContext(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		session, tokens, appErr := application.RefreshSession(request.Context(), token)
		if appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writeJSON(writer, http.StatusOK, authenticationResponse{
			Session: session, Tokens: tokens,
		})
	})
}

func logoutHandler(application Authentication, logger *mlog.Logger) http.Handler {
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

func bearerCredential(request *http.Request) (string, *model.AppError) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", authenticationRequiredError()
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", authenticationRequiredError()
	}
	return parts[1], nil
}

func Principal(ctx context.Context) (model.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(model.Principal)
	return principal, ok && principal.IsValid()
}

func credentialFromContext(ctx context.Context) (string, bool) {
	credential, ok := ctx.Value(credentialContextKey{}).(string)
	return credential, ok && credential != ""
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
