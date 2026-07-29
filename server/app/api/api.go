// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package api implements Proctor's versioned HTTP boundary.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

type Health interface {
	Live() bool
	Ready() bool
}

type AuthRequirement string

const (
	AuthPublic                    AuthRequirement = "public"
	AuthSessionRequired           AuthRequirement = "session_required"
	AuthRefreshCredentialRequired AuthRequirement = "refresh_credential_required"
	AuthPrivileged                AuthRequirement = "privileged"
)

type Route struct {
	Method string
	Path   string
	Auth   AuthRequirement
}

type Options struct {
	Logger         *mlog.Logger
	Health         Health
	Authentication Authentication
	BuildInfo      BuildInfo
	MaxBodyBytes   int64
}

type Authentication interface {
	Login(
		context.Context,
		string,
		string,
		model.SessionClientType,
		string,
		string,
		string,
	) (*model.User, *model.Session, *model.AuthenticationTokens, *model.AppError)
	AuthenticateAccess(context.Context, string) (*model.Principal, *model.AppError)
	RefreshSession(
		context.Context,
		string,
	) (*model.Session, *model.AuthenticationTokens, *model.AppError)
	Logout(context.Context, model.Principal) *model.AppError
	GetUser(context.Context, string) (*model.User, *model.AppError)
	GetSessions(context.Context, model.Principal) ([]*model.Session, *model.AppError)
	RevokeSession(context.Context, model.Principal, string) *model.AppError
	RevokeAllSessions(context.Context, model.Principal) *model.AppError
	ListAuditEvents(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.AuditQuery,
	) ([]*model.AuditEvent, *model.AppError)
}

type API struct {
	handler http.Handler
	routes  []Route
}

func New(options Options) (*API, error) {
	if options.Logger == nil {
		return nil, errors.New("logger is required")
	}
	if options.Health == nil {
		return nil, errors.New("health state is required")
	}
	if options.Authentication == nil {
		return nil, errors.New("authentication application is required")
	}
	if options.MaxBodyBytes <= 0 {
		return nil, errors.New("maximum body size must be greater than zero")
	}

	dispatcher := &dispatcher{byPath: make(map[string]map[string]http.Handler)}
	registrations := []struct {
		route   Route
		handler http.Handler
	}{
		{
			route: Route{Method: http.MethodGet, Path: "/health/live", Auth: AuthPublic},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if !options.Health.Live() {
					WriteProblem(writer, Problem{
						Type:      "https://proctor.sudosylabs.com/problems/not-live",
						Title:     "Service unavailable",
						Status:    http.StatusServiceUnavailable,
						Detail:    "The process is not healthy.",
						Instance:  request.URL.Path,
						Code:      "not_live",
						RequestID: RequestID(request.Context()),
					})
					return
				}
				writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
			}),
		},
		{
			route: Route{Method: http.MethodGet, Path: "/health/ready", Auth: AuthPublic},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if !options.Health.Ready() {
					WriteProblem(writer, Problem{
						Type:      "https://proctor.sudosylabs.com/problems/not-ready",
						Title:     "Service unavailable",
						Status:    http.StatusServiceUnavailable,
						Detail:    "The service is not ready to accept requests.",
						Instance:  request.URL.Path,
						Code:      "not_ready",
						RequestID: RequestID(request.Context()),
					})
					return
				}
				writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
			}),
		},
		{
			route: Route{Method: http.MethodGet, Path: "/api/v1/system/version", Auth: AuthPublic},
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeJSON(writer, http.StatusOK, options.BuildInfo)
			}),
		},
		{
			route:   Route{Method: http.MethodPost, Path: "/api/v1/auth/login", Auth: AuthPublic},
			handler: loginHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodPost,
				Path:   "/api/v1/auth/refresh",
				Auth:   AuthRefreshCredentialRequired,
			},
			handler: refreshHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodPost,
				Path:   "/api/v1/auth/logout",
				Auth:   AuthSessionRequired,
			},
			handler: logoutHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodGet,
				Path:   "/api/v1/users/me",
				Auth:   AuthSessionRequired,
			},
			handler: currentUserHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodGet,
				Path:   "/api/v1/users/me/sessions",
				Auth:   AuthSessionRequired,
			},
			handler: getSessionsHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodPost,
				Path:   "/api/v1/users/me/sessions/revoke",
				Auth:   AuthSessionRequired,
			},
			handler: revokeSessionHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodPost,
				Path:   "/api/v1/users/me/sessions/revoke-all",
				Auth:   AuthSessionRequired,
			},
			handler: revokeAllSessionsHandler(options.Authentication, options.Logger),
		},
		{
			route: Route{
				Method: http.MethodGet,
				Path:   "/api/v1/audits",
				Auth:   AuthPrivileged,
			},
			handler: listAuditEventsHandler(options.Authentication, options.Logger),
		},
	}

	routes := make([]Route, 0, len(registrations))
	for _, registration := range registrations {
		dispatcher.handle(
			registration.route,
			requireAuthentication(
				registration.handler,
				registration.route.Auth,
				options.Authentication,
				options.Logger,
			),
		)
		routes = append(routes, registration.route)
	}
	sortRoutes(routes)
	return &API{
		handler: withMiddleware(dispatcher, options.Logger, options.MaxBodyBytes),
		routes:  routes,
	}, nil
}

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	a.handler.ServeHTTP(writer, request)
}

func (a *API) Routes() []Route {
	return append([]Route(nil), a.routes...)
}

type dispatcher struct {
	byPath map[string]map[string]http.Handler
}

func (d *dispatcher) handle(route Route, handler http.Handler) {
	if d.byPath[route.Path] == nil {
		d.byPath[route.Path] = make(map[string]http.Handler)
	}
	d.byPath[route.Path][route.Method] = handler
}

func (d *dispatcher) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	methods, exists := d.byPath[request.URL.Path]
	if !exists {
		WriteProblem(writer, Problem{
			Type:      "https://proctor.sudosylabs.com/problems/not-found",
			Title:     "Resource not found",
			Status:    http.StatusNotFound,
			Detail:    "The requested resource was not found.",
			Instance:  request.URL.Path,
			Code:      "not_found",
			RequestID: RequestID(request.Context()),
		})
		return
	}
	handler, exists := methods[request.Method]
	if !exists {
		allowed := make([]string, 0, len(methods))
		for method := range methods {
			allowed = append(allowed, method)
		}
		sort.Strings(allowed)
		writer.Header().Set("Allow", strings.Join(allowed, ", "))
		WriteProblem(writer, Problem{
			Type:      "https://proctor.sudosylabs.com/problems/method-not-allowed",
			Title:     "Method not allowed",
			Status:    http.StatusMethodNotAllowed,
			Detail:    "The request method is not allowed for this resource.",
			Instance:  request.URL.Path,
			Code:      "method_not_allowed",
			RequestID: RequestID(request.Context()),
		})
		return
	}
	handler.ServeHTTP(writer, request)
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(left, right int) bool {
		if routes[left].Path == routes[right].Path {
			return routes[left].Method < routes[right].Method
		}
		return routes[left].Path < routes[right].Path
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
