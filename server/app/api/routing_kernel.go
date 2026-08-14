// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	parts       []pathPart
	rootMounted bool
}

func apiPath(parts ...pathPart) routePath {
	return routePath{parts: append([]pathPart(nil), parts...)}
}

func rootPath(parts ...pathPart) routePath {
	return routePath{parts: append([]pathPart(nil), parts...), rootMounted: true}
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
	prefix := apiPrefix
	if path.rootMounted {
		prefix = ""
	}
	return prefix + "/" + strings.Join(template, "/"),
		prefix + "/" + strings.Join(normalized, "/"), nil
}

type operationRequest struct {
	context        context.Context
	principal      model.Principal
	metadata       model.RequestMetadata
	params         Params
	request        *http.Request
	idempotencyKey string
}

type operationResult struct {
	status    int
	body      any
	noContent bool
	problem   *Problem
	headers   http.Header
}

func jsonResult(status int, body any) operationResult {
	return operationResult{status: status, body: body}
}

func noContentResult() operationResult {
	return operationResult{status: http.StatusNoContent, noContent: true}
}

func statusResult(status int) operationResult {
	return operationResult{status: status, noContent: true}
}

func problemResult(problem Problem) operationResult {
	return operationResult{status: problem.Status, problem: &problem}
}

func (result operationResult) withHeaders(headers http.Header) operationResult {
	result.headers = headers.Clone()
	return result
}

type operation func(operationRequest) (operationResult, error)

type protocolResult struct {
	kind          RouteProtocolKind
	status        int
	location      string
	body          io.ReadCloser
	contentLength int64
	jsonBody      any
	headers       http.Header
}

func redirectProtocolResult(location string) protocolResult {
	return protocolResult{kind: RouteProtocolRedirect, status: http.StatusSeeOther, location: location}
}

func binaryDownloadProtocolResult(body io.ReadCloser, contentLength int64) protocolResult {
	return protocolResult{kind: RouteProtocolBinaryDownload, status: http.StatusOK, body: body, contentLength: contentLength}
}

func notModifiedProtocolResult(contentLength int64) protocolResult {
	return protocolResult{kind: RouteProtocolBinaryDownload, status: http.StatusNotModified, contentLength: contentLength}
}

func streamingUploadProtocolResult(status int, body any) protocolResult {
	return protocolResult{kind: RouteProtocolStreamingUpload, status: status, jsonBody: body}
}

func (result protocolResult) withHeaders(headers http.Header) protocolResult {
	result.headers = headers.Clone()
	return result
}

type protocolOperation func(operationRequest) (protocolResult, error)
type upgradeOperation func(http.ResponseWriter, operationRequest) error

type routeDefinition struct {
	method            string
	path              routePath
	auth              AuthRequirement
	errorCodes        []string
	operation         operation
	protocolName      string
	protocolKind      RouteProtocolKind
	protocolOperation protocolOperation
	upgradeOperation  upgradeOperation
	idempotency       IdempotencyRequirement
}

// upgradeRoute is the sole raw response exception. A successful HTTP upgrade
// must transfer the response connection to the sibling transport, so this
// operation alone receives ResponseWriter. Authentication, request metadata,
// parameters, declared failures, and immutable manifest metadata remain owned
// by the routing kernel.
func upgradeRoute(
	name string,
	auth AuthRequirement,
	method string,
	path routePath,
	errorCodes []string,
	operation upgradeOperation,
) routeDefinition {
	return routeDefinition{
		method: method, path: path, auth: auth,
		errorCodes:   append([]string(nil), errorCodes...),
		protocolName: name, protocolKind: RouteProtocolUpgrade,
		upgradeOperation: operation, idempotency: IdempotencyNone,
	}
}

func route(
	auth AuthRequirement,
	method string,
	path routePath,
	errorCodes []string,
	operation operation,
) routeDefinition {
	return routeDefinition{
		method: method, path: path, auth: auth,
		errorCodes: append([]string(nil), errorCodes...), operation: operation,
		idempotency: IdempotencyNone,
	}
}

func idempotentPrincipalRoute(requirement IdempotencyRequirement, method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	definition := principalRoute(method, path, errorCodes, operation)
	definition.idempotency = requirement
	return definition
}

func publicRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthPublic, method, path, errorCodes, operation)
}

func principalRoute(
	method string,
	path routePath,
	errorCodes []string,
	operation operation,
) routeDefinition {
	return route(AuthPrincipalRequired, method, path, errorCodes, operation)
}

func sessionRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthSessionRequired, method, path, errorCodes, operation)
}

func strongSessionRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthStrongSessionRequired, method, path, errorCodes, operation)
}

func recentSessionRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthRecentSessionRequired, method, path, errorCodes, operation)
}

func strongRecentSessionRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthStrongRecentSessionRequired, method, path, errorCodes, operation)
}

func refreshCredentialRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthRefreshCredentialRequired, method, path, errorCodes, operation)
}

// protocolRoute is the reviewed escape hatch for a named HTTP protocol whose
// success response cannot be represented by the ordinary typed JSON/result
// vocabulary (for example, an external-provider redirect or bounded binary
// transfer). Authentication and safe error policy remain kernel-owned.
func protocolRoute(
	name string,
	kind RouteProtocolKind,
	auth AuthRequirement,
	method string,
	path routePath,
	errorCodes []string,
	operation protocolOperation,
) routeDefinition {
	return routeDefinition{
		method: method, path: path, auth: auth,
		errorCodes:   append([]string(nil), errorCodes...),
		protocolName: name, protocolKind: kind, protocolOperation: operation,
		idempotency: IdempotencyNone,
	}
}

type resource struct {
	name   string
	routes []routeDefinition
}

var transportProblemCodes = map[string]struct{}{
	"not_live":  {},
	"not_ready": {},
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
			if !validIdempotencyRequirement(route.idempotency) {
				return fmt.Errorf("resource %q %s idempotency requirement is invalid", resource.name, route.method)
			}
			if effectiveIdempotencyRequirement(route.idempotency) != IdempotencyNone && (route.auth != AuthPrincipalRequired || route.operation == nil) {
				return fmt.Errorf("resource %q %s idempotency requires an ordinary principal route", resource.name, route.method)
			}
			ordinary := route.operation != nil
			protocol := route.protocolOperation != nil
			upgrade := route.upgradeOperation != nil
			if countTrue(ordinary, protocol, upgrade) != 1 {
				return fmt.Errorf("resource %q %s operation is required", resource.name, route.method)
			}
			if (protocol || upgrade) && !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(route.protocolName) {
				return fmt.Errorf("resource %q %s protocol operation name %q is invalid", resource.name, route.method, route.protocolName)
			}
			if (protocol || upgrade) && !validRouteProtocolKind(route.protocolKind) {
				return fmt.Errorf("resource %q %s protocol operation kind %q is invalid", resource.name, route.method, route.protocolKind)
			}
			if upgrade && route.protocolKind != RouteProtocolUpgrade {
				return fmt.Errorf("resource %q %s upgrade operation kind %q is invalid", resource.name, route.method, route.protocolKind)
			}
			if protocol && route.protocolKind == RouteProtocolUpgrade {
				return fmt.Errorf("resource %q %s upgrade requires the dedicated upgrade operation", resource.name, route.method)
			}
			if ordinary && (route.protocolName != "" || route.protocolKind != "") {
				return fmt.Errorf("resource %q %s ordinary operation has protocol metadata", resource.name, route.method)
			}
			path, normalized, err := route.path.compile(apiPrefix)
			if err != nil {
				return fmt.Errorf("resource %q %s path: %w", resource.name, route.method, err)
			}
			if upgrade && (resource.name != webSocketResourceName ||
				route.protocolName != webSocketProtocolName ||
				route.auth != AuthSessionRequired ||
				route.method != http.MethodGet ||
				path != apiPrefix+"/"+webSocketPathLiteral) {
				return fmt.Errorf("resource %q %s %s is not the reserved WebSocket upgrade", resource.name, route.method, path)
			}
			key := route.method + " " + normalized
			if prior, exists := routeShapes[key]; exists {
				return fmt.Errorf("resource %q %s %s has duplicate route shape with %s", resource.name, route.method, path, prior)
			}
			routeShapes[key] = resource.name
			seenErrors := make(map[string]struct{}, len(route.errorCodes))
			for _, code := range route.errorCodes {
				_, applicationCode := applicationErrorMappings[code]
				_, transportCode := transportProblemCodes[code]
				if !applicationCode && !transportCode {
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

func validIdempotencyRequirement(requirement IdempotencyRequirement) bool {
	return requirement == "" || requirement == IdempotencyNone || requirement == IdempotencyOptional || requirement == IdempotencyRequired
}

func effectiveIdempotencyRequirement(requirement IdempotencyRequirement) IdempotencyRequirement {
	if requirement == "" {
		return IdempotencyNone
	}
	return requirement
}

func validRouteProtocolKind(kind RouteProtocolKind) bool {
	switch kind {
	case RouteProtocolRedirect, RouteProtocolBinaryDownload, RouteProtocolStreamingUpload, RouteProtocolUpgrade:
		return true
	default:
		return false
	}
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
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
	pendingRoutes []pendingCatalogRoute
	routes        []Route
	routeMatchers []routeMatcher
}

func newRouteCatalogBuilder() *routeCatalogBuilder {
	return &routeCatalogBuilder{
		routeKeys: make(map[string]struct{}),
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
			definition.idempotency = effectiveIdempotencyRequirement(definition.idempotency)
			path, _, err := definition.path.compile(apiPrefix)
			if err != nil {
				return fmt.Errorf("compile resource %q: %w", resource.name, err)
			}
			errorPolicy := newRouteErrorPolicy(definition.errorCodes)
			operationHandler := a.operationHandler(definition, errorPolicy)
			if definition.protocolOperation != nil {
				operationHandler = a.protocolOperationHandler(definition, errorPolicy)
			} else if definition.upgradeOperation != nil {
				operationHandler = a.upgradeOperationHandler(definition, errorPolicy)
			}
			operationHandler = a.withIdempotency(operationHandler, definition.idempotency)
			handler := a.newHandlerWithErrorPolicy(
				operationHandler,
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
				Method:       definition.method,
				Path:         path,
				Auth:         definition.auth,
				ErrorCodes:   append([]string(nil), definition.errorCodes...),
				ProtocolName: definition.protocolName,
				ProtocolKind: definition.protocolKind,
				Idempotency:  definition.idempotency,
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

func (a *API) upgradeOperationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, authenticationRequiredError())
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, invalidRequestError("route_params", nil))
			return
		}
		err := definition.upgradeOperation(writer, operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request,
		})
		if err == nil {
			return
		}
		cause, headers := responseErrorParts(err)
		if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
			a.logInvalidRouteError(request, cause)
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, headers)
		writeApplicationError(writer, request, a.logger, cause)
	})
}

type headerOnlyResponseWriter struct {
	header http.Header
}

func (writer headerOnlyResponseWriter) Header() http.Header { return writer.header }

func (headerOnlyResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("response header operation cannot write a body")
}

func (headerOnlyResponseWriter) WriteHeader(int) {
	panic("response header capture cannot write a status")
}

func captureResponseHeaders(operation func(http.ResponseWriter)) http.Header {
	headers := make(http.Header)
	if operation != nil {
		operation(headerOnlyResponseWriter{header: headers})
	}
	return headers
}

func combineResponseHeaders(groups ...http.Header) http.Header {
	combined := make(http.Header)
	for _, headers := range groups {
		for key, values := range headers {
			for _, value := range values {
				combined.Add(key, value)
			}
		}
	}
	return combined
}

type operationResponseError struct {
	err     error
	headers http.Header
}

func (failure *operationResponseError) Error() string { return failure.err.Error() }
func (failure *operationResponseError) Unwrap() error { return failure.err }

func errorWithHeaders(err error, headers http.Header) error {
	if err == nil {
		return nil
	}
	return &operationResponseError{err: err, headers: headers.Clone()}
}

func responseErrorParts(err error) (error, http.Header) {
	var responseError *operationResponseError
	if errors.As(err, &responseError) {
		return responseError.err, responseError.headers.Clone()
	}
	return err, nil
}

func validateResponseHeaders(headers http.Header) error {
	for key, values := range headers {
		if key == "" || http.CanonicalHeaderKey(key) == "" || strings.ContainsAny(key, "\r\n") {
			return fmt.Errorf("response header name %q is invalid", key)
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("response header %q contains an invalid value", key)
			}
		}
	}
	return nil
}

func applyResponseHeaders(writer http.ResponseWriter, headers http.Header) {
	for key, values := range headers {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
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
			request: request, idempotencyKey: idempotencyKeyFromContext(request.Context()),
		})
		if err != nil {
			cause, headers := responseErrorParts(err)
			if failure, ok := cause.(applicationFailure); ok && failure.Code() == "idempotency.in_progress" {
				if headers == nil {
					headers = make(http.Header)
				}
				headers.Set("Retry-After", "1")
			}
			if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
				a.logInvalidRouteError(request, cause)
				WriteProblem(writer, internalProblem(request))
				return
			}
			applyResponseHeaders(writer, headers)
			writeApplicationError(writer, request, a.logger, cause)
			return
		}
		if err := validateOperationResult(result, errorPolicy); err != nil {
			a.logger.ErrorContext(request.Context(), "route returned an invalid operation result", logErr(err))
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, result.headers)
		if result.problem != nil {
			WriteProblem(writer, *result.problem)
			return
		}
		if result.noContent {
			if result.status == http.StatusNoContent {
				writer.Header().Set("Cache-Control", "no-store")
			}
			writer.WriteHeader(result.status)
			return
		}
		writeJSON(writer, result.status, result.body)
	})
}

func validateOperationResult(result operationResult, errorPolicy routeErrorPolicy) error {
	if err := validateResponseHeaders(result.headers); err != nil {
		return err
	}
	if result.problem != nil {
		if result.status < 400 || result.status > 599 || result.problem.Status != result.status ||
			result.body != nil || result.noContent || result.problem.Code == "" {
			return errors.New("problem result shape is invalid")
		}
		if _, allowed := errorPolicy[result.problem.Code]; !allowed {
			return fmt.Errorf("problem code %q is undeclared", result.problem.Code)
		}
		return nil
	}
	if result.status < 200 || result.status > 299 {
		return fmt.Errorf("success status %d is invalid", result.status)
	}
	if result.noContent && result.body != nil {
		return errors.New("empty result contains a body")
	}
	return nil
}

func (a *API) protocolOperationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, authenticationRequiredError())
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, invalidRequestError("route_params", nil))
			return
		}
		result, err := definition.protocolOperation(operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request,
		})
		if result.body != nil {
			defer func() {
				if err := result.body.Close(); err != nil {
					a.logger.ErrorContext(request.Context(), "close protocol response body", logErr(err))
				}
			}()
		}
		if err != nil {
			cause, headers := responseErrorParts(err)
			if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
				a.logInvalidRouteError(request, cause)
				WriteProblem(writer, internalProblem(request))
				return
			}
			applyResponseHeaders(writer, headers)
			writeApplicationError(writer, request, a.logger, cause)
			return
		}
		if err := validateProtocolResult(definition.protocolKind, result); err != nil {
			a.logger.ErrorContext(request.Context(), "route returned an invalid protocol result", logErr(err))
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, result.headers)
		switch result.kind {
		case RouteProtocolRedirect:
			http.Redirect(writer, request, result.location, result.status)
		case RouteProtocolBinaryDownload:
			if result.status == http.StatusNotModified {
				writer.Header().Set("Content-Length", strconv.FormatInt(result.contentLength, 10))
				writer.WriteHeader(result.status)
				return
			}
			writer.Header().Set("Content-Length", strconv.FormatInt(result.contentLength, 10))
			writer.WriteHeader(result.status)
			written, copyErr := io.CopyN(writer, result.body, result.contentLength)
			if copyErr != nil || written != result.contentLength {
				a.logger.ErrorContext(request.Context(), "write bounded protocol response", logInt64("written", written), logErr(copyErr))
			}
		case RouteProtocolStreamingUpload:
			writeJSON(writer, result.status, result.jsonBody)
		}
	})
}

func validateProtocolResult(expected RouteProtocolKind, result protocolResult) error {
	if result.kind != expected {
		return fmt.Errorf("protocol result kind %q does not match route kind %q", result.kind, expected)
	}
	if err := validateResponseHeaders(result.headers); err != nil {
		return err
	}
	if result.headers.Get("Location") != "" || result.headers.Get("Content-Length") != "" {
		return errors.New("protocol-owned headers cannot be supplied by an operation")
	}
	switch result.kind {
	case RouteProtocolRedirect:
		if result.status != http.StatusSeeOther || result.location == "" || strings.ContainsAny(result.location, "\r\n") ||
			result.body != nil || result.contentLength != 0 || result.jsonBody != nil {
			return errors.New("redirect protocol result is invalid")
		}
		if _, err := url.Parse(result.location); err != nil {
			return fmt.Errorf("redirect location: %w", err)
		}
	case RouteProtocolBinaryDownload:
		if result.location != "" || result.jsonBody != nil {
			return errors.New("binary protocol result contains incompatible fields")
		}
		if result.status == http.StatusNotModified {
			if result.body != nil || result.contentLength < 0 {
				return errors.New("not-modified binary result contains a body")
			}
		} else if result.status != http.StatusOK || result.body == nil || result.contentLength < 0 {
			return errors.New("binary protocol result is invalid")
		}
	case RouteProtocolStreamingUpload:
		if result.status < 200 || result.status > 299 || result.location != "" || result.body != nil || result.contentLength != 0 {
			return errors.New("streaming upload protocol result is invalid")
		}
	default:
		return fmt.Errorf("protocol result kind %q is unsupported", result.kind)
	}
	return nil
}

func writeRouteApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	logger Logger,
	policy routeErrorPolicy,
	err error,
) {
	if !routeErrorAllowed(policy, err) {
		logger.ErrorContext(
			request.Context(),
			"route returned an undeclared application error",
			logString("request_id", RequestID(request.Context())),
			logString("error_id", applicationErrorCode(err)),
			logErr(err),
		)
		WriteProblem(writer, internalProblem(request))
		return
	}
	writeApplicationError(writer, request, logger, err)
}

func routeErrorAllowed(policy routeErrorPolicy, err error) bool {
	if policy == nil {
		return true
	}
	_, allowed := policy[applicationErrorCode(err)]
	return allowed
}

func (a *API) logInvalidRouteError(request *http.Request, err error) {
	a.logger.ErrorContext(
		request.Context(),
		"route returned an invalid application error response",
		logString("request_id", RequestID(request.Context())),
		logString("error_id", applicationErrorCode(err)),
		logErr(err),
	)
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
