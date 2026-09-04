// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// Auditable is implemented by model values that can produce a deliberately
// safe audit representation. Implementations must omit secrets, credentials,
// tokens, and unbounded user-controlled content.
type Auditable interface {
	Auditable() map[string]any
}
