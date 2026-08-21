// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/api.go. Proctor applies its own
// immutable route catalog, typed authentication policies, and Problem Details
// boundary.

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

type API struct {
	handler                 http.Handler
	router                  *mux.Router
	authenticator           Authenticator
	logger                  Logger
	localizer               Localizer
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
	if options.Localizer != nil {
		for _, name := range []string{"bad_request", "client_error", "conflict", "forbidden", "internal", "not_found", "service_unavailable", "too_many_requests", "unauthorized"} {
			for _, field := range []string{"detail", "title"} {
				id := "problem." + name + "." + field
				if _, err := options.Localizer.Translate("", id, nil); err != nil {
					return nil, fmt.Errorf("validate localization %q: %w", id, err)
				}
			}
		}
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
		localizer:               options.Localizer,
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

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if a.localizer != nil {
		request = request.WithContext(withRequestLocalization(
			request.Context(), a.localizer, preferredLocale(request, a.localizer.SupportedLocales()),
		))
	}
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
