// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package job is the single generic engine for durable, finite application
// Jobs and their runtime lifecycle. It owns immutable descriptor registration, database-backed claiming,
// leases and heartbeats, fenced state transitions, checkpoints, work budgets,
// retry and cancellation observation, recurrence timing, panic containment,
// safe operator projections, non-overlapping periodic runtime maintenance, and
// bounded shutdown. Periodic tasks create no Job, Job Attempt, or occurrence
// ledger; their named application/Store operation owns cross-node convergence.
//
// Execution is at-least-once. A successful claim is not an exactly-once
// guarantee, so handlers must make their domain effects idempotent and rely on
// the appropriate durable commit boundary. The engine owns execution
// mechanics, not the meaning of a Job type, command, checkpoint, progress
// stage, result, public error code, or recurring occurrence.
//
// Domain-specific handlers, commands, and recurrence proposers live in the
// sibling app/jobs package. Parent app supplies narrow use-case adapters and
// constructs Engine from the immutable app/jobs catalog plus domain-owned
// PeriodicTasks. The module-root
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
// recurrence, projections, controls, races, and shutdown. Concrete handler
// tests live in app/jobs, while PostgreSQL and root-lifecycle tests provide
// cross-component recovery and wiring evidence.
package job
