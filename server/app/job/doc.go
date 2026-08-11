// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package job executes durable, finite application Jobs.
//
// Engine owns registration, claiming, leases, heartbeats, fenced state
// transitions, checkpoints, work budgets, retries, cancellation observation,
// panic containment, and bounded shutdown. Domain-specific handlers remain in
// their owning application packages and enter through immutable Descriptors.
// The engine also owns the lifecycle of pre-start daily recurrence proposers;
// proposers retain application meaning and enqueue through narrow adapters.
// Safe operator projections and descriptor-governed control transitions also
// live here, while callers retain authorization, durable audit, and transport
// error translation.
// PostgreSQL remains authoritative through store.JobStore; this package does
// not select infrastructure or expose transport concerns.
package job
