// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package timerlayer decorates the complete persistence store with bounded,
// argument-free operation timing. It changes no store result, error, or
// lifecycle semantics and depends only on the inward store contracts.
//
//go:generate go run ../storetest/layergen -layer timer -source .. -output forwarding_gen.go
package timerlayer
