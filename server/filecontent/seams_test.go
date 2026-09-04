// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent_test

import (
	"github.com/sudosylabs/proctor/server/app"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/filecontent"
)

var (
	_ app.ProfilePictureUploadFiles            = (*filecontent.Content)(nil)
	_ app.ProfilePictureReadFiles              = (*filecontent.Content)(nil)
	_ app.DefaultProfilePictureRenderFiles     = (*filecontent.Content)(nil)
	_ app.DefaultProfilePictureGenerationFiles = (*filecontent.Content)(nil)
	_ appjobs.FileRevisionContentPurger        = (*filecontent.Content)(nil)
)
