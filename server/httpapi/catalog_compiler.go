// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"

	"github.com/gorilla/mux"
)

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
	return &routeCatalogBuilder{routeKeys: make(map[string]struct{})}
}

type routeErrorPolicy map[string]struct{}

func newRouteErrorPolicy(codes []string) routeErrorPolicy {
	policy := make(routeErrorPolicy, len(codes))
	for _, code := range codes {
		policy[code] = struct{}{}
	}
	return policy
}

func (a *API) buildRoutingKernel(apiPrefix string, maxBodyBytes int64, collect func() error) error {
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
	a.maxBodyBytes = maxBodyBytes
	if err := collect(); err != nil {
		a.catalog = nil
		return err
	}
	if err := a.sealRouteCatalog(); err != nil {
		a.catalog = nil
		return err
	}
	a.handler = withMiddleware(http.HandlerFunc(a.serveRoutes), a.logger)
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
			handler := a.newHandlerWithErrorPolicy(operationHandler, definition.auth, errorPolicy)
			bodyLimit := a.maxBodyBytes
			if definition.maxBodyBytes > 0 {
				bodyLimit = definition.maxBodyBytes
			}
			handler = limitRequestBody(handler, bodyLimit)
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
				Method: definition.method, Path: path, Auth: definition.auth,
				ErrorCodes:   append([]string(nil), definition.errorCodes...),
				ProtocolName: definition.protocolName, ProtocolKind: definition.protocolKind,
				Idempotency: definition.idempotency,
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
