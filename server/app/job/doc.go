// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package job is the single generic engine for durable, finite application
// Jobs. It owns immutable descriptor registration, database-backed claiming,
// leases and heartbeats, fenced state transitions, checkpoints, work budgets,
// retry and cancellation observation, recurrence timing, panic containment,
// safe operator projections, and bounded shutdown.
//
// Execution is at-least-once. A successful claim is not an exactly-once
// guarantee, so handlers must make their domain effects idempotent and rely on
// the appropriate durable commit boundary. The engine owns execution
// mechanics, not the meaning of a Job type, command, checkpoint, progress
// stage, result, public error code, or recurring occurrence.
//
// Domain-specific handlers and recurrence proposers remain in the parent app
// package beside their owning use cases. App constructs Engine from those
// adapters through immutable Descriptors and Recurrences. The module-root
// server retains the constructed Engine and owns when it starts and closes;
// construction itself is inert.
//
// Operator inspection and descriptor-governed transition mechanics live here.
// Actor-sensitive authorization, resource resolution, durable audit ordering,
// and transport error translation remain in app. This package never imports
// its parent, transports, platform services, or concrete persistence adapters;
// it depends inward only on model and store.JobStore.
//
// Tests at this boundary cover registration, execution, fencing, recovery,
// recurrence, projections, controls, races, and shutdown. Handler tests remain
// with their application adapters, while PostgreSQL and root-lifecycle tests
// provide cross-component recovery and wiring evidence.
package job
