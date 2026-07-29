// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package api implements Proctor's versioned HTTP boundary.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Logger       *mlog.Logger
	Health       Health
	Application  Application
	BuildInfo    BuildInfo
	MaxBodyBytes int64
}

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, *model.AppError)
}

type Authentication interface {
	Authenticator
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
}

type Users interface {
	GetUser(context.Context, string) (*model.User, *model.AppError)
}

type Sessions interface {
	GetSessions(context.Context, model.Principal) ([]*model.Session, *model.AppError)
	RevokeSession(context.Context, model.Principal, string) *model.AppError
	RevokeAllSessions(context.Context, model.Principal) *model.AppError
}

type Audits interface {
	ListAuditEvents(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.AuditQuery,
	) ([]*model.AuditEvent, *model.AppError)
}

type Bootstrap interface {
	GetInstallationStatus(context.Context) (*model.InstallationStatus, *model.AppError)
	BootstrapInstallation(
		context.Context,
		*model.Institution,
		*model.User,
		string,
		model.RequestMetadata,
		string,
	) (*model.InstallationBootstrapResult, *model.AppError)
}

type Roles interface {
	ListRoles(context.Context, model.Principal, model.RequestMetadata) ([]*model.Role, *model.AppError)
	GetRole(context.Context, model.Principal, model.RequestMetadata, string) (*model.Role, *model.AppError)
	CreateRole(context.Context, model.Principal, model.RequestMetadata, *model.Role) (*model.Role, *model.AppError)
	PatchRole(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		string,
		*model.RolePatch,
	) (*model.Role, *model.AppError)
	DeleteRole(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
}

type RoleBindings interface {
	ListRoleBindingsForUser(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		string,
	) ([]*model.RoleBinding, *model.AppError)
	ListRoleBindingsForScope(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.RoleScopeType,
		string,
	) ([]*model.RoleBinding, *model.AppError)
	CreateRoleBinding(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		*model.RoleBinding,
	) (*model.RoleBinding, *model.AppError)
	EndRoleBinding(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		string,
	) (*model.RoleBinding, *model.AppError)
}

// Application is the cohesive application-facing API contract. Its component
// interfaces keep domain ownership visible without turning authentication into
// an unrelated service locator.
type Application interface {
	Authentication
	Users
	Sessions
	Audits
	Bootstrap
	Roles
	RoleBindings
}

type API struct {
	handler     http.Handler
	dispatcher  *dispatcher
	application Application
	logger      *mlog.Logger
	health      Health
	buildInfo   BuildInfo
	routes      []Route
}

func New(options Options) (*API, error) {
	if options.Logger == nil {
		return nil, errors.New("logger is required")
	}
	if options.Health == nil {
		return nil, errors.New("health state is required")
	}
	if options.Application == nil {
		return nil, errors.New("application is required")
	}
	if options.MaxBodyBytes <= 0 {
		return nil, errors.New("maximum body size must be greater than zero")
	}

	api := &API{
		dispatcher:  &dispatcher{keys: make(map[string]struct{})},
		application: options.Application,
		logger:      options.Logger,
		health:      options.Health,
		buildInfo:   options.BuildInfo,
	}
	initializers := []func() error{
		api.InitSystem,
		api.InitAuthentication,
		api.InitUsers,
		api.InitSessions,
		api.InitAudits,
		api.InitBootstrap,
		api.InitRoles,
		api.InitRoleBindings,
	}
	for _, initialize := range initializers {
		if err := initialize(); err != nil {
			return nil, err
		}
	}
	sortRoutes(api.routes)
	api.handler = withMiddleware(api.dispatcher, options.Logger, options.MaxBodyBytes)
	return api, nil
}

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	a.handler.ServeHTTP(writer, request)
}

func (a *API) Routes() []Route {
	return append([]Route(nil), a.routes...)
}

type dispatcher struct {
	routes []registeredRoute
	keys   map[string]struct{}
}

type routeSegment struct {
	literal   string
	parameter string
}

type registeredRoute struct {
	route       Route
	handler     http.Handler
	segments    []routeSegment
	specificity int
}

func (a *API) Register(route Route, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("register %s %s: handler is nil", route.Method, route.Path)
	}
	switch route.Auth {
	case AuthPublic, AuthSessionRequired, AuthRefreshCredentialRequired, AuthPrivileged:
	default:
		return fmt.Errorf("register %s %s: authentication policy is invalid", route.Method, route.Path)
	}
	segments, canonical, err := compileRoutePath(route.Path)
	if err != nil {
		return fmt.Errorf("register %s %s: %w", route.Method, route.Path, err)
	}
	if route.Method == "" || strings.ToUpper(route.Method) != route.Method {
		return fmt.Errorf("register %s %s: HTTP method is invalid", route.Method, route.Path)
	}
	key := route.Method + " " + canonical
	if _, exists := a.dispatcher.keys[key]; exists {
		return fmt.Errorf("register %s %s: duplicate route", route.Method, route.Path)
	}
	a.dispatcher.keys[key] = struct{}{}
	a.dispatcher.routes = append(a.dispatcher.routes, registeredRoute{
		route: route,
		handler: requireAuthentication(
			handler, route.Auth, a.application, a.logger,
		),
		segments:    segments,
		specificity: routeSpecificity(segments),
	})
	a.routes = append(a.routes, route)
	return nil
}

func (d *dispatcher) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	type match struct {
		route  registeredRoute
		values map[string]string
	}
	matches := make([]match, 0, 2)
	maxSpecificity := -1
	for _, route := range d.routes {
		values, matchesPath := matchRoutePath(route.segments, request.URL.Path)
		if !matchesPath {
			continue
		}
		if route.specificity > maxSpecificity {
			matches = matches[:0]
			maxSpecificity = route.specificity
		}
		if route.specificity == maxSpecificity {
			matches = append(matches, match{route: route, values: values})
		}
	}
	allowed := make(map[string]struct{})
	for _, matched := range matches {
		allowed[matched.route.route.Method] = struct{}{}
		if matched.route.route.Method != request.Method {
			continue
		}
		for name, value := range matched.values {
			request.SetPathValue(name, value)
		}
		matched.route.handler.ServeHTTP(writer, request)
		return
	}
	if len(allowed) == 0 {
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
	methods := make([]string, 0, len(allowed))
	for method := range allowed {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	WriteProblem(writer, Problem{
		Type:      "https://proctor.sudosylabs.com/problems/method-not-allowed",
		Title:     "Method not allowed",
		Status:    http.StatusMethodNotAllowed,
		Detail:    "The request method is not allowed for this resource.",
		Instance:  request.URL.Path,
		Code:      "method_not_allowed",
		RequestID: RequestID(request.Context()),
	})
}

func routeSpecificity(segments []routeSegment) int {
	specificity := 0
	for _, segment := range segments {
		if segment.parameter == "" {
			specificity++
		}
	}
	return specificity
}

func compileRoutePath(path string) ([]routeSegment, string, error) {
	if path == "" || path[0] != '/' || (len(path) > 1 && strings.HasSuffix(path, "/")) {
		return nil, "", errors.New("path must be an absolute canonical path")
	}
	rawSegments := splitPath(path)
	segments := make([]routeSegment, 0, len(rawSegments))
	canonical := make([]string, 0, len(rawSegments))
	names := make(map[string]struct{})
	for _, raw := range rawSegments {
		if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}")
			if name == "" || strings.ContainsAny(name, "{}") {
				return nil, "", errors.New("path parameter is invalid")
			}
			if _, exists := names[name]; exists {
				return nil, "", errors.New("path parameter is duplicated")
			}
			names[name] = struct{}{}
			segments = append(segments, routeSegment{parameter: name})
			canonical = append(canonical, "{}")
			continue
		}
		if raw == "" || strings.ContainsAny(raw, "{}") {
			return nil, "", errors.New("path segment is invalid")
		}
		segments = append(segments, routeSegment{literal: raw})
		canonical = append(canonical, raw)
	}
	return segments, "/" + strings.Join(canonical, "/"), nil
}

func matchRoutePath(segments []routeSegment, path string) (map[string]string, bool) {
	if path == "" || (len(path) > 1 && strings.HasSuffix(path, "/")) {
		return nil, false
	}
	values := make(map[string]string)
	requestSegments := splitPath(path)
	if len(requestSegments) != len(segments) {
		return nil, false
	}
	for index, segment := range segments {
		value := requestSegments[index]
		if segment.parameter != "" {
			if value == "" {
				return nil, false
			}
			values[segment.parameter] = value
			continue
		}
		if segment.literal != value {
			return nil, false
		}
	}
	return values, true
}

func splitPath(path string) []string {
	if path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
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
