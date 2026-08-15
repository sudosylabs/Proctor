// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package correction owns the bounded staging and atomic application policy
// for instructions and resource corrections to one Open or Paused Exam
// Sitting. It preserves immutable Revision history, frozen policy and Starter
// Workspace content, current authorization, private manager provenance,
// idempotency, and commit-before-effect ordering.
//
// The package does not own Draft authoring, future-default Revision selection,
// Attempt admission, candidate delivery, HTTP, SQL, VFS selection, or realtime
// infrastructure. It depends inward only on domain models, bounded Store
// contracts, and consumer-owned authorization, audit, content, and effect
// ports; the parent application remains the public facade.
package correction
