// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/api.go. Proctor retains the
// versioned BaseRoutes tree and regex-constrained resource subrouters while
// applying its own typed authentication wrappers and Problem Details
// boundary.

// Package api implements Proctor's versioned HTTP boundary.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
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
)

type Route struct {
	Method string
	Path   string
	Auth   AuthRequirement
}

type routeMatcher struct {
	route      Route
	pathRegexp *regexp.Regexp
}

// Routes owns the stable HTTP resource tree. Versioned resources are always
// derived from APIRoot so changing model.APIURLSuffix moves the complete API
// without editing individual handlers.
type Routes struct {
	Root    *mux.Router
	APIRoot *mux.Router

	Health             *mux.Router
	System             *mux.Router
	Authentication     *mux.Router
	Users              *mux.Router
	CurrentUser        *mux.Router
	Audits             *mux.Router
	Bootstrap          *mux.Router
	Roles              *mux.Router
	Role               *mux.Router
	RoleBindings       *mux.Router
	RoleBinding        *mux.Router
	Institution        *mux.Router
	AcademicUnits      *mux.Router
	AcademicUnit       *mux.Router
	Programmes         *mux.Router
	Programme          *mux.Router
	ProgrammeLevels    *mux.Router
	ProgrammeLevel     *mux.Router
	AcademicPeriods    *mux.Router
	AcademicPeriod     *mux.Router
	Classes            *mux.Router
	Class              *mux.Router
	User               *mux.Router
	Affiliation        *mux.Router
	AcademicUnitMember *mux.Router
	ClassMember        *mux.Router
}

type Options struct {
	Logger       *mlog.Logger
	Health       Health
	Application  Application
	BuildInfo    BuildInfo
	PublicURL    string
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
	GetUserForPrincipal(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		string,
	) (*model.User, *model.AppError)
	ListUsers(context.Context, model.Principal, model.RequestMetadata, store.UserListOptions) ([]*model.User, *model.AppError)
	PatchUser(context.Context, model.Principal, model.RequestMetadata, string, *model.UserPatch) (*model.User, *model.AppError)
	SetUserDisabled(context.Context, model.Principal, model.RequestMetadata, string, bool) (*model.User, *model.AppError)
	RevokeUserSessions(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
}

type AcademicAdministration interface {
	GetInstitution(context.Context, model.Principal, model.RequestMetadata) (*model.Institution, *model.AppError)
	PatchInstitution(context.Context, model.Principal, model.RequestMetadata, *model.InstitutionPatch) (*model.Institution, *model.AppError)
	GetAcademicUnit(context.Context, model.Principal, model.RequestMetadata, string) (*model.AcademicUnit, *model.AppError)
	ListAcademicUnits(context.Context, model.Principal, model.RequestMetadata, string) ([]*model.AcademicUnit, *model.AppError)
	SearchAcademicUnits(context.Context, model.Principal, model.RequestMetadata, string, int) ([]*model.AcademicUnit, *model.AppError)
	CreateAcademicUnit(context.Context, model.Principal, model.RequestMetadata, *model.AcademicUnit) (*model.AcademicUnit, *model.AppError)
	PatchAcademicUnit(context.Context, model.Principal, model.RequestMetadata, string, *model.AcademicUnitPatch) (*model.AcademicUnit, *model.AppError)
	ArchiveAcademicUnit(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
	GetProgramme(context.Context, model.Principal, model.RequestMetadata, string) (*model.Programme, *model.AppError)
	ListProgrammes(context.Context, model.Principal, model.RequestMetadata, string, string, int) ([]*model.Programme, *model.AppError)
	CreateProgramme(context.Context, model.Principal, model.RequestMetadata, *model.Programme) (*model.Programme, *model.AppError)
	PatchProgramme(context.Context, model.Principal, model.RequestMetadata, string, *model.ProgrammePatch) (*model.Programme, *model.AppError)
	ArchiveProgramme(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
	GetProgrammeLevel(context.Context, model.Principal, model.RequestMetadata, string) (*model.ProgrammeLevel, *model.AppError)
	ListProgrammeLevels(context.Context, model.Principal, model.RequestMetadata, string, string, int) ([]*model.ProgrammeLevel, *model.AppError)
	CreateProgrammeLevel(context.Context, model.Principal, model.RequestMetadata, *model.ProgrammeLevel) (*model.ProgrammeLevel, *model.AppError)
	PatchProgrammeLevel(context.Context, model.Principal, model.RequestMetadata, string, *model.ProgrammeLevelPatch) (*model.ProgrammeLevel, *model.AppError)
	ArchiveProgrammeLevel(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
	GetAcademicPeriod(context.Context, model.Principal, model.RequestMetadata, string) (*model.AcademicPeriod, *model.AppError)
	ListAcademicPeriods(context.Context, model.Principal, model.RequestMetadata, string, int) ([]*model.AcademicPeriod, *model.AppError)
	CreateAcademicPeriod(context.Context, model.Principal, model.RequestMetadata, *model.AcademicPeriod) (*model.AcademicPeriod, *model.AppError)
	PatchAcademicPeriod(context.Context, model.Principal, model.RequestMetadata, string, *model.AcademicPeriodPatch) (*model.AcademicPeriod, *model.AppError)
	ArchiveAcademicPeriod(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
	GetClass(context.Context, model.Principal, model.RequestMetadata, string) (*model.Class, *model.AppError)
	ListClasses(context.Context, model.Principal, model.RequestMetadata, string) ([]*model.Class, *model.AppError)
	SearchClasses(context.Context, model.Principal, model.RequestMetadata, string, string, int) ([]*model.Class, *model.AppError)
	CreateClass(context.Context, model.Principal, model.RequestMetadata, *model.Class) (*model.Class, *model.AppError)
	PatchClass(context.Context, model.Principal, model.RequestMetadata, string, *model.ClassPatch) (*model.Class, *model.AppError)
	ArchiveClass(context.Context, model.Principal, model.RequestMetadata, string) *model.AppError
}

type MembershipAdministration interface {
	ListAffiliations(context.Context, model.Principal, model.RequestMetadata, string) ([]*model.Affiliation, *model.AppError)
	CreateAffiliation(context.Context, model.Principal, model.RequestMetadata, *model.Affiliation) (*model.Affiliation, *model.AppError)
	EndAffiliation(context.Context, model.Principal, model.RequestMetadata, string) (*model.Affiliation, *model.AppError)
	ListAcademicUnitMembers(context.Context, model.Principal, model.RequestMetadata, string, int64) ([]*model.AcademicUnitMember, *model.AppError)
	CreateAcademicUnitMember(context.Context, model.Principal, model.RequestMetadata, *model.AcademicUnitMember) (*model.AcademicUnitMember, *model.AppError)
	EndAcademicUnitMember(context.Context, model.Principal, model.RequestMetadata, string) (*model.AcademicUnitMember, *model.AppError)
	ListClassMembers(context.Context, model.Principal, model.RequestMetadata, string, int64) ([]*model.ClassMember, *model.AppError)
	EnrollClassMember(context.Context, model.Principal, model.RequestMetadata, *model.ClassMember) (*model.ClassEnrollment, *model.AppError)
	EndClassMember(context.Context, model.Principal, model.RequestMetadata, string) (*model.ClassMember, *model.AppError)
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
	PermissionChecker
	Users
	Sessions
	Audits
	Bootstrap
	Roles
	RoleBindings
	AcademicAdministration
	MembershipAdministration
}

type API struct {
	handler       http.Handler
	router        *mux.Router
	BaseRoutes    *Routes
	application   Application
	logger        *mlog.Logger
	health        Health
	buildInfo     BuildInfo
	cookies       browserCookies
	routes        []Route
	routeMatchers []routeMatcher
	routeKeys     map[string]struct{}
	prefixes      map[*mux.Router]string
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
	cookies, err := newBrowserCookies(options.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("configure browser cookies: %w", err)
	}

	api := &API{
		application: options.Application,
		logger:      options.Logger,
		health:      options.Health,
		buildInfo:   options.BuildInfo,
		cookies:     cookies,
		routeKeys:   make(map[string]struct{}),
		prefixes:    make(map[*mux.Router]string),
	}
	api.initializeBaseRoutes(model.APIURLSuffix)
	initializers := []func() error{
		api.InitSystem,
		api.InitAuthentication,
		api.InitUsers,
		api.InitSessions,
		api.InitAudits,
		api.InitBootstrap,
		api.InitRoles,
		api.InitRoleBindings,
		api.InitInstitution,
		api.InitAcademicUnits,
		api.InitProgrammes,
		api.InitProgrammeLevels,
		api.InitAcademicPeriods,
		api.InitClasses,
		api.InitMemberships,
	}
	for _, initialize := range initializers {
		if err := initialize(); err != nil {
			return nil, err
		}
	}
	sortRoutes(api.routes)
	api.handler = withMiddleware(
		http.HandlerFunc(api.serveRoutes),
		options.Logger,
		options.MaxBodyBytes,
	)
	return api, nil
}

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	a.handler.ServeHTTP(writer, request)
}

func (a *API) Routes() []Route {
	return append([]Route(nil), a.routes...)
}

func (a *API) serveRoutes(writer http.ResponseWriter, request *http.Request) {
	match := &mux.RouteMatch{}
	if !a.router.Match(request, match) {
		if len(a.allowedMethods(request)) != 0 {
			a.handleMethodNotAllowed(writer, request)
			return
		}
		a.handleNotFound(writer, request)
		return
	}
	a.router.ServeHTTP(writer, request)
}

func (a *API) initializeBaseRoutes(apiURLSuffix string) {
	root := mux.NewRouter()
	a.router = root
	a.prefixes[root] = ""

	a.BaseRoutes = &Routes{Root: root}
	a.BaseRoutes.APIRoot = a.subrouter(root, apiURLSuffix)
	a.BaseRoutes.Health = a.subrouter(root, "/health")
	a.BaseRoutes.System = a.subrouter(a.BaseRoutes.APIRoot, "/system")
	a.BaseRoutes.Authentication = a.subrouter(a.BaseRoutes.APIRoot, "/auth")
	a.BaseRoutes.Users = a.subrouter(a.BaseRoutes.APIRoot, "/users")
	a.BaseRoutes.CurrentUser = a.subrouter(a.BaseRoutes.Users, "/me")
	a.BaseRoutes.Audits = a.subrouter(a.BaseRoutes.APIRoot, "/audits")
	a.BaseRoutes.Bootstrap = a.subrouter(a.BaseRoutes.APIRoot, "/bootstrap")
	a.BaseRoutes.Roles = a.subrouter(a.BaseRoutes.APIRoot, "/roles")
	a.BaseRoutes.Role = a.subrouter(
		a.BaseRoutes.Roles,
		"/{role_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.RoleBindings = a.subrouter(a.BaseRoutes.APIRoot, "/role-bindings")
	a.BaseRoutes.RoleBinding = a.subrouter(
		a.BaseRoutes.RoleBindings,
		"/{role_binding_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.Institution = a.subrouter(a.BaseRoutes.APIRoot, "/institution")
	a.BaseRoutes.AcademicUnits = a.subrouter(a.BaseRoutes.APIRoot, "/academic-units")
	a.BaseRoutes.AcademicUnit = a.subrouter(
		a.BaseRoutes.AcademicUnits,
		"/{academic_unit_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.Programmes = a.subrouter(a.BaseRoutes.APIRoot, "/programmes")
	a.BaseRoutes.Programme = a.subrouter(
		a.BaseRoutes.Programmes,
		"/{programme_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.ProgrammeLevels = a.subrouter(a.BaseRoutes.APIRoot, "/programme-levels")
	a.BaseRoutes.ProgrammeLevel = a.subrouter(
		a.BaseRoutes.ProgrammeLevels,
		"/{programme_level_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.AcademicPeriods = a.subrouter(a.BaseRoutes.APIRoot, "/academic-periods")
	a.BaseRoutes.AcademicPeriod = a.subrouter(
		a.BaseRoutes.AcademicPeriods,
		"/{academic_period_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.Classes = a.subrouter(a.BaseRoutes.APIRoot, "/classes")
	a.BaseRoutes.Class = a.subrouter(
		a.BaseRoutes.Classes,
		"/{class_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.User = a.subrouter(
		a.BaseRoutes.Users,
		"/{user_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.Affiliation = a.subrouter(
		a.BaseRoutes.APIRoot,
		"/affiliations/{affiliation_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.AcademicUnitMember = a.subrouter(
		a.BaseRoutes.APIRoot,
		"/academic-unit-members/{academic_unit_member_id:"+canonicalIDRoutePattern()+"}",
	)
	a.BaseRoutes.ClassMember = a.subrouter(
		a.BaseRoutes.APIRoot,
		"/class-members/{class_member_id:"+canonicalIDRoutePattern()+"}",
	)
}

func canonicalIDRoutePattern() string {
	return "[" + model.IdAlphabet + "]{" + strconv.Itoa(model.IdLength) + "}"
}

func (a *API) subrouter(parent *mux.Router, pathPrefix string) *mux.Router {
	router := parent.PathPrefix(pathPrefix).Subrouter()
	a.prefixes[router] = a.prefixes[parent] + pathPrefix
	return router
}

// Register binds one explicitly classified endpoint beneath a stable base
// route. path is relative to base and is empty when the resource root itself is
// the endpoint.
func (a *API) Register(
	base *mux.Router,
	path string,
	method string,
	handler *Handler,
) error {
	if handler == nil || handler.handler == nil {
		return fmt.Errorf("register %s %s: handler is nil", method, path)
	}
	auth := handler.authentication
	switch auth {
	case AuthPublic, AuthSessionRequired, AuthRefreshCredentialRequired:
	default:
		return fmt.Errorf("register %s %s: authentication policy is invalid", method, path)
	}
	prefix, exists := a.prefixes[base]
	if !exists {
		return fmt.Errorf("register %s %s: base route is not owned by this API", method, path)
	}
	if method == "" || strings.ToUpper(method) != method {
		return fmt.Errorf("register %s %s: HTTP method is invalid", method, path)
	}
	if path != "" && (!strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/")) {
		return fmt.Errorf("register %s %s: relative route path is not canonical", method, path)
	}
	fullPath := prefix + path
	probe := mux.NewRouter().NewRoute().Path(fullPath).Methods(method)
	if err := probe.GetError(); err != nil {
		return fmt.Errorf("register %s %s: invalid route: %w", method, fullPath, err)
	}
	pathRegexp, err := probe.GetPathRegexp()
	if err != nil {
		return fmt.Errorf("register %s %s: compile route: %w", method, fullPath, err)
	}
	compiledPathRegexp, err := regexp.Compile(pathRegexp)
	if err != nil {
		return fmt.Errorf("register %s %s: compile route regexp: %w", method, fullPath, err)
	}
	key := method + " " + pathRegexp
	if _, exists := a.routeKeys[key]; exists {
		return fmt.Errorf("register %s %s: duplicate route", method, fullPath)
	}

	registered := base.Handle(path, handler).Methods(method)
	if err := registered.GetError(); err != nil {
		return fmt.Errorf("register %s %s: %w", method, fullPath, err)
	}

	a.routeKeys[key] = struct{}{}
	route := Route{Method: method, Path: fullPath, Auth: auth}
	a.routes = append(a.routes, route)
	a.routeMatchers = append(a.routeMatchers, routeMatcher{
		route:      route,
		pathRegexp: compiledPathRegexp,
	})
	return nil
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(left, right int) bool {
		if routes[left].Path == routes[right].Path {
			return routes[left].Method < routes[right].Method
		}
		return routes[left].Path < routes[right].Path
	})
}

func (a *API) handleNotFound(writer http.ResponseWriter, request *http.Request) {
	WriteProblem(writer, Problem{
		Type:      "https://proctor.sudosylabs.com/problems/not-found",
		Title:     "Resource not found",
		Status:    http.StatusNotFound,
		Detail:    "The requested resource was not found.",
		Instance:  request.URL.Path,
		Code:      "not_found",
		RequestID: RequestID(request.Context()),
	})
}

func (a *API) handleMethodNotAllowed(writer http.ResponseWriter, request *http.Request) {
	if methods := a.allowedMethods(request); len(methods) != 0 {
		writer.Header().Set("Allow", strings.Join(methods, ", "))
	}
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

func (a *API) allowedMethods(request *http.Request) []string {
	allowed := make(map[string]struct{})
	for _, matcher := range a.routeMatchers {
		if matcher.pathRegexp.MatchString(request.URL.Path) {
			allowed[matcher.route.Method] = struct{}{}
		}
	}
	methods := make([]string, 0, len(allowed))
	for method := range allowed {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
