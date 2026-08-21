// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package httpapi implements Proctor's versioned HTTP transport.
//
// The package owns transport concerns: the immutable route catalog, request
// parameters and DTOs, strict decoding and body limits, credential and
// assurance classification, response serialization, Problem Details, and the
// safe route manifest used for OpenAPI agreement. Application use cases remain
// authoritative for action and resource authorization.
//
// New is the only production construction entry point. It validates every
// required dependency, projects the composition-only application capabilities
// into each resource's narrow consumer-owned interface, compiles the complete
// catalog, rejects ambiguous or invalid routes, and seals the handler before
// returning. Resources never receive the API, a mutable router, Store,
// platform services, or a late-registration hook.
//
// A resource owns a cohesive path family, its wire DTOs, domain mapping,
// explicit credential and assurance requirements, and its allowlist of public
// errors. Ordinary operations return typed results; the kernel validates the
// full result before applying headers, cookies, bodies, or errors. Named,
// bounded protocol results cover redirects, downloads, and uploads. The
// session-authenticated WebSocket handshake is the sole reserved raw upgrade
// exception because the sibling transport must take ownership of the upgraded
// connection.
//
// To extend the API, add or update one cohesive resource constructor, depend
// on the narrowest application capability that serves it, add the resource to
// the explicit production catalog, update the checked-in OpenAPI contract, and
// exercise the resource through the real routing kernel. Do not expose router
// implementation types or add package initialization registration.
//
// Routes returns a defensive, read-only projection of the sealed catalog for
// agreement tests and diagnostics. It is not a registration interface.
package httpapi
