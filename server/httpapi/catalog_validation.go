// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var transportProblemCodes = map[string]struct{}{
	"not_live":  {},
	"not_ready": {},
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
			if route.maxBodyBytes < 0 {
				return fmt.Errorf("resource %q %s maximum body size must not be negative", resource.name, route.method)
			}
			if effectiveIdempotencyRequirement(route.idempotency) != IdempotencyNone &&
				(!idempotencyPrincipalAuth(route.auth) || route.upgradeOperation != nil || route.operation == nil && route.protocolOperation == nil) {
				return fmt.Errorf("resource %q %s idempotency requires a principal route", resource.name, route.method)
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

func idempotencyPrincipalAuth(requirement AuthRequirement) bool {
	switch requirement {
	case AuthPrincipalRequired, AuthSessionRequired, AuthStrongSessionRequired, AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired:
		return true
	default:
		return false
	}
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
