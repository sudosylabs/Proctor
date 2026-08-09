// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type reconciliationUserListerFake struct {
	users []*model.User
	calls []store.UserListOptions
}

func (f *reconciliationUserListerFake) List(_ context.Context, options store.UserListOptions) ([]*model.User, error) {
	f.calls = append(f.calls, options)
	start := 0
	if options.AfterId != "" {
		for index, user := range f.users {
			if user.ID.String() == options.AfterId {
				start = index + 1
				break
			}
		}
	}
	end := min(len(f.users), start+options.Limit)
	return append([]*model.User(nil), f.users[start:end]...), nil
}

type reconciliationDefaultJobsFake struct{ users []model.UserID }

func (f *reconciliationDefaultJobsFake) ProposeDefaultProfilePicture(_ context.Context, userID model.UserID, _ time.Time) error {
	f.users = append(f.users, userID)
	return nil
}

func TestDefaultProfilePictureReconciliationUsesBoundedPagesAndSafeCheckpoints(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	users := []*model.User{reconciliationUser(t, "alpha", at), reconciliationUser(t, "beta", at), reconciliationUser(t, "gamma", at)}
	lister := &reconciliationUserListerFake{users: users}
	defaults := &reconciliationDefaultJobsFake{}
	handler := defaultProfilePictureReconciliationHandler{users: lister, defaults: defaults, now: func() time.Time { return at }}
	command, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommandV1{BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, 1, command, "reconcile:2026-08-10", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []JobCheckpointValue
	execution := JobExecution{Job: job, reserveWork: allowJobWorkReservation(), checkpoint: func(_ context.Context, value JobCheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}}

	outcome := handler.Run(context.Background(), execution)
	if outcome.Kind != JobOutcomeSucceeded || outcome.Err != nil || len(defaults.users) != 2 || len(checkpoints) != 1 {
		t.Fatalf("outcome=%#v proposed=%v checkpoints=%#v", outcome, defaults.users, checkpoints)
	}
	if len(lister.calls) != 1 || lister.calls[0].Limit != 2 || !lister.calls[0].MissingDefaultProfilePicture || !lister.calls[0].IncludeDisabled {
		t.Fatalf("list calls = %#v", lister.calls)
	}
	var final DefaultProfilePictureReconciliationCheckpointV1
	if err = json.Unmarshal(checkpoints[0].Document, &final); err != nil {
		t.Fatal(err)
	}
	if final.Processed != 2 || final.AfterUserID != users[1].ID || checkpoints[0].Progress.Current != 2 || checkpoints[0].Progress.Total != 2 || checkpoints[0].Progress.Stage != "completed" {
		t.Fatalf("final checkpoint = %#v progress=%#v", final, checkpoints[0].Progress)
	}
}

func TestDefaultProfilePictureReconciliationDoesNotExceedCommittedBatchOnRetry(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	users := []*model.User{reconciliationUser(t, "alpha", at), reconciliationUser(t, "beta", at), reconciliationUser(t, "gamma", at)}
	lister := &reconciliationUserListerFake{users: users}
	defaults := &reconciliationDefaultJobsFake{}
	handler := defaultProfilePictureReconciliationHandler{users: lister, defaults: defaults, now: func() time.Time { return at }}
	command, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommandV1{BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := EncodeDefaultProfilePictureReconciliationCheckpoint(DefaultProfilePictureReconciliationCheckpointV1{AfterUsername: users[1].Username, AfterUserID: users[1].ID, Processed: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, 1, command, "reconcile:2026-08-10", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	job.CheckpointVersion = 1
	job.Checkpoint = checkpoint
	if err = job.Validate(); err != nil {
		t.Fatal(err)
	}
	execution := JobExecution{Job: job, reserveWork: allowJobWorkReservation(), checkpoint: func(context.Context, JobCheckpointValue) error { return nil }}

	outcome := handler.Run(context.Background(), execution)
	if outcome.Kind != JobOutcomeSucceeded || len(defaults.users) != 0 || len(lister.calls) != 0 {
		t.Fatalf("outcome=%#v proposed=%v calls=%#v", outcome, defaults.users, lister.calls)
	}
}

func TestDefaultProfilePictureReconciliationDoesNotRepeatReservedWorkAfterCrash(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	lister := &reconciliationUserListerFake{users: []*model.User{reconciliationUser(t, "alpha", at)}}
	defaults := &reconciliationDefaultJobsFake{}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, 1, json.RawMessage(`{"batch_size":1}`), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	job.WorkReserved = 1
	outcome := (defaultProfilePictureReconciliationHandler{users: lister, defaults: defaults, now: func() time.Time { return at }}).Run(context.Background(), JobExecution{Job: job, reserveWork: allowJobWorkReservation()})
	if outcome.Kind != JobOutcomeSucceeded || len(lister.calls) != 0 || len(defaults.users) != 0 {
		t.Fatalf("outcome=%#v calls=%#v proposed=%#v", outcome, lister.calls, defaults.users)
	}
}

func TestDefaultProfilePictureReconciliationCommandsAreBoundedAndStrict(t *testing.T) {
	t.Parallel()

	if _, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommandV1{BatchSize: 0}); err == nil {
		t.Fatal("accepted zero batch size")
	}
	if _, err := DecodeDefaultProfilePictureReconciliationCommand(1, json.RawMessage(`{"batch_size":10,"unknown":true}`)); err == nil {
		t.Fatal("accepted unknown command field")
	}
	if _, err := DecodeDefaultProfilePictureReconciliationCheckpoint(1, json.RawMessage(`{"processed":-1}`)); err == nil {
		t.Fatal("accepted invalid checkpoint")
	}
}

func reconciliationUser(t *testing.T, username string, at time.Time) *model.User {
	t.Helper()
	user := &model.User{Username: username, Email: username + "@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	if err := user.Validate(); err != nil {
		t.Fatal(err)
	}
	return user
}
