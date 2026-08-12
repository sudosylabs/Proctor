// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's application use cases and orchestration.
//
// App is the public facade. Its constructor projects the bounded root Store
// into exact per-model and named aggregate contracts, validates security and
// infrastructure capabilities before they become observable, and retains only
// focused services and behavioral ports. Identity,
// Access Control, academic relationships, durable audit, and Realtime remain
// cooperating sibling capabilities; none is a service locator for the others.
//
// The package owns application policy, authoritative authorization, audit
// ordering, transaction intent, and post-commit effects. It excludes HTTP and
// WebSocket wire behavior, SQL and concrete infrastructure, and reusable file
// content mechanics. Focused service implementations remain unexported behind
// App and consumer-owned interfaces. The app/job child package is the bounded
// exception for the generic durable Job execution engine.
package app
