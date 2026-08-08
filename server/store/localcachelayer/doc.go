// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package localcachelayer caches a small handwritten allowlist of disposable
// store reads in bounded process-local memory. PostgreSQL remains authoritative.
// The package depends inward only on store and model contracts; it does not own
// PostgreSQL, cluster transport, application workflow caches, or security-state
// caching.
//
//go:generate go run ../storetest/layergen -layer localcache -source .. -output forwarding_gen.go
package localcachelayer
