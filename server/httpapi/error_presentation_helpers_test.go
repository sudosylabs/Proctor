// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

// ApplicationErrorStatuses returns a copy of the registered application-code
// to HTTP status mapping. Tests use it to prove every registered code produces
// its declared status.
func ApplicationErrorStatuses() map[string]int {
	cloned := make(map[string]int, len(applicationErrorMappings))
	for code, mapping := range applicationErrorMappings {
		cloned[code] = mapping.status
	}
	return cloned
}
