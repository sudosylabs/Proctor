// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

// fromLegacyAppError is a no-op compatibility helper retained only while a few
// call sites still name the conversion explicitly. Prefer returning *app.Error
// directly. Remove in a follow-up cleanup after ticket 39.
func fromLegacyAppError(err error) error {
	return err
}
