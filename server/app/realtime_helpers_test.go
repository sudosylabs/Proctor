// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import "github.com/sudosylabs/proctor/server/app/realtime"

func newRealtimeService(
	invalidator realtime.AuthenticationInvalidator,
	diagnostics realtimeDiagnostics,
) (*realtimeService, error) {
	delivery, err := realtime.New(invalidator, diagnostics)
	if err != nil {
		return nil, err
	}
	return newRealtimeServiceWithDelivery(delivery, diagnostics)
}
