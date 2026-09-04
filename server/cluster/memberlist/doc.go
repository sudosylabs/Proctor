// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package memberlist implements Proctor's built-in multi-node cluster adapter.
// It owns encrypted gossip membership, best-effort direct messaging, node
// compatibility checks, and the private maintenance of disposable discovery
// leases used to find bootstrap peers.
//
// The package does not own durable delivery, application authorization,
// application event meaning, or SQL persistence. It may depend on the
// transport-neutral contracts in package cluster and on HashiCorp Memberlist;
// the composition root supplies the concrete cluster.DiscoveryStore adapter.
// Discovery time, scheduling, lease policy, and store operations remain behind
// a private maintenance module. Construction is inert: it performs no store,
// network, timer, or goroutine work. Transport remains the sole owner of
// network and discovery-maintenance lifecycle.
package memberlist
