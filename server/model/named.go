// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"regexp"
	"unicode/utf8"
)

const (
	NameMaxLength       = 64
	DisplayNameMaxRunes = 128
	DescriptionMaxRunes = 1024
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func sanitizeNamed(name, displayName, description *string) {
	*name = SanitizeUnicode(*name)
	*displayName = SanitizeUnicode(*displayName)
	*description = SanitizeUnicode(*description)
}

func validateNamed(where, modelName, id, name, displayName, description string) error {
	details := "id=" + id
	if len(name) == 0 || len(name) > NameMaxLength || !validName.MatchString(name) {
		return invalidModelError(
			where,
			modelName,
			"name",
			"must contain only lowercase letters, numbers, hyphens, and underscores",
			details,
		)
	}
	displayNameLength := utf8.RuneCountInString(displayName)
	if displayNameLength == 0 || displayNameLength > DisplayNameMaxRunes {
		return invalidModelError(where, modelName, "display_name", "has an invalid length", details)
	}
	if utf8.RuneCountInString(description) > DescriptionMaxRunes {
		return invalidModelError(where, modelName, "description", "is too long", details)
	}
	return nil
}
