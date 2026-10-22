// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
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
	if options.AfterID != "" {
		for index, user := range f.users {
			if user.ID == options.AfterID {
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
	command, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommand{BatchSize: 2})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, command, "reconcile:2026-08-10", at, at, 5)
	require.NoError(t, err)
	var checkpoints []jobengine.CheckpointValue
	execution := testJobExecution(job, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})

	outcome := handler.Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v proposed=%v checkpoints", outcome, defaults.users)
	require.NoError(t, outcome.Err, "outcome=%#v proposed=%v checkpoints", outcome, defaults.users)
	require.Len(t, defaults.users, 2, "outcome=%#v proposed=%v checkpoints", outcome, defaults.users)
	require.Len(t, checkpoints, 1, "outcome=%#v proposed=%v checkpoints", outcome, defaults.users)
	require.Len(t, lister.calls, 1)
	require.Equal(t, 2, lister.calls[0].Limit)
	require.True(t, lister.calls[0].MissingDefaultProfilePicture)
	require.True(t, lister.calls[0].IncludeDisabled)
	var final DefaultProfilePictureReconciliationCheckpoint
	err = json.Unmarshal(checkpoints[0].Document, &final)
	require.NoError(t, err)
	require.Equal(t, int64(2), final.Processed, "final checkpoint")
	require.Equal(t, users[1].ID, final.AfterUserID, "final checkpoint")
	require.Equal(t, int64(2), checkpoints[0].Progress.Current, "final checkpoint")
	require.Equal(t, int64(2), checkpoints[0].Progress.Total, "final checkpoint")
	require.Equal(t, "completed", checkpoints[0].Progress.Stage, "final checkpoint")
}

func TestDefaultProfilePictureReconciliationDoesNotExceedCommittedBatchOnRetry(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	users := []*model.User{reconciliationUser(t, "alpha", at), reconciliationUser(t, "beta", at), reconciliationUser(t, "gamma", at)}
	lister := &reconciliationUserListerFake{users: users}
	defaults := &reconciliationDefaultJobsFake{}
	handler := defaultProfilePictureReconciliationHandler{users: lister, defaults: defaults, now: func() time.Time { return at }}
	command, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommand{BatchSize: 2})
	require.NoError(t, err)
	checkpoint, err := EncodeDefaultProfilePictureReconciliationCheckpoint(DefaultProfilePictureReconciliationCheckpoint{AfterUsername: users[1].Username, AfterUserID: users[1].ID, Processed: 2})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, command, "reconcile:2026-08-10", at, at, 5)
	require.NoError(t, err)
	job.Checkpoint = checkpoint
	err = job.Validate()
	require.NoError(t, err)
	execution := testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil })

	outcome := handler.Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v proposed=%v calls", outcome, defaults.users)
	require.Empty(t, defaults.users, "outcome=%#v proposed=%v calls", outcome, defaults.users)
	require.Empty(t, lister.calls, "outcome=%#v proposed=%v calls", outcome, defaults.users)
}

func TestDefaultProfilePictureReconciliationDoesNotRepeatReservedWorkAfterCrash(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	lister := &reconciliationUserListerFake{users: []*model.User{reconciliationUser(t, "alpha", at)}}
	defaults := &reconciliationDefaultJobsFake{}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureReconcile, json.RawMessage(`{"batch_size":1}`), "daily", at, at, 5)
	require.NoError(t, err)
	job.WorkReserved = 1
	outcome := (defaultProfilePictureReconciliationHandler{users: lister, defaults: defaults, now: func() time.Time { return at }}).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v calls=%#v proposed", outcome, lister.calls)
	require.Empty(t, lister.calls, "outcome=%#v calls=%#v proposed", outcome, lister.calls)
	require.Empty(t, defaults.users, "outcome=%#v calls=%#v proposed", outcome, lister.calls)
}

func TestDefaultProfilePictureReconciliationCommandsAreBoundedAndStrict(t *testing.T) {
	t.Parallel()

	_, err := EncodeDefaultProfilePictureReconciliationCommand(DefaultProfilePictureReconciliationCommand{BatchSize: 0})
	require.Error(t, err, "accepted zero batch size")
	_, err = DecodeDefaultProfilePictureReconciliationCommand(json.RawMessage(`{"batch_size":10,"unknown":true}`))
	require.Error(t, err, "accepted unknown command field")
	_, err = DecodeDefaultProfilePictureReconciliationCheckpoint(json.RawMessage(`{"processed":-1}`))
	require.Error(t, err, "accepted invalid checkpoint")
}

func reconciliationUser(t *testing.T, username string, at time.Time) *model.User {
	t.Helper()
	user := &model.User{Username: username, Email: username + "@example.test"}
	user.PrepareCreate(model.NewUserID(), at)
	require.NoError(t, user.Validate())
	return user
}
