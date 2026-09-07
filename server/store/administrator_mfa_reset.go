// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import "github.com/sudosylabs/proctor/server/model"

// AdministratorMFAReset is accepted only by the explicit offline host command.
// The exact installation and active administrator are confirmed under the
// serving-lease and protected administrator fences. Deployment capabilities
// confirm that reenrollment and an existing primary authentication path remain
// available; this operation never adds or changes a primary credential.
type AdministratorMFAReset struct {
	InstitutionID model.InstitutionID
	UserID        model.UserID
	MFAEnabled    bool
	Capabilities  AccessDeploymentCapabilities
}

type AdministratorMFAResetResult struct {
	RecordID string
	Recovery *model.UserMFARecovery
}
