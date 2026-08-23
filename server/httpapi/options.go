// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// Logger is the narrow operational logging port owned by the HTTP transport.
// Composition supplies a logging-backed adapter; package httpapi never imports logging.
type Logger interface {
	InfoContext(ctx context.Context, message string, fields ...LogField)
	ErrorContext(ctx context.Context, message string, fields ...LogField)
}

// Localizer is the narrow presentation-owned localization port. Application
// and domain failures remain stable codes; only the HTTP edge asks for prose.
type Localizer interface {
	Translate(locale, id string, args any) (string, error)
	SupportedLocales() []string
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

// Metrics is the narrow, transport-owned observation port. Route is the
// sealed template, never a request path or resource identifier.
type Metrics interface {
	HTTPStarted() func()
	ObserveHTTPRequest(route, method string, status int, duration time.Duration)
	ObserveHTTPPayload(route, method string, requestBytes, responseBytes int64)
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

// Route is an immutable manifest entry exposed for contract and OpenAPI
// agreement checks after the catalog has been sealed.
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

// Options contains composition-owned dependencies. New validates the complete
// set before compiling any resource route into the immutable HTTP manifest.
type Options struct {
	Logger                        Logger
	Metrics                       Metrics
	Localizer                     Localizer
	Health                        Health
	Application                   Application
	AcademicUnits                 AcademicUnitApplication
	Institutions                  InstitutionApplication
	Programmes                    ProgrammeApplication
	ProgrammeLevels               ProgrammeLevelApplication
	AcademicPeriods               AcademicPeriodApplication
	Classes                       ClassApplication
	Affiliations                  AffiliationApplication
	AcademicUnitMembers           AcademicUnitMemberApplication
	ClassMembers                  ClassMemberApplication
	Invitations                   InvitationApplication
	BrowserInvitations            BrowserInvitationApplication
	OnboardingImports             OnboardingImportApplication
	StudentProgressions           StudentProgressionApplication
	AcademicAdministrationBatches AcademicAdministrationBatchApplication
	UserProfiles                  UserProfileApplication
	UserSettings                  UserSettingsApplication
	AccountStates                 AccountStateApplication
	SessionAdministrations        SessionAdministrationApplication
	Roles                         RoleApplication
	RoleBindings                  RoleBindingApplication
	AuditListings                 AuditListingApplication
	Bootstrap                     BootstrapApplication
	AccessPolicy                  AccessPolicyApplication
	Mail                          MailApplication
	BuildInfo                     BuildInfo
	PublicURL                     string
	MaxBodyBytes                  int64
	RecentAuthenticationTTL       time.Duration
	NodeID                        string
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
