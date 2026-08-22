// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package server is Proctor's sole composition root and Node Runtime lifecycle
// owner. Business policy remains in the application layer.
//
// New follows one ordered, inert recipe: acquire concrete infrastructure,
// transfer it atomically to Platform, borrow a lifecycle-free construction
// projection, assemble application and transport consumers, discard the
// projection, and return one Server. Platform owns accepted infrastructure on
// every acceptance outcome; the root never closes or navigates it afterward.
// One validated configuration snapshot drives construction, while consumers
// retain only focused immutable settings and capabilities.
//
// Start enters Platform, durable Jobs, WebSocket, listener ownership and HTTP
// serving in order, then publishes readiness. Failure and Close first make the
// node unready, drain HTTP when serving began, and dispose Jobs, WebSocket, the
// HTTP transport and Platform exactly once. The primary listener remains
// Server-owned until HTTP's first accept handoff; the HTTP runtime owns any
// configured ACME challenge and forwarding listener. Constructors never bind
// listeners or start background work.
//
// NewForTesting uses that same private recipe with concrete adapter
// substitutions. Its result exposes only Server, the application facade and an
// HTTP handler; tests retain references to adapters they supplied when they
// need lifecycle observations.
//
// Add infrastructure by extending root acquisition, the ownership-taking
// Platform contract and the lifecycle-free construction projection together.
// Add runtime modules by placing construction in the ordered consumer recipe
// and lifecycle entry/exit in Server. Do not add a second composition path,
// Platform locator getter, phase override or hidden startup helper.
package server
