// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/handlers.go and
// server/channels/web/handlers.go. Proctor applies authentication requirements
// from its sealed route catalog while using an immutable principal and the
// standard net/http request context.

package httpapi

import (
	"context"
	application "github.com/sudosylabs/proctor/server/app"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) newHandlerWithErrorPolicy(
	handler http.Handler,
	requirement AuthRequirement,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return withRequestParams(requireAuthentication(
		handler,
		requirement,
		a.authenticator,
		a.logger,
		a.cookies,
		a.recentAuthenticationTTL,
		errorPolicy,
	))
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
