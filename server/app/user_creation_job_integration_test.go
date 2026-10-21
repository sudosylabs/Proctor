//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestLocalUserCreationCommitsDefaultPictureIntent(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username: "default-intent-user", Email: "default-intent-user@example.edu",
	}, Password: "correct horse battery staple"})
	require.NoError(t, appErr)
	settings, err := persistence.UserSettings().Get(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, model.UserSettingsInitialSource, settings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
	require.Equal(t, user.ID, settings.UserID)
	require.True(t, settings.Revision.IsValid())
	jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{
		Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, Limit: 200,
	})
	require.NoError(t, err)
	for _, job := range jobs {
		if job.DedupeKey == user.ID.String() {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("local user creation did not commit default-picture intent for %s", user.ID))
}
