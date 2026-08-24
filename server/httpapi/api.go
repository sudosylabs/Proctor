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
	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

type API struct {
	handler                 http.Handler
	router                  *mux.Router
	authenticator           Authenticator
	logger                  Logger
	metrics                 Metrics
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
	if options.Localizer == nil {
		return nil, errors.New("localizer is required")
	}
	for _, definition := range LocalizationDefinitions() {
		if _, err := options.Localizer.Translate("", definition.ID, nil); err != nil {
			return nil, fmt.Errorf("validate localization %q: %w", definition.ID, err)
		}
	}
	if options.Health == nil {
		return nil, errors.New("health state is required")
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
	if options.WebSocket == nil {
		return nil, errors.New("websocket transport is required")
	}
	cookies, err := newBrowserCookies(options.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("configure browser cookies: %w", err)
	}
	applications, err := resolveResourceApplications(options)
	if err != nil {
		return nil, err
	}

	api := &API{
		authenticator:           applications.authenticator,
		logger:                  options.Logger,
		metrics:                 options.Metrics,
		localizer:               options.Localizer,
		cookies:                 cookies,
		recentAuthenticationTTL: options.RecentAuthenticationTTL,
		webSocket:               options.WebSocket,
		maxBodyBytes:            options.MaxBodyBytes,
	}
	resources := productionResources(applications, options.Health, options.BuildInfo, cookies, api.webSocket)
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
			request.Context(), a.localizer,
			localization.PreferredLocale(request.Header.Get("Accept-Language"), a.localizer.SupportedLocales()),
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
		if a.metrics != nil {
			method := boundedHTTPMethod(request.Method)
			recorder, observation := beginRequestMetrics(writer, request, a.metrics, "unmatched", method)
			defer observation.finish(0)
			writer = recorder
		}
		if len(a.allowedMethods(request)) != 0 {
			a.handleMethodNotAllowed(writer, request)
			return
		}
		a.handleNotFound(writer, request)
		return
	}
	a.router.ServeHTTP(writer, request)
}

func boundedHTTPMethod(method string) string {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func (a *API) Close() error {
	// The HTTP transport borrows the sibling WebSocket transport for upgrade
	// dispatch. Node Runtime owns and closes that sibling explicitly.
	return nil
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
	title, detail := localizedProblemPresentation(request, http.StatusNotFound)
	WriteProblem(writer, Problem{
		Type:      "https://proctor.sudosylabs.com/problems/not-found",
		Title:     title,
		Status:    http.StatusNotFound,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      "not_found",
		RequestID: RequestID(request.Context()),
	})
}

func (a *API) handleMethodNotAllowed(writer http.ResponseWriter, request *http.Request) {
	if methods := a.allowedMethods(request); len(methods) != 0 {
		writer.Header().Set("Allow", strings.Join(methods, ", "))
	}
	title, detail := localizedNamedProblemPresentation(request, "method_not_allowed")
	WriteProblem(writer, Problem{
		Type:      "https://proctor.sudosylabs.com/problems/method-not-allowed",
		Title:     title,
		Status:    http.StatusMethodNotAllowed,
		Detail:    detail,
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
