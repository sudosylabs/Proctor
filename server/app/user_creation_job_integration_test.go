//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"os"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestLocalUserCreationCommitsDefaultPictureIntent(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username: "default-intent-user", Email: "default-intent-user@example.edu",
	}, "correct horse battery staple")
	if appErr != nil {
		t.Fatal(appErr)
	}
	jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{
		Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, Limit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.DedupeKey == user.ID.String() {
			return
		}
	}
	t.Fatalf("local user creation did not commit default-picture intent for %s", user.ID)
}
