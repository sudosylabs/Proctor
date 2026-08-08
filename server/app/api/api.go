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
	"time"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// Logger is the narrow operational logging port owned by the HTTP transport.
// Composition supplies an mlog-backed adapter; package api never imports mlog.
type Logger interface {
	InfoContext(ctx context.Context, message string, fields ...LogField)
	ErrorContext(ctx context.Context, message string, fields ...LogField)
}

// LogField is one structured operational log attribute.
type LogField struct {
	Key   string
	Value any
}

func logString(key, value string) LogField { return LogField{Key: key, Value: value} }
func logInt(key string, value int) LogField  { return LogField{Key: key, Value: value} }
func logInt64(key string, value int64) LogField {
	return LogField{Key: key, Value: value}
}
func logAny(key string, value any) LogField { return LogField{Key: key, Value: value} }
func logErr(err error) LogField             { return LogField{Key: "error", Value: err} }

type Health interface {
	Live() bool
	Ready() bool
}

type AuthRequirement string

const (
	AuthPublic                      AuthRequirement = "public"
	AuthPrincipalRequired           AuthRequirement = "principal_required"
	AuthSessionRequired             AuthRequirement = "session_required"
	AuthStrongSessionRequired       AuthRequirement = "strong_session_required"
	AuthRecentSessionRequired       AuthRequirement = "recent_session_required"
	AuthStrongRecentSessionRequired AuthRequirement = "strong_recent_session_required"
	AuthRefreshCredentialRequired   AuthRequirement = "refresh_credential_required"
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

	Health               *mux.Router
	System               *mux.Router
	Authentication       *mux.Router
	IdentityProviders    *mux.Router
	IdentityProvider     *mux.Router
	Users                *mux.Router
	CurrentUser          *mux.Router
	MFA                  *mux.Router
	PersonalAccessTokens *mux.Router
	PersonalAccessToken  *mux.Router
	Audits               *mux.Router
	Bootstrap            *mux.Router
	Roles                *mux.Router
	Role                 *mux.Router
	RoleBindings         *mux.Router
	RoleBinding          *mux.Router
	Institution          *mux.Router
	AcademicUnits        *mux.Router
	AcademicUnit         *mux.Router
	Programmes           *mux.Router
	Programme            *mux.Router
	ProgrammeLevels      *mux.Router
	ProgrammeLevel       *mux.Router
	AcademicPeriods      *mux.Router
	AcademicPeriod       *mux.Router
	Classes              *mux.Router
	Class                *mux.Router
	User                 *mux.Router
	UserSessions         *mux.Router
	UserSession          *mux.Router
	Affiliation          *mux.Router
	AcademicUnitMember   *mux.Router
	ClassMember          *mux.Router
	WebSocket            *mux.Router
}

type Options struct {
	Logger                  Logger
	Health                  Health
	Application             Application
	AcademicUnits           AcademicUnitApplication
	Institutions            InstitutionApplication
	Programmes              ProgrammeApplication
	ProgrammeLevels         ProgrammeLevelApplication
	AcademicPeriods         AcademicPeriodApplication
	Classes                 ClassApplication
	Affiliations            AffiliationApplication
	AcademicUnitMembers     AcademicUnitMemberApplication
	ClassMembers            ClassMemberApplication
	UserProfiles            UserProfileApplication
	AccountStates           AccountStateApplication
	SessionAdministrations  SessionAdministrationApplication
	Roles                   RoleApplication
	RoleBindings            RoleBindingApplication
	AuditListings           AuditListingApplication
	Bootstrap               BootstrapApplication
	BuildInfo               BuildInfo
	PublicURL               string
	MaxBodyBytes            int64
	RecentAuthenticationTTL time.Duration
	NodeID                  string
	// WebSocket is the sibling transport constructed at composition root.
	// HTTP owns only route mounting and session middleware around Accept.
	WebSocket WebSocketTransport
}

// WebSocketTransport is the narrow mount surface HTTP needs from the sibling
// websocket package. Composition supplies the concrete hub.
type WebSocketTransport interface {
	Accept(
		writer http.ResponseWriter,
		request *http.Request,
		principal model.Principal,
		metadata model.RequestMetadata,
		connectionID string,
		sequence int64,
		allowMissingOrigin bool,
	) error
	Close() error
}

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	AuthenticateBearer(context.Context, string) (*model.Principal, error)
}

type Authentication interface {
	Authenticator
	Login(
		context.Context,
		application.Invocation,
		application.LoginCommand,
	) (*application.LoginResult, error)
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	RefreshSession(
		context.Context,
		application.Invocation,
		application.RefreshSessionCommand,
	) (*model.Session, *model.AuthenticationTokens, error)
	Logout(context.Context, application.Invocation, application.LogoutCommand) error
	RequestEmailVerification(
		context.Context,
		application.Invocation,
		application.RequestEmailVerificationCommand,
	) error
	CompleteEmailVerification(
		context.Context,
		application.Invocation,
		application.CompleteEmailVerificationCommand,
	) (*model.User, error)
	RequestPasswordReset(
		context.Context,
		application.Invocation,
		application.RequestPasswordResetCommand,
	) error
	CompletePasswordReset(
		context.Context,
		application.Invocation,
		application.CompletePasswordResetCommand,
	) (*model.User, error)
}

type ExternalAuthentication interface {
	ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider
	BeginExternalAuthentication(
		context.Context,
		application.Invocation,
		application.BeginExternalAuthenticationCommand,
	) (*model.ExternalAuthenticationStart, error)
	CompleteExternalAuthentication(
		context.Context,
		application.Invocation,
		application.CompleteExternalAuthenticationCommand,
	) (*model.ExternalAuthenticationCompletion, error)
}

type AccountStateApplication interface {
	SetUserEnabled(context.Context, application.Invocation, application.SetUserEnabledCommand) (*model.User, error)
}

type SessionAdministrationApplication interface {
	ListUserSessions(context.Context, application.Invocation, application.ListUserSessionsQuery) ([]*model.Session, error)
	RevokeUserSession(context.Context, application.Invocation, application.RevokeUserSessionCommand) error
	RevokeUserSessions(context.Context, application.Invocation, application.RevokeUserSessionsCommand) error
}

type UserProfileApplication interface {
	SearchUsers(context.Context, application.Invocation, application.SearchUsersQuery) ([]*model.User, error)
	GetUserProfile(context.Context, application.Invocation, application.GetUserProfileQuery) (*model.User, error)
	UpdateUserProfile(context.Context, application.Invocation, application.UpdateUserProfileCommand) (*model.User, error)
}

type AcademicUnitApplication interface {
	GetAcademicUnit(context.Context, application.Invocation, application.GetAcademicUnitQuery) (*model.AcademicUnit, error)
	ListAcademicUnits(context.Context, application.Invocation, application.ListAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	SearchAcademicUnits(context.Context, application.Invocation, application.SearchAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	CreateAcademicUnit(context.Context, application.Invocation, application.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
	UpdateAcademicUnit(context.Context, application.Invocation, application.UpdateAcademicUnitCommand) (*model.AcademicUnit, error)
	ArchiveAcademicUnit(context.Context, application.Invocation, application.ArchiveAcademicUnitCommand) error
}

type InstitutionApplication interface {
	GetInstitution(context.Context, application.Invocation, application.GetInstitutionQuery) (*model.Institution, error)
	UpdateInstitution(context.Context, application.Invocation, application.UpdateInstitutionCommand) (*model.Institution, error)
}

type ProgrammeApplication interface {
	GetProgramme(context.Context, application.Invocation, application.GetProgrammeQuery) (*model.Programme, error)
	ListProgrammes(context.Context, application.Invocation, application.ListProgrammesQuery) ([]*model.Programme, error)
	CreateProgramme(context.Context, application.Invocation, application.CreateProgrammeCommand) (*model.Programme, error)
	UpdateProgramme(context.Context, application.Invocation, application.UpdateProgrammeCommand) (*model.Programme, error)
	ArchiveProgramme(context.Context, application.Invocation, application.ArchiveProgrammeCommand) error
}

type ProgrammeLevelApplication interface {
	GetProgrammeLevel(context.Context, application.Invocation, application.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error)
	ListProgrammeLevels(context.Context, application.Invocation, application.ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error)
	CreateProgrammeLevel(context.Context, application.Invocation, application.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	UpdateProgrammeLevel(context.Context, application.Invocation, application.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	ArchiveProgrammeLevel(context.Context, application.Invocation, application.ArchiveProgrammeLevelCommand) error
}

type AcademicPeriodApplication interface {
	GetAcademicPeriod(context.Context, application.Invocation, application.GetAcademicPeriodQuery) (*model.AcademicPeriod, error)
	ListAcademicPeriods(context.Context, application.Invocation, application.ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error)
	CreateAcademicPeriod(context.Context, application.Invocation, application.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	UpdateAcademicPeriod(context.Context, application.Invocation, application.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	ArchiveAcademicPeriod(context.Context, application.Invocation, application.ArchiveAcademicPeriodCommand) error
}

type ClassApplication interface {
	GetClass(context.Context, application.Invocation, application.GetClassQuery) (*model.Class, error)
	ListClasses(context.Context, application.Invocation, application.ListClassesQuery) ([]*model.Class, error)
	SearchClasses(context.Context, application.Invocation, application.SearchClassesQuery) ([]*model.Class, error)
	CreateClass(context.Context, application.Invocation, application.CreateClassCommand) (*model.Class, error)
	UpdateClass(context.Context, application.Invocation, application.UpdateClassCommand) (*model.Class, error)
	ArchiveClass(context.Context, application.Invocation, application.ArchiveClassCommand) error
}

type AffiliationApplication interface {
	ListAffiliations(context.Context, application.Invocation, application.ListAffiliationsQuery) ([]*model.Affiliation, error)
	CreateAffiliation(context.Context, application.Invocation, application.CreateAffiliationCommand) (*model.Affiliation, error)
	EndAffiliation(context.Context, application.Invocation, application.EndAffiliationCommand) (*model.Affiliation, error)
}

type AcademicUnitMemberApplication interface {
	ListAcademicUnitMembers(context.Context, application.Invocation, application.ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error)
	CreateAcademicUnitMember(context.Context, application.Invocation, application.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EndAcademicUnitMember(context.Context, application.Invocation, application.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
}

type ClassMemberApplication interface {
	ListClassMembers(context.Context, application.Invocation, application.ListClassMembersQuery) ([]*model.ClassMember, error)
	EnrollClassMember(context.Context, application.Invocation, application.EnrollClassMemberCommand) (*model.ClassEnrollment, error)
	EndClassMember(context.Context, application.Invocation, application.EndClassMemberCommand) (*model.ClassMember, error)
}

type Sessions interface {
	ListSessions(context.Context, application.Invocation, application.ListSessionsQuery) ([]*model.Session, error)
	RevokeSession(context.Context, application.Invocation, application.RevokeSessionCommand) error
	RevokeAllSessions(context.Context, application.Invocation, application.RevokeAllSessionsCommand) error
}

type PersonalAccessTokens interface {
	CreatePersonalAccessToken(
		context.Context,
		application.Invocation,
		application.CreatePersonalAccessTokenCommand,
	) (*model.PersonalAccessTokenCreation, error)
	ListPersonalAccessTokens(
		context.Context,
		application.Invocation,
		application.ListPersonalAccessTokensQuery,
	) ([]*model.PersonalAccessToken, error)
	RevokePersonalAccessToken(
		context.Context,
		application.Invocation,
		application.RevokePersonalAccessTokenCommand,
	) (*model.PersonalAccessToken, error)
	SetPersonalAccessTokenDisabled(
		context.Context,
		application.Invocation,
		application.SetPersonalAccessTokenDisabledCommand,
	) (*model.PersonalAccessToken, error)
}

type MFA interface {
	GetMFAStatus(
		context.Context,
		application.Invocation,
		application.GetMFAStatusQuery,
	) (*application.MFAStatus, error)
	SetupMFA(
		context.Context,
		application.Invocation,
		application.SetupMFACommand,
	) (*application.MFASetup, error)
	ActivateMFA(
		context.Context,
		application.Invocation,
		application.ActivateMFACommand,
	) (*application.MFAActivation, error)
	ChallengeMFA(
		context.Context,
		application.Invocation,
		application.ChallengeMFACommand,
	) (*model.Session, error)
	RegenerateMFARecoveryCodes(
		context.Context,
		application.Invocation,
		application.RegenerateMFARecoveryCodesCommand,
	) ([]string, error)
	DisableMFA(
		context.Context,
		application.Invocation,
		application.DisableMFACommand,
	) error
}

type AuditListingApplication interface {
	ListAuditEvents(context.Context, application.Invocation, application.ListAuditEventsQuery) ([]*model.AuditEvent, error)
}

type BootstrapApplication interface {
	GetInstallationStatus(context.Context, application.GetInstallationStatusQuery) (*model.InstallationStatus, error)
	BootstrapInstallation(context.Context, application.Invocation, application.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error)
}

type RoleApplication interface {
	ListRoles(context.Context, application.Invocation, application.ListRolesQuery) ([]*model.Role, error)
	GetRole(context.Context, application.Invocation, application.GetRoleQuery) (*model.Role, error)
	CreateRole(context.Context, application.Invocation, application.CreateRoleCommand) (*model.Role, error)
	UpdateRole(context.Context, application.Invocation, application.UpdateRoleCommand) (*model.Role, error)
	ArchiveRole(context.Context, application.Invocation, application.ArchiveRoleCommand) error
}

type RoleBindingApplication interface {
	ListRoleBindings(context.Context, application.Invocation, application.ListRoleBindingsQuery) ([]*model.RoleBinding, error)
	CreateRoleBinding(context.Context, application.Invocation, application.CreateRoleBindingCommand) (*model.RoleBinding, error)
	EndRoleBinding(context.Context, application.Invocation, application.EndRoleBindingCommand) (*model.RoleBinding, error)
}

type Realtime interface {
	AuthorizeWebSocketSubscription(
		context.Context,
		model.Principal,
		model.RequestMetadata,
		model.Action,
		model.Resource,
	) error
	ValidateWebSocketPrincipal(context.Context, model.Principal) error
}

// Application is the cohesive application-facing API contract. Its component
// interfaces keep domain ownership visible without turning authentication into
// an unrelated service locator.
type Application interface {
	Authentication
	ExternalAuthentication
	PermissionChecker
	Sessions
	PersonalAccessTokens
	MFA
	InstitutionApplication
	Realtime
}

type API struct {
	handler                 http.Handler
	router                  *mux.Router
	BaseRoutes              *Routes
	application             Application
	academicUnits           AcademicUnitApplication
	institutions            InstitutionApplication
	programmes              ProgrammeApplication
	programmeLevels         ProgrammeLevelApplication
	academicPeriods         AcademicPeriodApplication
	classes                 ClassApplication
	affiliations            AffiliationApplication
	academicUnitMembers     AcademicUnitMemberApplication
	classMembers            ClassMemberApplication
	userProfiles            UserProfileApplication
	accountStates           AccountStateApplication
	sessionAdministrations  SessionAdministrationApplication
	roles                   RoleApplication
	roleBindings            RoleBindingApplication
	auditListings           AuditListingApplication
	bootstrap               BootstrapApplication
	logger                  Logger
	health                  Health
	buildInfo               BuildInfo
	cookies                 browserCookies
	recentAuthenticationTTL time.Duration
	routes                  []Route
	routeMatchers           []routeMatcher
	routeKeys               map[string]struct{}
	prefixes                map[*mux.Router]string
	webSocket               WebSocketTransport
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
	if options.AcademicUnits == nil {
		return nil, errors.New("academic unit reads are required")
	}
	if options.Institutions == nil {
		return nil, errors.New("institution application is required")
	}
	if options.Programmes == nil {
		return nil, errors.New("programme application is required")
	}
	if options.ProgrammeLevels == nil {
		return nil, errors.New("programme level application is required")
	}
	if options.AcademicPeriods == nil {
		return nil, errors.New("academic period application is required")
	}
	if options.Classes == nil {
		return nil, errors.New("class application is required")
	}
	if options.Affiliations == nil {
		return nil, errors.New("affiliation application is required")
	}
	if options.AcademicUnitMembers == nil {
		return nil, errors.New("academic unit member application is required")
	}
	if options.ClassMembers == nil {
		return nil, errors.New("class member application is required")
	}
	if options.UserProfiles == nil {
		return nil, errors.New("user profile application is required")
	}
	if options.AccountStates == nil {
		return nil, errors.New("account state application is required")
	}
	if options.SessionAdministrations == nil {
		return nil, errors.New("session administration application is required")
	}
	if options.Roles == nil {
		return nil, errors.New("role application is required")
	}
	if options.RoleBindings == nil {
		return nil, errors.New("role binding application is required")
	}
	if options.AuditListings == nil {
		return nil, errors.New("audit listing application is required")
	}
	if options.Bootstrap == nil {
		return nil, errors.New("bootstrap application is required")
	}
	if options.MaxBodyBytes <= 0 {
		return nil, errors.New("maximum body size must be greater than zero")
	}
	if options.RecentAuthenticationTTL <= 0 {
		return nil, errors.New("recent authentication TTL must be greater than zero")
	}
	if options.NodeID == "" {
		return nil, errors.New("cluster node ID is required")
	}
	cookies, err := newBrowserCookies(options.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("configure browser cookies: %w", err)
	}

	api := &API{
		application:             options.Application,
		academicUnits:           options.AcademicUnits,
		institutions:            options.Institutions,
		programmes:              options.Programmes,
		programmeLevels:         options.ProgrammeLevels,
		academicPeriods:         options.AcademicPeriods,
		classes:                 options.Classes,
		affiliations:            options.Affiliations,
		academicUnitMembers:     options.AcademicUnitMembers,
		classMembers:            options.ClassMembers,
		userProfiles:            options.UserProfiles,
		accountStates:           options.AccountStates,
		sessionAdministrations:  options.SessionAdministrations,
		roles:                   options.Roles,
		roleBindings:            options.RoleBindings,
		auditListings:           options.AuditListings,
		bootstrap:               options.Bootstrap,
		logger:                  options.Logger,
		health:                  options.Health,
		buildInfo:               options.BuildInfo,
		cookies:                 cookies,
		recentAuthenticationTTL: options.RecentAuthenticationTTL,
		routeKeys:               make(map[string]struct{}),
		prefixes:                make(map[*mux.Router]string),
		webSocket:               options.WebSocket,
	}
	if api.webSocket == nil {
		// Unit tests that exercise only HTTP DTO mapping may omit the hub.
		// Production composition always supplies the sibling websocket.Hub.
		api.webSocket = noopWebSocketTransport{}
	}
	api.initializeBaseRoutes(model.APIURLSuffix)
	if err := api.registerRoutes(); err != nil {
		return nil, err
	}
	sortRoutes(api.routes)
	api.handler = withMiddleware(
		http.HandlerFunc(api.serveRoutes),
		options.Logger,
		options.MaxBodyBytes,
	)
	return api, nil
}

func (a *API) registerRoutes() error {
	initializers := []func() error{
		a.InitSystem,
		a.InitAuthentication,
		a.InitExternalAuthentication,
		a.InitUsers,
		a.InitSessions,
		a.InitMFA,
		a.InitPersonalAccessTokens,
		a.InitAudits,
		a.InitBootstrap,
		a.InitRoles,
		a.InitRoleBindings,
		a.registerUserProfileRoutes,
		a.registerInstitutionRoutes,
		a.registerAcademicUnitRoutes,
		a.registerProgrammeRoutes,
		a.registerProgrammeLevelRoutes,
		a.registerAcademicPeriodRoutes,
		a.registerClassRoutes,
		a.registerAffiliationRoutes,
		a.registerAcademicUnitMemberRoutes,
		a.registerClassMemberRoutes,
		a.InitWebSocket,
	}
	for _, initialize := range initializers {
		if err := initialize(); err != nil {
			return err
		}
	}
	return nil
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
	a.BaseRoutes.IdentityProviders = a.subrouter(
		a.BaseRoutes.Authentication,
		"/providers",
	)
	a.BaseRoutes.IdentityProvider = a.subrouter(
		a.BaseRoutes.IdentityProviders,
		"/{provider_id:"+providerIDRoutePattern()+"}",
	)
	a.BaseRoutes.Users = a.subrouter(a.BaseRoutes.APIRoot, "/users")
	a.BaseRoutes.CurrentUser = a.subrouter(a.BaseRoutes.Users, "/me")
	a.BaseRoutes.MFA = a.subrouter(a.BaseRoutes.CurrentUser, "/mfa")
	a.BaseRoutes.PersonalAccessTokens = a.subrouter(
		a.BaseRoutes.CurrentUser,
		"/tokens",
	)
	a.BaseRoutes.PersonalAccessToken = a.subrouter(
		a.BaseRoutes.PersonalAccessTokens,
		"/{personal_access_token_id:"+canonicalIDRoutePattern()+"}",
	)
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
	a.BaseRoutes.UserSessions = a.subrouter(a.BaseRoutes.User, "/sessions")
	a.BaseRoutes.UserSession = a.subrouter(
		a.BaseRoutes.UserSessions,
		"/{session_id:"+canonicalIDRoutePattern()+"}",
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
	a.BaseRoutes.WebSocket = a.subrouter(a.BaseRoutes.APIRoot, "/websocket")
}

func (a *API) Close() error {
	if a.webSocket == nil {
		return nil
	}
	return a.webSocket.Close()
}

type noopWebSocketTransport struct{}

func (noopWebSocketTransport) Accept(
	http.ResponseWriter,
	*http.Request,
	model.Principal,
	model.RequestMetadata,
	string,
	int64,
	bool,
) error {
	return errors.New("websocket transport is not configured")
}

func (noopWebSocketTransport) Close() error { return nil }

func canonicalIDRoutePattern() string {
	return "[" + model.IdAlphabet + "]{" + strconv.Itoa(model.IdLength) + "}"
}

func providerIDRoutePattern() string {
	return "[a-z0-9][a-z0-9._-]{0,63}"
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
	case AuthPublic,
		AuthPrincipalRequired,
		AuthSessionRequired,
		AuthStrongSessionRequired,
		AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired,
		AuthRefreshCredentialRequired:
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
