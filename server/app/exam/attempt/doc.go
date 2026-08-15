// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package attempt owns candidate admission, Participation and Connection
// fencing, correction-aware protected presentation, and read-only Attempt
// Workspace bootstrap mechanics. It does not own HTTP or WebSocket wire
// contracts, mutable workspace operations, renewal/expiry enforcement, SQL,
// VFS selection, Jobs, or infrastructure lifecycle. The parent exam package
// remains the public application facade.
//
// The package depends inward on model, the bounded Exam Attempt Store, and
// narrow consumer-owned audit, content, presentation, and realtime ports. It
// never imports its parent package, transports, concrete adapters, or platform.
package attempt
