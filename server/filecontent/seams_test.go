// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent_test

import (
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/filecontent"
)

var (
	_ app.ProfilePictureUploadFiles            = (*filecontent.Content)(nil)
	_ app.ProfilePictureReadFiles              = (*filecontent.Content)(nil)
	_ app.DefaultProfilePictureRenderFiles     = (*filecontent.Content)(nil)
	_ app.DefaultProfilePictureGenerationFiles = (*filecontent.Content)(nil)
	_ app.FileRevisionContentPurger            = (*filecontent.Content)(nil)
)
