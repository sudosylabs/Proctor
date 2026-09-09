// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"

	"github.com/sudosylabs/proctor/server/model"
)

// Membership continuation is a transport envelope. The application receives
// typed keyset selectors and authorizes the resource independently on every page.
type membershipPageCursor struct {
	Version     int                `json:"version"`
	ScopeType   model.ResourceType `json:"scope_type"`
	ScopeID     string             `json:"scope_id"`
	ActiveAt    *int64             `json:"active_at"`
	AfterUserID string             `json:"after_user_id"`
	AfterID     string             `json:"after_id"`
}

func membershipPageCursorSpec() opaqueCursorSpec[membershipPageCursor] {
	return opaqueCursorSpec[membershipPageCursor]{
		label: "membership", maximumEncodedLength: 1024, currentVersion: 1,
		members:        []string{"version", "scope_type", "scope_id", "active_at", "after_user_id", "after_id"},
		version:        func(cursor membershipPageCursor) int { return cursor.Version },
		setVersion:     func(cursor *membershipPageCursor, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate: func(cursor membershipPageCursor) error {
			if (cursor.ScopeType != model.ResourceAcademicUnit && cursor.ScopeType != model.ResourceClass) ||
				!model.IsValidId(cursor.ScopeID) || cursor.ActiveAt == nil || *cursor.ActiveAt < 0 ||
				!model.IsValidId(cursor.AfterUserID) || !model.IsValidId(cursor.AfterID) {
				return errors.New("invalid membership keyset")
			}
			return nil
		},
	}
}
