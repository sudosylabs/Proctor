// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package config owns Proctor's operator-supplied deployment configuration and
// its lifecycle.
//
// The package excludes durable application settings and business policy. It
// depends only on the Go standard library; the module-root composition code
// selects configuration paths and projects validated values into runtime
// policies and infrastructure adapters.
//
// A Store loads defaults, overlays a typed backing, and finally applies active
// PROCTOR_ environment settings. It keeps the validated backing snapshot
// separate from the effective snapshot observed by the runtime. Callers always
// receive clones, so neither snapshot can be mutated outside the Store.
//
// Environment settings are operator-owned process inputs: they take precedence
// in effective snapshots but are never written to the backing. Set protects the
// fields overridden in the caller's prior snapshot before it validates and
// persists the candidate, then reevaluates the current environment and validates
// the next effective snapshot. A failed load, transformation, validation, or
// save publishes neither snapshot and notifies no listener.
//
// Successful effective changes notify listeners after serialized Store work has
// completed, allowing a listener to perform another configuration operation.
// Store owns its backing lifecycle and Close is terminal.
package config
