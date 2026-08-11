// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

// The routing kernel vocabulary is deliberately private. Resource modules
// describe HTTP contracts with these values; gorilla/mux remains an
// implementation detail of the compiler.

type parameterKind uint8

const (
	parameterCanonicalID parameterKind = iota + 1
	parameterProviderID
)

type pathPart interface {
	compile() (template string, normalized string, err error)
}

type pathLiteral string

func literal(value string) pathPart { return pathLiteral(value) }

func (part pathLiteral) compile() (string, string, error) {
	value := string(part)
	if !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(value) {
		return "", "", fmt.Errorf("path literal %q is not canonical", value)
	}
	return value, value, nil
}

type pathParameter struct {
	name string
	kind parameterKind
}

func canonicalID(name string) pathPart {
	return pathParameter{name: name, kind: parameterCanonicalID}
}

func providerID(name string) pathPart {
	return pathParameter{name: name, kind: parameterProviderID}
}

func (part pathParameter) compile() (string, string, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(part.name) {
		return "", "", fmt.Errorf("path parameter name %q is not canonical", part.name)
	}
	var pattern, normalized string
	switch part.kind {
	case parameterCanonicalID:
		pattern = canonicalIDRoutePattern()
		normalized = "canonical_id"
	case parameterProviderID:
		pattern = providerIDRoutePattern()
		normalized = "provider_id"
	default:
		return "", "", fmt.Errorf("path parameter %q has unsupported parameter kind %d", part.name, part.kind)
	}
	return "{" + part.name + ":" + pattern + "}", "{" + normalized + "}", nil
}

type routePath struct {
	parts []pathPart
}

func apiPath(parts ...pathPart) routePath {
	return routePath{parts: append([]pathPart(nil), parts...)}
}

func (path routePath) compile(apiPrefix string) (string, string, error) {
	if apiPrefix == "" || !strings.HasPrefix(apiPrefix, "/") || strings.HasSuffix(apiPrefix, "/") {
		return "", "", fmt.Errorf("API prefix %q is not canonical", apiPrefix)
	}
	if len(path.parts) == 0 {
		return "", "", errors.New("route path is empty")
	}
	template := make([]string, 0, len(path.parts))
	normalized := make([]string, 0, len(path.parts))
	for _, part := range path.parts {
		if part == nil {
			return "", "", errors.New("route path contains a nil part")
		}
		compiled, shape, err := part.compile()
		if err != nil {
			return "", "", err
		}
		template = append(template, compiled)
		normalized = append(normalized, shape)
	}
	return apiPrefix + "/" + strings.Join(template, "/"),
		apiPrefix + "/" + strings.Join(normalized, "/"), nil
}

type operationRequest struct {
	context   context.Context
	principal model.Principal
	metadata  model.RequestMetadata
	params    Params
	request   *http.Request
}

type operationResult struct {
	status    int
	body      any
	noContent bool
}

func jsonResult(status int, body any) operationResult {
	return operationResult{status: status, body: body}
}

func noContentResult() operationResult {
	return operationResult{status: http.StatusNoContent, noContent: true}
}

type operation func(operationRequest) (operationResult, error)

type routeDefinition struct {
	method     string
	path       routePath
	auth       AuthRequirement
	errorCodes []string
	operation  operation
}

func principalRoute(
	method string,
	path routePath,
	errorCodes []string,
	operation operation,
) routeDefinition {
	return routeDefinition{
		method: method, path: path, auth: AuthPrincipalRequired,
		errorCodes: append([]string(nil), errorCodes...), operation: operation,
	}
}

type resource struct {
	name   string
	routes []routeDefinition
}

func newResource(name string, routes ...routeDefinition) resource {
	return resource{name: name, routes: append([]routeDefinition(nil), routes...)}
}

func validateResourceCatalog(apiPrefix string, resources []resource) error {
	resourceNames := make(map[string]struct{}, len(resources))
	routeShapes := make(map[string]string)
	for _, resource := range resources {
		if resource.name == "" {
			return errors.New("resource name is required")
		}
		if _, exists := resourceNames[resource.name]; exists {
			return fmt.Errorf("resource name %q is duplicated", resource.name)
		}
		resourceNames[resource.name] = struct{}{}
		if len(resource.routes) == 0 {
			return fmt.Errorf("resource %q has no routes", resource.name)
		}
		for _, route := range resource.routes {
			if !isHTTPMethod(route.method) || strings.ToUpper(route.method) != route.method {
				return fmt.Errorf("resource %q HTTP method %q is invalid", resource.name, route.method)
			}
			if !validAuthRequirement(route.auth) {
				return fmt.Errorf("resource %q authentication requirement is invalid", resource.name)
			}
			if route.operation == nil {
				return fmt.Errorf("resource %q %s operation is required", resource.name, route.method)
			}
			path, normalized, err := route.path.compile(apiPrefix)
			if err != nil {
				return fmt.Errorf("resource %q %s path: %w", resource.name, route.method, err)
			}
			key := route.method + " " + normalized
			if prior, exists := routeShapes[key]; exists {
				return fmt.Errorf("resource %q %s %s has duplicate route shape with %s", resource.name, route.method, path, prior)
			}
			routeShapes[key] = resource.name
			seenErrors := make(map[string]struct{}, len(route.errorCodes))
			for _, code := range route.errorCodes {
				if _, exists := applicationErrorMappings[code]; !exists {
					return fmt.Errorf("resource %q %s %s public error code %q is not mapped", resource.name, route.method, path, code)
				}
				if _, exists := seenErrors[code]; exists {
					return fmt.Errorf("resource %q %s %s public error code %q is duplicated", resource.name, route.method, path, code)
				}
				seenErrors[code] = struct{}{}
			}
		}
	}
	return nil
}

func validAuthRequirement(requirement AuthRequirement) bool {
	switch requirement {
	case AuthPublic,
		AuthPrincipalRequired,
		AuthSessionRequired,
		AuthStrongSessionRequired,
		AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired,
		AuthRefreshCredentialRequired:
		return true
	default:
		return false
	}
}

func isHTTPMethod(method string) bool {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

type pendingCatalogRoute struct {
	method  string
	path    string
	handler http.Handler
}

type routeCatalogBuilder struct {
	routeKeys     map[string]struct{}
	prefixes      map[*mux.Router]string
	pendingRoutes []pendingCatalogRoute
	routes        []Route
	routeMatchers []routeMatcher
}

func newRouteCatalogBuilder() *routeCatalogBuilder {
	return &routeCatalogBuilder{
		routeKeys: make(map[string]struct{}),
		prefixes:  make(map[*mux.Router]string),
	}
}

type routeErrorPolicy map[string]struct{}

func newRouteErrorPolicy(codes []string) routeErrorPolicy {
	policy := make(routeErrorPolicy, len(codes))
	for _, code := range codes {
		policy[code] = struct{}{}
	}
	return policy
}

func (a *API) buildRoutingKernel(
	apiPrefix string,
	maxBodyBytes int64,
	collect func() error,
) error {
	if a.authenticator == nil {
		return errors.New("routing kernel authenticator is required")
	}
	if a.logger == nil {
		return errors.New("routing kernel logger is required")
	}
	if maxBodyBytes <= 0 {
		return errors.New("routing kernel maximum body size must be greater than zero")
	}
	if collect == nil {
		return errors.New("routing kernel catalog collector is required")
	}
	if a.catalog != nil || a.handler != nil {
		return errors.New("routing kernel is already built")
	}
	a.catalog = newRouteCatalogBuilder()
	a.initializeBaseRoutes(apiPrefix)
	if err := collect(); err != nil {
		a.catalog = nil
		return err
	}
	if err := a.sealRouteCatalog(); err != nil {
		a.catalog = nil
		return err
	}
	a.handler = withMiddleware(
		http.HandlerFunc(a.serveRoutes),
		a.logger,
		maxBodyBytes,
	)
	return nil
}

func (a *API) collectResources(apiPrefix string, resources ...resource) error {
	if a.catalog == nil {
		return errors.New("collect resources after route catalog was sealed")
	}
	if err := validateResourceCatalog(apiPrefix, resources); err != nil {
		return err
	}
	for _, resource := range resources {
		for _, definition := range resource.routes {
			path, _, err := definition.path.compile(apiPrefix)
			if err != nil {
				return fmt.Errorf("compile resource %q: %w", resource.name, err)
			}
			errorPolicy := newRouteErrorPolicy(definition.errorCodes)
			handler := a.newHandlerWithErrorPolicy(
				a.operationHandler(definition, errorPolicy),
				definition.auth,
				errorPolicy,
			)
			probe := mux.NewRouter().NewRoute().Path(path).Methods(definition.method)
			if err := probe.GetError(); err != nil {
				return fmt.Errorf("compile resource %q %s %s: %w", resource.name, definition.method, path, err)
			}
			pathRegexp, err := probe.GetPathRegexp()
			if err != nil {
				return fmt.Errorf("compile resource %q %s %s matcher: %w", resource.name, definition.method, path, err)
			}
			compiledRegexp, err := regexp.Compile(pathRegexp)
			if err != nil {
				return fmt.Errorf("compile resource %q %s %s regexp: %w", resource.name, definition.method, path, err)
			}
			key := definition.method + " " + pathRegexp
			if _, exists := a.catalog.routeKeys[key]; exists {
				return fmt.Errorf("compile resource %q %s %s: duplicate route shape", resource.name, definition.method, path)
			}
			route := Route{
				Method:     definition.method,
				Path:       path,
				Auth:       definition.auth,
				ErrorCodes: append([]string(nil), definition.errorCodes...),
			}
			sort.Strings(route.ErrorCodes)
			a.catalog.routeKeys[key] = struct{}{}
			a.catalog.routes = append(a.catalog.routes, route)
			a.catalog.routeMatchers = append(a.catalog.routeMatchers, routeMatcher{route: route, pathRegexp: compiledRegexp})
			a.catalog.pendingRoutes = append(a.catalog.pendingRoutes, pendingCatalogRoute{
				method: definition.method, path: path, handler: handler,
			})
		}
	}
	return nil
}

func (a *API) sealRouteCatalog() error {
	if a.catalog == nil {
		return errors.New("route catalog is already sealed")
	}
	router := mux.NewRouter()
	for _, route := range a.catalog.pendingRoutes {
		registered := router.Handle(route.path, route.handler).Methods(route.method)
		if err := registered.GetError(); err != nil {
			return fmt.Errorf("compile %s %s: %w", route.method, route.path, err)
		}
	}
	sortRoutes(a.catalog.routes)
	a.router = router
	a.routes = a.catalog.routes
	a.routeMatchers = a.catalog.routeMatchers
	a.catalog = nil
	return nil
}

func (a *API) operationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(
				writer, request, a.logger, errorPolicy,
				authenticationRequiredError(),
			)
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(
				writer, request, a.logger, errorPolicy,
				invalidRequestError("route_params", nil),
			)
			return
		}
		result, err := definition.operation(operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request,
		})
		if err != nil {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, err)
			return
		}
		if result.status < 200 || result.status > 399 {
			a.logger.ErrorContext(request.Context(), "route returned an invalid success status", logInt("status", result.status))
			WriteProblem(writer, internalProblem(request))
			return
		}
		if result.noContent {
			if result.status != http.StatusNoContent || result.body != nil {
				a.logger.ErrorContext(request.Context(), "route returned an invalid no-content result")
				WriteProblem(writer, internalProblem(request))
				return
			}
			writer.Header().Set("Cache-Control", "no-store")
			writer.WriteHeader(result.status)
			return
		}
		writeJSON(writer, result.status, result.body)
	})
}

func writeRouteApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	logger Logger,
	policy routeErrorPolicy,
	err error,
) {
	code := applicationErrorCode(err)
	if policy != nil {
		if _, allowed := policy[code]; !allowed {
			logger.ErrorContext(
				request.Context(),
				"route returned an undeclared application error",
				logString("request_id", RequestID(request.Context())),
				logString("error_id", code),
				logErr(err),
			)
			WriteProblem(writer, internalProblem(request))
			return
		}
	}
	writeApplicationError(writer, request, logger, err)
}

func authRequiresPrincipal(requirement AuthRequirement) bool {
	switch requirement {
	case AuthPrincipalRequired,
		AuthSessionRequired,
		AuthStrongSessionRequired,
		AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired:
		return true
	default:
		return false
	}
}

func (request operationRequest) invocation() application.Invocation {
	return application.NewInvocation(request.principal, request.metadata)
}

func (request operationRequest) decodeJSON(target any, where string) error {
	if err := decodeRequestJSON(request.request, target); err != nil {
		return invalidRequestError(where, err)
	}
	return nil
}

func (request operationRequest) queryLimit() (int, error) {
	value := request.request.URL.Query().Get("limit")
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		return 0, invalidRequestError("limit", err)
	}
	return limit, nil
}
