// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import "net/http"

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
	maxBodyBytes      int64
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
		errorCodes:   routeErrorCodes(auth, errorCodes),
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
		errorCodes: routeErrorCodes(auth, errorCodes), operation: operation,
		idempotency: IdempotencyNone,
	}
}

func routeErrorCodes(auth AuthRequirement, errorCodes []string) []string {
	result := append([]string(nil), errorCodes...)
	switch auth {
	case AuthPrincipalRequired, AuthSessionRequired, AuthStrongSessionRequired,
		AuthRecentSessionRequired, AuthStrongRecentSessionRequired:
		for _, code := range []string{
			"authentication.dpop.invalid",
			"authentication.dpop.replayed",
			"authentication.dpop.use_nonce",
			"authentication.dpop.unavailable",
		} {
			found := false
			for _, existing := range result {
				if existing == code {
					found = true
					break
				}
			}
			if !found {
				result = append(result, code)
			}
		}
	}
	return result
}

func idempotentPrincipalRoute(requirement IdempotencyRequirement, method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	definition := principalRoute(method, path, errorCodes, operation)
	definition.idempotency = requirement
	return definition
}

// idempotentProtocolRoute declares a bounded non-upgrade protocol operation
// whose command key must reach the protocol handler. It is used for streaming
// uploads that cannot be represented by the ordinary JSON operation shape.
func idempotentProtocolRoute(
	requirement IdempotencyRequirement,
	maxBodyBytes int64,
	name string,
	kind RouteProtocolKind,
	auth AuthRequirement,
	method string,
	path routePath,
	errorCodes []string,
	operation protocolOperation,
) routeDefinition {
	definition := protocolRoute(name, kind, auth, method, path, errorCodes, operation)
	definition.idempotency = requirement
	definition.maxBodyBytes = maxBodyBytes
	return definition
}

func publicRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	return route(AuthPublic, method, path, errorCodes, operation)
}

func principalRoute(method string, path routePath, errorCodes []string, operation operation) routeDefinition {
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
		errorCodes:   routeErrorCodes(auth, errorCodes),
		protocolName: name, protocolKind: kind, protocolOperation: operation,
		idempotency: IdempotencyNone,
	}
}

type resource struct {
	name   string
	routes []routeDefinition
}

func newResource(name string, routes ...routeDefinition) resource {
	return resource{name: name, routes: append([]routeDefinition(nil), routes...)}
}

type protocolOperation func(operationRequest) (protocolResult, error)
type upgradeOperation func(http.ResponseWriter, operationRequest) error
