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
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const invitationMaintenancePageSize = 500
const invitationMaintenanceMaximumPages = 20
const onboardingImportOrphanSafetyWindow = time.Hour

// InvitationMaintenanceCommand is the immutable command of the invitation maintenance Job.
type InvitationMaintenanceCommand struct {
	PageSize int `json:"page_size"`
	MaxPages int `json:"max_pages"`
}

// InvitationMaintenanceResult is the safe result of the invitation maintenance Job.
type InvitationMaintenanceResult struct {
	Expired          int `json:"expired"`
	Purged           int `json:"purged"`
	OnboardingPurged int `json:"onboarding_purged"`
}

type InvitationMaintenanceStore interface {
	Maintain(context.Context, int) (*store.InvitationMaintenanceResult, error)
}
type OnboardingImportMaintenanceStore interface {
	GetOnboardingImport(context.Context, model.OnboardingImportID) (*store.OnboardingImport, error)
	ListExpiredOnboardingImports(context.Context, int, time.Time) ([]model.OnboardingImportID, error)
	PurgeOnboardingImport(context.Context, *store.OnboardingImportPurge) (bool, error)
}
type OnboardingImportMaintenanceFiles interface {
	RemoveOnboardingImport(context.Context, model.OnboardingImportID) error
	ListOnboardingImportFiles(context.Context, string, int, time.Time) ([]model.OnboardingImportID, string, error)
}
type invitationMaintenanceHandler struct {
	invitations InvitationMaintenanceStore
	imports     OnboardingImportMaintenanceStore
	content     OnboardingImportMaintenanceFiles
	now         func() time.Time
}

func (h invitationMaintenanceHandler) Run(ctx context.Context, execution job.Execution) job.Outcome {
	if execution.Job == nil || h.invitations == nil || h.imports == nil || h.content == nil || h.now == nil {
		return job.PermanentFailure("job.command.invalid", errors.New("Invitation maintenance job is invalid"))
	}
	var command InvitationMaintenanceCommand
	if decodeStrictJobDocument(execution.Job.Command, &command) != nil || command.PageSize < 1 || command.PageSize > invitationMaintenancePageSize || command.MaxPages < 1 || command.MaxPages > invitationMaintenanceMaximumPages {
		return job.PermanentFailure("job.command.invalid", errors.New("Invitation maintenance command is invalid"))
	}
	result := InvitationMaintenanceResult{}
	fileCursor := ""
	for page := 0; page < command.MaxPages; page++ {
		maintained, err := h.invitations.Maintain(ctx, command.PageSize)
		if err != nil {
			return job.RetryableFailure("dependency.unavailable", err)
		}
		result.Expired += maintained.Expired
		result.Purged += maintained.Purged
		at := model.TimeUTC(h.now())
		expiredImports, err := h.imports.ListExpiredOnboardingImports(ctx, command.PageSize, at)
		if err != nil {
			return job.RetryableFailure("dependency.unavailable", err)
		}
		for _, id := range expiredImports {
			if err = h.content.RemoveOnboardingImport(ctx, id); err != nil {
				return job.RetryableFailure("dependency.unavailable", err)
			}
			purged, purgeErr := h.imports.PurgeOnboardingImport(ctx, &store.OnboardingImportPurge{ID: id, At: at})
			if purgeErr != nil {
				return job.RetryableFailure("dependency.unavailable", purgeErr)
			}
			if purged {
				result.OnboardingPurged++
			}
		}
		staged, nextCursor, err := h.content.ListOnboardingImportFiles(ctx, fileCursor, command.PageSize, at.Add(-onboardingImportOrphanSafetyWindow))
		if err != nil {
			return job.RetryableFailure("dependency.unavailable", err)
		}
		for _, id := range staged {
			current, getErr := h.imports.GetOnboardingImport(ctx, id)
			if getErr != nil && !store.IsNotFound(getErr) {
				return job.RetryableFailure("dependency.unavailable", getErr)
			}
			if store.IsNotFound(getErr) || (current != nil && current.State != model.OnboardingImportUploading && current.State != model.OnboardingImportParsing) {
				if removeErr := h.content.RemoveOnboardingImport(ctx, id); removeErr != nil {
					return job.RetryableFailure("dependency.unavailable", removeErr)
				}
			}
		}
		fileCursor = nextCursor
		if !maintained.More && len(expiredImports) < command.PageSize && fileCursor == "" {
			break
		}
	}
	document, err := json.Marshal(result)
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: document, Err: err}
}

type invitationMaintenanceProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p invitationMaintenanceProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("Invitation maintenance proposer dependencies are unavailable")
	}
	command, err := json.Marshal(InvitationMaintenanceCommand{PageSize: invitationMaintenancePageSize, MaxPages: invitationMaintenanceMaximumPages})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeInvitationMaintenance, command,
		"invitation-maintenance:"+model.TimeUTC(occurrence).Format("2006-01-02"), model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func invitationMaintenanceDescriptor(handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: model.JobTypeInvitationMaintenance, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute, Concurrency: 1,
		MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute,
		MaximumRetryDelay: 30 * time.Minute, Visibility: job.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour,
		FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
