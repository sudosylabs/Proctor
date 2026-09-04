// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import "strings"

// directorySearchPattern prepares a literal, case-insensitive substring for
// PostgreSQL LIKE predicates. Directory queries do not expose SQL wildcard
// syntax: %, _, and the selected escape character are ordinary input.
func directorySearchPattern(term string) string {
	escaped := strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(strings.TrimSpace(term))
	return "%" + escaped + "%"
}
