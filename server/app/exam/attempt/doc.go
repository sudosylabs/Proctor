// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package attempt owns candidate admission, Participation and Connection
// fencing, authenticated renewal, database-time expiry enforcement, policy
// suspension and manager re-allow, correction-aware protected presentation,
// and the acknowledged mutable Attempt Workspace. Workspace use cases own
// path-safe manifest and journal recovery, selective entry fences, staged
// content coordination, Attempt-scoped idempotency, audit intent, and safe
// post-commit refetch hints. It does not own HTTP or WebSocket wire contracts,
// SQL, VFS selection, durable Jobs, or infrastructure lifecycle. The parent
// exam package remains the public application facade.
//
// The package depends inward on model, the bounded Exam Attempt Store, and
// narrow consumer-owned audit, content, and realtime ports. It
// never imports its parent package, transports, concrete adapters, or platform.
package attempt
