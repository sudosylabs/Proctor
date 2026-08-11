//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
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
			if err != nil {
				t.Fatal(err)
			}
			if _, err = persistence.User().Create(context.Background(), &store.UserCreation{User: user, DefaultProfilePictureJob: job}); err != nil {
				t.Fatal(err)
			}

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
			pictures := &profilePictureService{
				users: persistence.User(), files: files, content: &pictureContentFake{}, now: time.Now,
			}
			realHandler := defaultProfilePictureHandler{generator: pictures}
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
			if err != nil {
				t.Fatal(err)
			}
			if err = nodeA.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if phase == "during_commit" {
				select {
				case <-publishEntered:
				case <-time.After(time.Second):
					t.Fatal("first worker did not enter the publication transaction")
				}
			} else {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("first worker did not reach the requested loss point")
				}
			}
			closedA := make(chan error, 1)
			go func() { closedA <- nodeA.Close() }()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("first worker did not observe node shutdown")
			}
			if phase == "during_commit" {
				close(continuePublish)
			}
			if err = <-closedA; err != nil {
				t.Fatalf("node-a Close() = %v", err)
			}
			afterShutdown, err := persistence.Job().Get(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if afterShutdown.Status != model.JobStatusRunning {
				t.Fatalf("job after shutdown = %#v", afterShutdown)
			}

			time.Sleep(3 * descriptor.LeaseDuration)
			nodeB, err := jobengine.New(jobengine.Config{
				Store: persistence.Job(), Descriptors: []jobengine.Descriptor{descriptor}, NodeID: "node-b",
				Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = nodeB.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				current, getErr := persistence.Job().Get(context.Background(), job.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if current.Status == model.JobStatusSucceeded {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("recovered job did not succeed: %#v", current)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err = nodeB.Close(); err != nil {
				t.Fatalf("node-b Close() = %v", err)
			}
			persistedUser, err := persistence.User().Get(context.Background(), user.ID.String())
			if err != nil {
				t.Fatal(err)
			}
			if persistedUser.DefaultProfilePictureFileID.IsZero() {
				t.Fatal("recovered handler did not durably attach the default picture")
			}
			var availableDefaults int
			if err = persistence.GetMaster().Get(context.Background(), &availableDefaults, `
				SELECT count(*)
				FROM file_entries fe
				JOIN file_revisions fr ON fr.id = fe.current_revision_id
				WHERE fe.purpose = 'profile_picture_default'
				  AND fe.id = $1
				  AND fr.availability = 'available'
			`, persistedUser.DefaultProfilePictureFileID.String()); err != nil {
				t.Fatal(err)
			}
			if availableDefaults != 1 {
				t.Fatalf("available default publications = %d, want 1", availableDefaults)
			}
			if handler.calls.Load() != 2 {
				t.Fatalf("handler calls = %d, want 2", handler.calls.Load())
			}
			attempts, err := persistence.Job().ListAttempts(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 2 || attempts[0].Status != model.JobAttemptStatusLeaseExpired || attempts[1].Status != model.JobAttemptStatusSucceeded {
				t.Fatalf("attempt history = %#v", attempts)
			}
			if _, err = persistence.Job().Complete(context.Background(), &store.JobCompletion{
				AttemptID: attempts[0].ID, ClaimToken: attempts[0].ClaimToken,
				Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`),
			}); !store.IsConflict(err) {
				t.Fatalf("stale worker completion error = %v", err)
			}
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
	next     JobHandler
	entered  chan struct{}
	canceled chan struct{}
	calls    atomic.Int64
}

func (h *recoveryCommitHandler) Run(ctx context.Context, execution JobExecution) JobOutcome {
	call := h.calls.Add(1)
	if call != 1 {
		return h.next.Run(ctx, execution)
	}
	switch h.phase {
	case "before_commit":
		close(h.entered)
		<-ctx.Done()
		close(h.canceled)
		return JobRetryableFailure("dependency.unavailable", ctx.Err())
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

func clusteredRecoveryJobDescriptor(handler JobHandler) JobDescriptor {
	return JobDescriptor{
		Type: model.JobTypeProfilePictureGenerateDefault, CommandVersions: []int{1}, ResultVersions: []int{1},
		PublicErrorCodes: []string{"dependency.unavailable"}, Timeout: 2 * time.Second, Concurrency: 1,
		MaximumAttempts: 3, LeaseDuration: 250 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond,
		BaseRetryDelay: time.Millisecond, MaximumRetryDelay: 10 * time.Millisecond,
		Visibility: JobVisibilityOperator, SuccessRetention: 24 * time.Hour, FailureRetention: 48 * time.Hour,
		Handler: handler,
	}
}

func openClusteredJobIntegrationStore(t *testing.T) *sqlstore.SQLStore {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err = migrator.Close(); err != nil {
		t.Fatal(err)
	}
	persistence, err := sqlstore.New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := persistence.Close(); err != nil {
			t.Errorf("close clustered Job integration store: %v", err)
		}
	})
	if _, err = persistence.GetMaster().Exec(context.Background(), `TRUNCATE TABLE users, file_entries, job_permanent_occurrences, jobs CASCADE`); err != nil {
		t.Fatal(err)
	}
	return persistence
}
