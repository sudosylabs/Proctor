// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// Auditable is implemented by model values that can produce a deliberately
// safe audit representation. Implementations must omit secrets, credentials,
// tokens, and unbounded user-controlled content.
type Auditable interface {
	Auditable() map[string]any
}
