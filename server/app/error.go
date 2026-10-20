// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

// domainInvalid maps a domain ValidationError into a stable application code
// while preserving safe field context for transport mapping.
func domainInvalid(code apperror.Code, err error) error {
	out := apperror.New(code)
	var validation *model.ValidationError
	if errors.As(err, &validation) {
		if fields := validation.SafeFields(); len(fields) > 0 {
			out = out.WithFields(fields)
		}
	}
	return out.Wrap(err)
}
