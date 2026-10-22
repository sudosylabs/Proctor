// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

func (s *externalAuthenticationService) begin(
	ctx context.Context,
	providerID string,
	returnTo string,
	clientType model.SessionClientType,
	deviceID string,
	deviceName string,
	source string,
) (*model.ExternalAuthenticationStart, error) {
	return s.beginWithInvitationClaim(ctx, providerID, returnTo, clientType, deviceID, deviceName, source, "")
}
