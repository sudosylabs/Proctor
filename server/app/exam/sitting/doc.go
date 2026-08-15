// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package sitting owns Exam Sitting scheduling, discovery, schedule changes,
// and manager cancellation application policy.
//
// Ordinary operations require current Exam management, exact Academic Unit
// membership, and the applicable role permission; explicit administrator
// overrides remain separate audited actions. Durable Store commands remain
// authoritative for revision, lifecycle, lineage, and academic-period checks;
// transient effects are published only after a non-replayed commit.
//
// The package does not own transports, SQL, lifecycle transitions after the
// Scheduled state, Jobs, Attempts, or file content. It depends inward only on
// model, bounded Store contracts, and consumer-owned authorization, audit, and
// effect ports; it never imports the parent application or concrete adapters.
package sitting
