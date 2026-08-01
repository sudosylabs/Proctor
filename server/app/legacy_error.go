// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import "github.com/sudosylabs/proctor/server/model"

// fromLegacyAppError adapts a model.AppError into a transport-neutral
// application error. It preserves the stable machine code, safe fields, and
// wrapped cause so HTTP mapping can keep characterized public responses while
// capabilities migrate off the legacy type. Remove after ticket 39.
func fromLegacyAppError(err *model.AppError) error {
	if err == nil {
		return nil
	}
	out := NewError(err.ErrorCode())
	if fields := err.SafeFields(); len(fields) > 0 {
		out = out.WithFields(fields)
	}
	if cause := err.Unwrap(); cause != nil {
		out = out.Wrap(cause)
	}
	return out
}
