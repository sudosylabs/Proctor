// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package sitting owns Exam Sitting scheduling, discovery, manager lifecycle
// commands, and trusted lifecycle reconciliation application policy.
//
// Ordinary operations require current Exam management, exact Academic Unit
// membership, and the applicable role permission; explicit administrator
// overrides remain separate audited actions. Durable Store commands remain
// authoritative for revision, lifecycle, lineage, and academic-period checks;
// transient effects are published only after a non-replayed commit.
//
// The package constructs lifecycle Job intents through a consumer-owned
// factory, while persistence atomically commits each intent with its Sitting
// mutation. It does not execute Jobs and does not own transports, SQL,
// Attempts, submission sealing, or file content. It depends inward only on
// model, bounded Store contracts, and consumer-owned authorization, audit,
// Job-construction, and effect ports; it never imports the parent application
// or concrete adapters.
package sitting
