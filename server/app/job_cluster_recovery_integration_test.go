//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestJobRunnerRecoversNodeLossAroundADurableDomainCommit(t *testing.T) {
	// This is module-level PostgreSQL recovery evidence, not an alternate
	// production composition path. Root wiring is covered separately through
	// testlib; direct Engine construction is required here to control the exact
	// loss point around the domain commit.
	for _, phase := range []string{"before_commit", "during_commit", "after_commit"} {
		t.Run(phase, func(t *testing.T) {
			persistence := openClusteredJobIntegrationStore(t)
			at := model.NowUTC().Add(-time.Second)
			user, job, err := prepareUserDefaultProfilePictureJob(&model.User{
				Username: "recovery-" + phase,
				Email:    "recovery-" + phase + "@example.test",
			}, at)
			require.NoError(t, err)
			settings, settingsErr := prepareInitialUserSettingsDocument(user)
			require.NoError(t, settingsErr)
			_, err = persistence.User().Create(context.Background(), &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: job})
			require.NoError(t, err)

			publishEntered := make(chan struct{})
			continuePublish := make(chan struct{})
			if phase != "during_commit" {
				close(continuePublish)
			}
			files := &recoveryFileStore{
				profilePictureFileStore: persistence.File(),
				publishEntered:          publishEntered,
				continuePublish:         continuePublish,
			}
			pictures := newProfilePictureServiceForTest(
				persistence.User(), files, &pictureContentFake{},
				nil, nil, nil, nil, nil, time.Now,
			)
			realHandler := jobhandler.NewDefaultProfilePictureDescriptor(
				jobDefaultProfilePictureGenerator{service: pictures},
			).Handler
			entered := make(chan struct{})
			canceled := make(chan struct{})
			handler := &recoveryCommitHandler{
				phase: phase, next: realHandler, entered: entered, canceled: canceled,
			}
			descriptor := clusteredRecoveryJobDescriptor(handler)
			nodeA, err := jobengine.New(jobengine.Config{
				Store: persistence.Job(), Descriptors: []jobengine.Descriptor{descriptor}, NodeID: "node-a",
				Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond},
			})
			require.NoError(t, err)
			err = nodeA.Start(context.Background())
			require.NoError(t, err)
			if phase == "during_commit" {
				select {
				case <-publishEntered:
				case <-time.After(time.Second):
					require.FailNow(t, "first worker did not enter the publication transaction")
				}
			} else {
				select {
				case <-entered:
				case <-time.After(time.Second):
					require.FailNow(t, "first worker did not reach the requested loss point")
				}
			}
			closedA := make(chan error, 1)
			go func() { closedA <- nodeA.Close() }()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				require.FailNow(t, "first worker did not observe node shutdown")
			}
			if phase == "during_commit" {
				close(continuePublish)
			}
			err = <-closedA
			require.NoError(t, err)
			afterShutdown, err := persistence.Job().Get(context.Background(), job.ID)
			require.NoError(t, err)
			require.Equal(t, model.JobStatusRunning, afterShutdown.Status)

			time.Sleep(3 * descriptor.LeaseDuration)
			nodeB, err := jobengine.New(jobengine.Config{
				Store: persistence.Job(), Descriptors: []jobengine.Descriptor{descriptor}, NodeID: "node-b",
				Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond},
			})
			require.NoError(t, err)
			err = nodeB.Start(context.Background())
			require.NoError(t, err)
			deadline := time.Now().Add(2 * time.Second)
			for {
				current, getErr := persistence.Job().Get(context.Background(), job.ID)
				require.NoError(t, getErr)
				if current.Status == model.JobStatusSucceeded {
					break
				}
				require.False(t, time.Now().After(deadline), "recovered job did not succeed: %#v", current)
				time.Sleep(5 * time.Millisecond)
			}
			err = nodeB.Close()
			require.NoError(t, err)
			persistedUser, err := persistence.User().Get(context.Background(), user.ID)
			require.NoError(t, err)
			require.False(t, persistedUser.DefaultProfilePictureFileID.IsZero(), "recovered handler did not durably attach the default picture")
			var availableDefaults int
			err = persistence.GetMaster().Get(context.Background(), &availableDefaults, `
				SELECT count(*)
				FROM file_entries fe
				JOIN file_revisions fr ON fr.id = fe.current_revision_id
				WHERE fe.purpose = 'profile_picture_default'
				  AND fe.id = $1
				  AND fr.availability = 'available'
			`, persistedUser.DefaultProfilePictureFileID.String())
			require.NoError(t, err)
			require.Equal(t, 1, availableDefaults, "available default publications")
			require.Equal(t, int64(2), handler.calls.Load(), "handler calls")
			attempts, err := persistence.Job().ListAttempts(context.Background(), job.ID)
			require.NoError(t, err)
			require.Len(t, attempts, 2)
			require.Equal(t, model.JobAttemptStatusLeaseExpired, attempts[0].Status)
			require.Equal(t, model.JobAttemptStatusSucceeded, attempts[1].Status)
			_, err = persistence.Job().Complete(context.Background(), &store.JobCompletion{
				AttemptID: attempts[0].ID, ClaimToken: attempts[0].ClaimToken,
				Kind: store.JobCompletionSucceeded, Result: json.RawMessage(`{}`),
			})
			require.True(t, store.IsConflict(err), "stale worker completion error")
		})
	}
}

type integrationJobDiagnostics struct{}

func (*integrationJobDiagnostics) ErrorContext(context.Context, string, error) {}

type recoveryFileStore struct {
	profilePictureFileStore
	publishEntered  chan struct{}
	continuePublish chan struct{}
	once            sync.Once
}

func (s *recoveryFileStore) PublishDefaultProfilePicture(ctx context.Context, input *store.DefaultProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	s.once.Do(func() {
		close(s.publishEntered)
		<-s.continuePublish
	})
	return s.profilePictureFileStore.PublishDefaultProfilePicture(ctx, input)
}

type recoveryCommitHandler struct {
	phase    string
	next     jobengine.Handler
	entered  chan struct{}
	canceled chan struct{}
	calls    atomic.Int64
}

func (h *recoveryCommitHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	call := h.calls.Add(1)
	if call != 1 {
		return h.next.Run(ctx, execution)
	}
	switch h.phase {
	case "before_commit":
		close(h.entered)
		<-ctx.Done()
		close(h.canceled)
		return jobengine.RetryableFailure("dependency.unavailable", ctx.Err())
	case "during_commit":
		go func() {
			<-ctx.Done()
			close(h.canceled)
		}()
		return h.next.Run(ctx, execution)
	case "after_commit":
		outcome := h.next.Run(ctx, execution)
		close(h.entered)
		<-ctx.Done()
		close(h.canceled)
		return outcome
	default:
		panic("unknown recovery phase")
	}
}

func clusteredRecoveryJobDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{
		Type: model.JobTypeProfilePictureGenerateDefault, PublicErrorCodes: []string{"dependency.unavailable"}, Timeout: 2 * time.Second, Concurrency: 1,
		MaximumAttempts: 3, LeaseDuration: 250 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond,
		BaseRetryDelay: time.Millisecond, MaximumRetryDelay: 10 * time.Millisecond,
		Visibility: jobengine.VisibilityOperator, SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour,
		Handler: handler,
	}
}

func openClusteredJobIntegrationStore(t *testing.T) *sqlstore.SQLStore {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	require.NoError(t, err)
	if err = migrator.Up(); err != nil {
		_ = migrator.Close()
		require.NoError(t, err)
	}
	err = migrator.Close()
	require.NoError(t, err)
	persistence, err := sqlstore.New(context.Background(), settings)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, persistence.Close())
	})
	_, err = persistence.GetMaster().Exec(context.Background(), `TRUNCATE TABLE
		mail_key_state, mail_fanout_bundles, mail_payload_keys, mail_deliveries, mail_occurrences,
		audit_events, institutions, users, file_entries, job_permanent_occurrences, jobs CASCADE`)
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(context.Background(), `INSERT INTO mail_key_state(
		singleton, required_primary_key_id, active_rekey_job_id, updated_at
	) VALUES (TRUE, NULL, NULL, clock_timestamp())`)
	require.NoError(t, err)
	return persistence
}
