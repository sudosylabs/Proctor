// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/api.go. Proctor applies its own
// immutable route catalog, typed authentication policies, and Problem Details
// boundary.

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

func logString(key, value string) LogField  { return LogField{Key: key, Value: value} }
func logInt(key string, value int) LogField { return LogField{Key: key, Value: value} }
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
type IdempotencyRequirement string

type RouteProtocolKind string

const (
	AuthPublic                      AuthRequirement = "public"
	AuthPrincipalRequired           AuthRequirement = "principal_required"
	AuthSessionRequired             AuthRequirement = "session_required"
	AuthStrongSessionRequired       AuthRequirement = "strong_session_required"
	AuthRecentSessionRequired       AuthRequirement = "recent_session_required"
	AuthStrongRecentSessionRequired AuthRequirement = "strong_recent_session_required"
	AuthRefreshCredentialRequired   AuthRequirement = "refresh_credential_required"
)

const (
	IdempotencyNone     IdempotencyRequirement = "none"
	IdempotencyOptional IdempotencyRequirement = "optional"
	IdempotencyRequired IdempotencyRequirement = "required"
)

const (
	RouteProtocolRedirect        RouteProtocolKind = "redirect"
	RouteProtocolBinaryDownload  RouteProtocolKind = "binary_download"
	RouteProtocolStreamingUpload RouteProtocolKind = "streaming_upload"
	RouteProtocolUpgrade         RouteProtocolKind = "upgrade"
)

type Route struct {
	Method       string
	Path         string
	Auth         AuthRequirement
	ErrorCodes   []string
	ProtocolName string
	ProtocolKind RouteProtocolKind
	Idempotency  IdempotencyRequirement
}

type routeMatcher struct {
	route      Route
	pathRegexp *regexp.Regexp
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
	Invitations             InvitationApplication
	OnboardingImports       OnboardingImportApplication
	UserProfiles            UserProfileApplication
	UserSettings            UserSettingsApplication
	AccountStates           AccountStateApplication
	SessionAdministrations  SessionAdministrationApplication
	Roles                   RoleApplication
	RoleBindings            RoleBindingApplication
	AuditListings           AuditListingApplication
	Bootstrap               BootstrapApplication
	AccessPolicy            AccessPolicyApplication
	Mail                    MailApplication
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
// websocket package. Composition supplies the concrete hub. Accept owns the
// response after invocation; a returned error must describe a pre-upgrade
// failure and must not follow a committed response.
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
}

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	AuthenticateBearer(context.Context, string) (*model.Principal, error)
}

type Authentication interface {
	Authenticator
	RegisterLocalUser(
		context.Context,
		application.Invocation,
		application.RegisterLocalUserCommand,
	) error
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
	ExternalAuthenticationProviders(context.Context) ([]model.ExternalAuthenticationProvider, error)
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
	ChangeUserEmail(context.Context, application.Invocation, application.ChangeUserEmailCommand) (*application.UserEmailState, error)
	VerifyUserEmailPrivileged(context.Context, application.Invocation, application.VerifyUserEmailPrivilegedCommand) (*application.UserEmailState, error)
	UploadProfilePicture(context.Context, application.Invocation, application.UploadProfilePictureCommand) (*model.User, error)
	RemoveProfilePicture(context.Context, application.Invocation, application.RemoveProfilePictureCommand) (*model.User, error)
	GetProfilePicture(context.Context, application.Invocation, application.GetProfilePictureQuery) (*application.ProfilePictureContent, error)
}

type UserSettingsApplication interface {
	ReadOwnUserSettings(context.Context, application.Invocation) (application.UserSettingsView, error)
	ReplaceOwnUserSettings(context.Context, application.Invocation, application.ReplaceOwnUserSettingsCommand) (application.UserSettingsReplacementResult, error)
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

type ExamApplication interface {
	CreateExam(context.Context, application.Invocation, application.CreateExamCommand) (application.ExamView, error)
	GetExam(context.Context, application.Invocation, application.GetExamQuery) (application.ExamView, error)
	EditExamDraftText(context.Context, application.Invocation, application.EditExamDraftTextCommand) (application.ExamView, error)
	ConfigureExamDraftFocusLoss(context.Context, application.Invocation, application.ConfigureExamDraftFocusLossCommand) (application.ExamView, error)
	ListExams(context.Context, application.Invocation, application.ListExamsQuery) (application.ExamCatalogPage, error)
	ArchiveExam(context.Context, application.Invocation, application.ArchiveExamCommand) (model.Exam, error)
	ListExamManagers(context.Context, application.Invocation, application.ListExamManagersQuery) (application.ExamManagerPage, error)
	AddExamManager(context.Context, application.Invocation, application.AddExamManagerCommand) (application.ExamManagerChange, error)
	RemoveExamManager(context.Context, application.Invocation, application.RemoveExamManagerCommand) (application.ExamManagerChange, error)
	TransferExamOwnership(context.Context, application.Invocation, application.TransferExamOwnershipCommand) (application.ExamManagerChange, error)
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
	DesktopAuthorization
	ExternalAuthentication
	authenticationMethodApplication
	Sessions
	PersonalAccessTokens
	MFA
	InstitutionApplication
	JobOperationsApplication
	MailApplication
	ExamApplication
	ExamRevisionApplication
	ExamSittingApplication
	ExamSittingCorrectionApplication
	ExamResourceApplication
	ExamStarterWorkspaceApplication
	ExamAttemptApplication
	ExamIntegrityReviewApplication
	Realtime
}

type MailApplication interface {
	GetMailKeyState(context.Context, application.Invocation) (application.MailKeyStateView, error)
	StartMailRekey(context.Context, application.Invocation, string) (application.MailRekeyView, error)
	GetMailRekeyStatus(context.Context, application.Invocation, model.JobID) (application.MailRekeyStatusView, error)
	SendTestMail(context.Context, application.Invocation) (application.MailDeliveryView, error)
	GetMailMetrics(context.Context, application.Invocation) (application.MailMetricsSnapshot, error)
	GetMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
	ListMailDeliveries(context.Context, application.Invocation, application.ListMailDeliveriesQuery) (application.MailDeliveryPage, error)
	CancelMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
	RetryMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
}

type API struct {
	handler                 http.Handler
	router                  *mux.Router
	authenticator           Authenticator
	logger                  Logger
	cookies                 browserCookies
	recentAuthenticationTTL time.Duration
	routes                  []Route
	routeMatchers           []routeMatcher
	catalog                 *routeCatalogBuilder
	webSocket               WebSocketTransport
	maxBodyBytes            int64
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
		authenticator:           options.Application,
		logger:                  options.Logger,
		cookies:                 cookies,
		recentAuthenticationTTL: options.RecentAuthenticationTTL,
		webSocket:               options.WebSocket,
	}
	if api.webSocket == nil {
		// Unit tests that exercise only HTTP DTO mapping may omit the hub.
		// Production composition always supplies the sibling websocket.Hub.
		api.webSocket = noopWebSocketTransport{}
	}
	resources := productionResources(options, cookies, api.webSocket)
	if err := api.buildRoutingKernel(
		model.APIURLSuffix,
		options.MaxBodyBytes,
		func() error { return api.collectResources(model.APIURLSuffix, resources...) },
	); err != nil {
		return nil, err
	}
	return api, nil
}

func productionResources(options Options, cookies browserCookies, webSocket WebSocketTransport) []resource {
	accessPolicy := options.AccessPolicy
	if accessPolicy == nil {
		accessPolicy = unavailableAccessPolicyApplication{}
	}
	invitations := options.Invitations
	if invitations == nil {
		invitations, _ = options.Application.(InvitationApplication)
	}
	onboardingImports := options.OnboardingImports
	if onboardingImports == nil {
		onboardingImports, _ = options.Application.(OnboardingImportApplication)
	}
	if onboardingImports == nil {
		onboardingImports = unavailableOnboardingImportApplication{}
	}
	if invitations == nil {
		invitations = unavailableInvitationApplication{}
	}
	return []resource{
		systemResource(options.Health, options.BuildInfo),
		bootstrapResource(options.Bootstrap),
		accessPolicyResource(accessPolicy),
		authenticationResource(options.Application, cookies),
		authenticationMethodResource(options.Application, cookies),
		desktopAuthorizationResource(options.Application),
		externalAuthenticationResource(options.Application, cookies),
		userProfileResource(options.UserProfiles),
		userSettingsResource(options.UserSettings),
		userAdministrationResource(options.AccountStates, options.SessionAdministrations),
		sessionResource(options.Application, cookies),
		mfaResource(options.Application),
		personalAccessTokenResource(options.Application),
		institutionResource(options.Institutions),
		academicUnitResource(options.AcademicUnits),
		examResource(options.Application),
		examRevisionResource(options.Application),
		examSittingResource(options.Application),
		examSittingCorrectionResource(options.Application),
		examResourceHTTPResource(options.Application),
		examStarterWorkspaceHTTPResource(options.Application),
		examAttemptResource(options.Application),
		examIntegrityReviewResource(options.Application),
		programmeResource(options.Programmes),
		programmeLevelResource(options.ProgrammeLevels),
		academicPeriodResource(options.AcademicPeriods),
		classResource(options.Classes),
		affiliationResource(options.Affiliations),
		academicUnitMemberResource(options.AcademicUnitMembers),
		classMemberResource(options.ClassMembers),
		invitationResource(invitations),
		onboardingImportResource(onboardingImports),
		roleResource(options.Roles),
		roleBindingResource(options.RoleBindings),
		auditResource(options.AuditListings),
		jobResource(options.Application),
		mailResource(options.Mail),
		webSocketResource(webSocket),
	}
}

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	a.handler.ServeHTTP(writer, request)
}

func (a *API) Routes() []Route {
	source := a.routes
	if a.catalog != nil {
		source = a.catalog.routes
	}
	routes := make([]Route, len(source))
	for index, route := range source {
		routes[index] = route
		routes[index].ErrorCodes = append([]string(nil), route.ErrorCodes...)
	}
	return routes
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

func (a *API) Close() error {
	// The HTTP transport borrows the sibling WebSocket transport for upgrade
	// dispatch. Node Runtime owns and closes that sibling explicitly.
	return nil
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

func canonicalIDRoutePattern() string {
	return "[" + model.IdAlphabet + "]{" + strconv.Itoa(model.IdLength) + "}"
}

func providerIDRoutePattern() string {
	return "[a-z0-9][a-z0-9._-]{0,63}"
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
	if writer.Header().Get("Cache-Control") == "" {
		writer.Header().Set("Cache-Control", "no-store")
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
