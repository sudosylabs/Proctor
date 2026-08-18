// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const invitationMaintenancePageSize = 500
const invitationMaintenanceMaximumPages = 20

type InvitationMaintenanceCommandV1 struct {
	PageSize int `json:"page_size"`
	MaxPages int `json:"max_pages"`
}
type InvitationMaintenanceResultV1 struct {
	Expired int `json:"expired"`
	Purged  int `json:"purged"`
}

type invitationMaintenanceStore interface {
	Maintain(context.Context, int) (*store.InvitationMaintenanceResult, error)
}
type invitationMaintenanceHandler struct{ invitations invitationMaintenanceStore }

func (h invitationMaintenanceHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || execution.Job.CommandVersion != 1 || h.invitations == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Invitation maintenance job is invalid"))
	}
	var command InvitationMaintenanceCommandV1
	if decodeStrictJobDocument(execution.Job.Command, &command) != nil || command.PageSize < 1 || command.PageSize > invitationMaintenancePageSize || command.MaxPages < 1 || command.MaxPages > invitationMaintenanceMaximumPages {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Invitation maintenance command is invalid"))
	}
	result := InvitationMaintenanceResultV1{}
	for page := 0; page < command.MaxPages; page++ {
		maintained, err := h.invitations.Maintain(ctx, command.PageSize)
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.Expired += maintained.Expired
		result.Purged += maintained.Purged
		if !maintained.More {
			break
		}
	}
	document, err := json.Marshal(result)
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type invitationMaintenanceProposer struct {
	jobs jobEnqueuer
	now  func() time.Time
}

func (p invitationMaintenanceProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("Invitation maintenance proposer dependencies are unavailable")
	}
	command, err := json.Marshal(InvitationMaintenanceCommandV1{PageSize: invitationMaintenancePageSize, MaxPages: invitationMaintenanceMaximumPages})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeInvitationMaintenance, 1, command,
		"invitation-maintenance:"+model.TimeUTC(occurrence).Format("2006-01-02"), model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func invitationMaintenanceDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeInvitationMaintenance, CommandVersions: []int{1}, ResultVersions: []int{1},
		PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute, Concurrency: 1,
		MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute,
		MaximumRetryDelay: 30 * time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour,
		FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
