// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"strings"
)

// Localization selects the installation-wide fallback locale. Catalog
// availability is validated by the i18n module during root composition.
type Localization struct {
	DefaultLocale string `json:"DefaultLocale"`
}

func validLocaleIdentifier(value string) bool {
	if value == "" || len(value) > 35 || strings.TrimSpace(value) != value {
		return false
	}
	for index, part := range strings.Split(value, "-") {
		if part == "" || len(part) > 8 {
			return false
		}
		for _, character := range part {
			if character < 'A' || character > 'Z' {
				if character < 'a' || character > 'z' {
					if index == 0 || character < '0' || character > '9' {
						return false
					}
				}
			}
		}
	}
	return true
}
