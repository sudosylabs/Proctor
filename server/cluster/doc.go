// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package cluster owns Proctor's inter-node transport contracts: wire messages,
// handler registration, and the best-effort delivery API.
//
// Delivery is transient and non-durable. Handlers must be idempotent; security
// and business correctness recover from PostgreSQL, cache TTLs, and client
// resynchronization rather than assuming every peer receives every message.
//
// Concrete transports live in sibling packages such as cluster/local. This
// package imports only the Go standard library so the contract stays free of
// platform and adapter dependencies.
package cluster
