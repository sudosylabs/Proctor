// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Application-owned academic administration follows Mattermost's pattern:
// authorize at the use-case boundary, keep persistence behind per-model
// stores, and surround every durable mutation with an authoritative audit.

package app

import (
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const defaultAdministrationListLimit = 100

func normalizeAdministrationLimit(limit int) int {
	if limit == 0 {
		return defaultAdministrationListLimit
	}
	return limit
}

func administrationError(where, resource string, err error) error {
	_ = where
	var appFailure *Error
	if errors.As(err, &appFailure) {
		return err
	}
	var validation *model.ValidationError
	if errors.As(err, &validation) {
		return domainInvalid(resource+".invalid", err)
	}
	code := "administration.unavailable"
	switch {
	case store.IsNotFound(err):
		code = "resource.not_found"
	case store.IsConflict(err):
		code = resource + ".conflict"
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "users_last_system_admin" {
			code = "user.last_system_admin"
		}
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			code = resource + ".invalid"
		}
	}
	return NewError(code).WithField("resource", resource).Wrap(err)
}
