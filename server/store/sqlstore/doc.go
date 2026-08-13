// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package sqlstore implements Proctor's authoritative PostgreSQL persistence
// adapter.
//
// The package owns SQL schema projections, query execution, transaction
// lifecycle, locking, constraint translation, and fail-closed reconstruction
// of durable rows into validated domain models. Ordinary transaction mechanics
// remain private here; callers use named Store operations whose contracts state
// their complete atomic and concurrency guarantees.
//
// Package sqlstore excludes application policy, authorization decisions,
// transport contracts, transient cache state, and post-commit effects. It may
// depend inward on store contracts and domain models plus PostgreSQL and SQL
// implementation libraries. Domain packages never depend on database/sql, and
// generic transaction callbacks never cross the Store interface.
package sqlstore
