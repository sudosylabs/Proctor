// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type invitationMaintenanceStoreFake struct {
	results []*store.InvitationMaintenanceResult
	calls   int
	state   model.OnboardingImportState
}

func (f *invitationMaintenanceStoreFake) ListExpiredOnboardingImports(context.Context, int, time.Time) ([]model.OnboardingImportID, error) {
	return nil, nil
}
func (f *invitationMaintenanceStoreFake) GetOnboardingImport(context.Context, model.OnboardingImportID) (*store.OnboardingImport, error) {
	state := f.state
	if state == "" {
		state = model.OnboardingImportParsing
	}
	return &store.OnboardingImport{State: state}, nil
}
func (f *invitationMaintenanceStoreFake) PurgeOnboardingImport(context.Context, model.OnboardingImportID, time.Time) (bool, error) {
	return true, nil
}

type onboardingImportFilesMaintenanceFake struct {
	staged  []model.OnboardingImportID
	removed []model.OnboardingImportID
}

func (*onboardingImportFilesMaintenanceFake) StageOnboardingImport(context.Context, model.OnboardingImportID, io.Reader, int64) (string, int64, error) {
	return "", 0, nil
}
func (*onboardingImportFilesMaintenanceFake) IsOnboardingImportTooLarge(error) bool { return false }
func (*onboardingImportFilesMaintenanceFake) OpenOnboardingImport(context.Context, model.OnboardingImportID) (io.ReadCloser, error) {
	return nil, nil
}
func (f *onboardingImportFilesMaintenanceFake) RemoveOnboardingImport(_ context.Context, id model.OnboardingImportID) error {
	f.removed = append(f.removed, id)
	return nil
}
func (f *onboardingImportFilesMaintenanceFake) ListOnboardingImportFiles(context.Context, string, int, time.Time) ([]model.OnboardingImportID, string, error) {
	return f.staged, "", nil
}

func (f *invitationMaintenanceStoreFake) Maintain(context.Context, int) (*store.InvitationMaintenanceResult, error) {
	result := f.results[f.calls]
	f.calls++
	return result, nil
}

func TestInvitationMaintenanceJobConsumesBoundedPages(t *testing.T) {
	at := model.TimeFromMillis(1_800_000_000_000)
	command, _ := json.Marshal(InvitationMaintenanceCommandV1{PageSize: 25, MaxPages: 3})
	job, _ := model.NewJob(model.NewJobID(), model.JobTypeInvitationMaintenance, 1, command, "maintenance", at, at, 5)
	persistence := &invitationMaintenanceStoreFake{results: []*store.InvitationMaintenanceResult{{Expired: 25, More: true}, {Expired: 2, Purged: 3}}}
	handler := invitationMaintenanceHandler{invitations: persistence, imports: persistence, content: &onboardingImportFilesMaintenanceFake{}, now: func() time.Time { return at }}
	outcome := handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || persistence.calls != 2 || !json.Valid(outcome.Result) {
		t.Fatalf("outcome/calls = %#v / %d", outcome, persistence.calls)
	}
	var result InvitationMaintenanceResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || result.Expired != 27 || result.Purged != 3 {
		t.Fatalf("result = %#v, %v", result, err)
	}
	descriptor := invitationMaintenanceDescriptor(handler)
	if descriptor.Concurrency != 1 || descriptor.Timeout > 5*time.Minute || descriptor.Type != model.JobTypeInvitationMaintenance {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestInvitationMaintenanceRemovesSourceForTerminalImport(t *testing.T) {
	t.Parallel()
	at := model.TimeFromMillis(1_800_000_000_000)
	id := model.NewOnboardingImportID()
	command, _ := json.Marshal(InvitationMaintenanceCommandV1{PageSize: 25, MaxPages: 1})
	job, _ := model.NewJob(model.NewJobID(), model.JobTypeInvitationMaintenance, 1, command, "maintenance", at, at, 5)
	persistence := &invitationMaintenanceStoreFake{results: []*store.InvitationMaintenanceResult{{}}, state: model.OnboardingImportPreviewReady}
	files := &onboardingImportFilesMaintenanceFake{staged: []model.OnboardingImportID{id}}
	outcome := (invitationMaintenanceHandler{invitations: persistence, imports: persistence, content: files, now: func() time.Time { return at }}).
		Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(files.removed) != 1 || files.removed[0] != id {
		t.Fatalf("terminal source cleanup = %#v / %v", outcome, files.removed)
	}
}

func TestInvitationMaintenanceProposalIsDurablyDeduplicatedAcrossNodes(t *testing.T) {
	t.Parallel()

	occurrence := time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	first := invitationMaintenanceProposer{jobs: jobs, now: func() time.Time { return occurrence }}
	second := invitationMaintenanceProposer{jobs: jobs, now: func() time.Time { return occurrence.Add(time.Minute) }}
	if err := first.Propose(context.Background(), occurrence); err != nil {
		t.Fatal(err)
	}
	if err := second.Propose(context.Background(), occurrence); err != nil {
		t.Fatal(err)
	}
	if len(jobs.jobs) != 1 {
		t.Fatalf("logical maintenance jobs = %d, want 1", len(jobs.jobs))
	}
	for _, job := range jobs.jobs {
		if job.Type != model.JobTypeInvitationMaintenance || job.DedupePolicy != model.JobDedupePermanent ||
			job.DedupeKey != "invitation-maintenance:2026-08-18" {
			t.Fatalf("maintenance job = %#v", job)
		}
	}
}
