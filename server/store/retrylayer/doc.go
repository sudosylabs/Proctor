// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package retrylayer retries only explicitly allowlisted, idempotent store
// operations after adapter-classified transient failures.
//
//go:generate go run ../storetest/layergen -layer retry -source .. -output forwarding_gen.go
package retrylayer
