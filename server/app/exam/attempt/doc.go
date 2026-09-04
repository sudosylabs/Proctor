// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package attempt owns candidate admission, Participation and Connection
// fencing, authenticated renewal, database-time expiry enforcement, bounded
// Focus Loss evaluation, policy suspension and manager re-allow,
// correction-aware protected presentation, and the acknowledged mutable
// Attempt Workspace. Ended Focus Loss claims enter its explicit bounded
// discrepancy use case and can never rewrite collected evidence. It also owns voluntary immutable Submission sealing,
// actorless per-Attempt sealing invoked by bounded Sitting-close work, and
// purpose-specific protected manager inspection. Workspace use cases own
// path-safe manifest and journal recovery, selective entry fences, staged
// content coordination, Attempt-scoped idempotency, audit intent, and safe
// post-commit refetch hints. The automatic seal use case owns system-audit
// intent and post-commit effects but neither pages populations nor schedules
// Jobs. The package does not own HTTP or WebSocket wire contracts, SQL, VFS
// selection, durable Job execution, or infrastructure lifecycle. The parent
// exam package remains the public application facade.
//
// The package depends inward on model, the bounded Exam Attempt, Attempt
// Workspace, and Submission Store contracts, the shared safemarkdown leaf,
// and narrow consumer-owned audit, content, and realtime ports. It never
// imports its parent package, transports, concrete adapters, or platform.
package attempt
