// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/handlers.go and
// server/channels/web/handlers.go. Proctor keeps typed route wrappers that make
// authentication requirements visible at registration while using its
// immutable principal and standard net/http request context.

package api

import (
	"context"
	"net/http"

	"github.com/sudosylabs/proctor/server/mlog"
)

// Handler is a fully classified API endpoint. It is intentionally constructed
// only through APIHandler, APISessionRequired, or another explicit API wrapper
// so an endpoint cannot be registered without an authentication contract.
type Handler struct {
	handler        http.Handler
	authentication AuthRequirement
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.handler.ServeHTTP(writer, request)
}

// APIHandler classifies an endpoint as public.
func (a *API) APIHandler(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthPublic)
}

// APISessionRequired classifies an endpoint as requiring an authenticated,
// revocable server-side session.
func (a *API) APISessionRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthSessionRequired)
}

// APIRefreshCredentialRequired classifies an endpoint as accepting only a
// refresh credential. The credential is made available to the refresh use case
// but is never resolved as an ordinary request principal.
func (a *API) APIRefreshCredentialRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthRefreshCredentialRequired)
}

func (a *API) newHandler(
	handler http.Handler,
	requirement AuthRequirement,
) *Handler {
	if handler == nil {
		return &Handler{authentication: requirement}
	}
	return &Handler{
		handler: withRequestParams(requireAuthentication(
			handler,
			requirement,
			a.application,
			a.logger,
			a.cookies,
		)),
		authentication: requirement,
	}
}

func requireAuthentication(
	next http.Handler,
	requirement AuthRequirement,
	application Authenticator,
	logger *mlog.Logger,
	cookies browserCookies,
) http.Handler {
	switch requirement {
	case AuthPublic:
		return next
	case AuthRefreshCredentialRequired:
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			credential, appErr := requestRefreshCredential(request)
			if appErr != nil {
				WriteError(writer, request, appErr)
				return
			}
			if credential.source == credentialSourceCookie {
				if appErr := cookies.verifyCSRF(request); appErr != nil {
					WriteError(writer, request, appErr)
					return
				}
			}
			ctx := context.WithValue(request.Context(), credentialContextKey{}, credential)
			ctx = context.WithValue(ctx, credentialSourceContextKey{}, credential.source)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	case AuthSessionRequired:
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			credential, appErr := requestAccessCredential(request)
			if appErr != nil {
				WriteError(writer, request, appErr)
				return
			}
			if credential.source == credentialSourceCookie &&
				requiresCSRF(request.Method) {
				if appErr := cookies.verifyCSRF(request); appErr != nil {
					WriteError(writer, request, appErr)
					return
				}
			}
			principal, appErr := application.AuthenticateAccess(
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
			ctx := context.WithValue(request.Context(), principalContextKey{}, *principal)
			ctx = context.WithValue(ctx, credentialSourceContextKey{}, credential.source)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	default:
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			logger.ErrorContext(
				request.Context(),
				"route has unsupported authentication requirement",
				mlog.String("requirement", string(requirement)),
			)
			WriteProblem(writer, internalProblem(request))
		})
	}
}

func requiresCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
