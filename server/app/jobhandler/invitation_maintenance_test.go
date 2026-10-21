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
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome/calls = %#v", outcome)
	require.Equal(t, 2, persistence.calls, "outcome/calls = %#v", outcome)
	require.True(t, json.Valid(outcome.Result), "outcome/calls = %#v", outcome)
	var result InvitationMaintenanceResultV1
	require.NoError(t, json.Unmarshal(outcome.Result, &result), "result = %#v", result)
	require.Equal(t, 27, result.Expired, "result = %#v", result)
	require.Equal(t, 3, result.Purged, "result = %#v", result)
	descriptor := invitationMaintenanceDescriptor(handler)
	require.Equal(t, 1, descriptor.Concurrency)
	require.LessOrEqual(t, descriptor.Timeout, 5*time.Minute)
	require.Equal(t, model.JobTypeInvitationMaintenance, descriptor.Type)
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
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "terminal source cleanup = %#v", outcome)
	require.Len(t, files.removed, 1, "terminal source cleanup = %#v", outcome)
	require.Equal(t, id, files.removed[0], "terminal source cleanup = %#v", outcome)
}

func TestInvitationMaintenanceProposalIsDurablyDeduplicatedAcrossNodes(t *testing.T) {
	t.Parallel()

	occurrence := time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	first := invitationMaintenanceProposer{jobs: jobs, now: func() time.Time { return occurrence }}
	second := invitationMaintenanceProposer{jobs: jobs, now: func() time.Time { return occurrence.Add(time.Minute) }}
	require.NoError(t, first.Propose(context.Background(), occurrence))
	require.NoError(t, second.Propose(context.Background(), occurrence))
	require.Len(t, jobs.jobs, 1, "logical maintenance jobs")
	for _, job := range jobs.jobs {
		require.Equal(t, model.JobTypeInvitationMaintenance, job.Type)
		require.Equal(t, model.JobDedupePermanent, job.DedupePolicy)
		require.Equal(t, "invitation-maintenance:2026-08-18", job.DedupeKey)
	}
}
