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
	application "github.com/sudosylabs/proctor/server/app"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
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

// APIPrincipalRequired accepts either a revocable interactive session or a
// personal access token. The latter remains subject to its scope and optional
// academic-unit ceiling during application authorization.
func (a *API) APIPrincipalRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthPrincipalRequired)
}

// APISessionRequired classifies an endpoint as requiring an authenticated,
// revocable server-side session.
func (a *API) APISessionRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthSessionRequired)
}

// APIStrongSessionRequired requires a session whose current authentication
// strength records a trusted multi-factor authentication event.
func (a *API) APIStrongSessionRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthStrongSessionRequired)
}

// APIRecentSessionRequired requires a session whose most recent login or
// stronger authentication event falls within the configured reauthentication
// window.
func (a *API) APIRecentSessionRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthRecentSessionRequired)
}

// APIStrongRecentSessionRequired composes both assurance requirements.
func (a *API) APIStrongRecentSessionRequired(handler http.Handler) *Handler {
	return a.newHandler(handler, AuthStrongRecentSessionRequired)
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
	return a.newHandlerWithErrorPolicy(handler, requirement, nil)
}

func (a *API) newHandlerWithErrorPolicy(
	handler http.Handler,
	requirement AuthRequirement,
	errorPolicy routeErrorPolicy,
) *Handler {
	if handler == nil {
		return &Handler{authentication: requirement}
	}
	authenticator := a.authenticator
	if authenticator == nil {
		authenticator = a.application
	}
	return &Handler{
		handler: withRequestParams(requireAuthentication(
			handler,
			requirement,
			authenticator,
			a.logger,
			a.cookies,
			a.recentAuthenticationTTL,
			errorPolicy,
		)),
		authentication: requirement,
	}
}

func requireAuthentication(
	next http.Handler,
	requirement AuthRequirement,
	application Authenticator,
	logger Logger,
	cookies browserCookies,
	recentAuthenticationTTL time.Duration,
	errorPolicy routeErrorPolicy,
) http.Handler {
	switch requirement {
	case AuthPublic:
		return next
	case AuthRefreshCredentialRequired:
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			credential, appErr := requestRefreshCredential(request)
			if appErr != nil {
				writeRouteApplicationError(writer, request, logger, errorPolicy, appErr)
				return
			}
			if credential.source == credentialSourceCookie {
				if appErr := cookies.verifyCSRF(request); appErr != nil {
					writeRouteApplicationError(writer, request, logger, errorPolicy, appErr)
					return
				}
			}
			ctx := context.WithValue(request.Context(), credentialContextKey{}, credential)
			ctx = context.WithValue(ctx, credentialSourceContextKey{}, credential.source)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	case AuthPrincipalRequired,
		AuthSessionRequired,
		AuthStrongSessionRequired,
		AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired:
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			credential, appErr := requestAccessCredential(request)
			if appErr != nil {
				writeRouteApplicationError(writer, request, logger, errorPolicy, appErr)
				return
			}
			if credential.source == credentialSourceCookie &&
				requiresCSRF(request.Method) {
				if appErr := cookies.verifyCSRF(request); appErr != nil {
					writeRouteApplicationError(writer, request, logger, errorPolicy, appErr)
					return
				}
			}
			var principal *model.Principal
			var authErr error
			if requirement == AuthPrincipalRequired &&
				credential.source == credentialSourceBearer {
				principal, authErr = application.AuthenticateBearer(
					request.Context(),
					credential.token,
				)
			} else {
				principal, authErr = application.AuthenticateAccess(
					request.Context(),
					credential.token,
				)
			}
			if authErr != nil {
				if credential.source == credentialSourceCookie {
					cookies.clear(writer)
				}
				writeRouteApplicationError(writer, request, logger, errorPolicy, authErr)
				return
			}
			if appErr := requirePrincipalAssurance(
				*principal,
				requirement,
				time.Now(),
				recentAuthenticationTTL,
			); appErr != nil {
				writeRouteApplicationError(writer, request, logger, errorPolicy, appErr)
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
				logString("requirement", string(requirement)),
			)
			WriteProblem(writer, internalProblem(request))
		})
	}
}

func requirePrincipalAssurance(
	principal model.Principal,
	requirement AuthRequirement,
	now time.Time,
	recentAuthenticationTTL time.Duration,
) error {
	strongRequired := requirement == AuthStrongSessionRequired ||
		requirement == AuthStrongRecentSessionRequired
	recentRequired := requirement == AuthRecentSessionRequired ||
		requirement == AuthStrongRecentSessionRequired
	if strongRequired && !principal.HasStrongAuthentication() {
		return application.NewError("authentication.strong_required")
	}
	if recentRequired &&
		!principal.IsRecentlyAuthenticated(now, recentAuthenticationTTL) {
		return application.NewError("authentication.reauthentication_required")
	}
	return nil
}

func requiresCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
