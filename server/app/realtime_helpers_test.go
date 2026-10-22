// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import apprealtime "github.com/sudosylabs/proctor/server/app/realtime"

func newRealtimeService(
	invalidator apprealtime.AuthenticationInvalidator,
	diagnostics realtimeDiagnostics,
) (*realtimeService, error) {
	delivery, err := apprealtime.New(invalidator, diagnostics)
	if err != nil {
		return nil, err
	}
	return newRealtimeServiceWithDelivery(delivery, diagnostics)
}
