// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package retrylayer retries only explicitly allowlisted, idempotent store
// operations after adapter-classified transient failures.
//
//go:generate go run ../storetest/layergen -layer retry -source .. -output forwarding_gen.go
package retrylayer
