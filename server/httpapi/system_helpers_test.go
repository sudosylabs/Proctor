// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import "time"

func systemResource(health Health, buildInfo BuildInfo) resource {
	return systemResourceWithApplication(
		health,
		buildInfo,
		unavailableDesktopCompatibilityApplication{},
		time.Now,
	)
}
