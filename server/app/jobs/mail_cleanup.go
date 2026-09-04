// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailCleanupPageSize     = 500
	mailCleanupMaximumPages = 20
)

const MailCleanupPageSize = mailCleanupPageSize
const MailCleanupMaximumPages = mailCleanupMaximumPages

type MailCleanupCommandV1 struct {
	PageSize int `json:"page_size"`
	MaxPages int `json:"max_pages"`
}

type MailCleanupResultV1 struct {
	FanoutsTerminalized        int `json:"fanouts_terminalized"`
	FanoutDeliveriesSuppressed int `json:"fanout_deliveries_suppressed"`
	Expired                    int `json:"expired"`
	Deleted                    int `json:"deleted"`
}

type MailCleanupStore interface {
	SuppressExpired(context.Context, int) (*store.MailMaintenanceResult, error)
	CleanupTerminal(context.Context, int) (*store.MailMaintenanceResult, error)
}

type SittingMailMaintenanceStore interface {
	MaintainMailExpansions(context.Context, int) (*store.ExamSittingMailMaintenanceResult, error)
}

type mailCleanupHandler struct {
	mail     MailCleanupStore
	sittings SittingMailMaintenanceStore
	recorder MailDeliveryRecorder
}

func (h mailCleanupHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || execution.Job.CommandVersion != 1 {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("mail cleanup job is invalid"))
	}
	var command MailCleanupCommandV1
	if decodeStrictJobDocument(execution.Job.Command, &command) != nil || command.PageSize < 1 || command.PageSize > mailCleanupPageSize || command.MaxPages < 1 || command.MaxPages > mailCleanupMaximumPages || h.mail == nil || h.sittings == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("mail cleanup command is invalid"))
	}
	result := MailCleanupResultV1{}
	for page := 0; page < command.MaxPages; page++ {
		terminalized, err := h.sittings.MaintainMailExpansions(ctx, command.PageSize)
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.FanoutsTerminalized += terminalized.FanoutsTerminalized
		result.FanoutDeliveriesSuppressed += terminalized.DeliveriesSuppressed
		if !terminalized.More {
			break
		}
	}
	for page := 0; page < command.MaxPages; page++ {
		deleted, err := h.mail.CleanupTerminal(ctx, command.PageSize)
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.Deleted += deleted.Affected
		if !deleted.More {
			break
		}
	}
	for page := 0; page < command.MaxPages; page++ {
		expired, err := h.mail.SuppressExpired(ctx, command.PageSize)
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		result.Expired += expired.Affected
		recordMailMaintenanceDeliveries(ctx, h.recorder, expired)
		if !expired.More {
			break
		}
	}
	document, err := json.Marshal(result)
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func recordMailMaintenanceDeliveries(ctx context.Context, recorder MailDeliveryRecorder, result *store.MailMaintenanceResult) {
	if recorder == nil || result == nil {
		return
	}
	for _, delivery := range result.Deliveries {
		recorder.RecordJobMailDelivery(ctx, MailDeliveryMetric{
			TemplateKey: delivery.TemplateKey, State: delivery.State, OutcomeCode: delivery.PublicFailureCode,
			ProcessingLatency: delivery.ProcessingLatency,
		})
	}
}

type mailCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p mailCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if p.jobs == nil || p.now == nil {
		return errors.New("mail cleanup proposer dependencies are unavailable")
	}
	command, err := json.Marshal(MailCleanupCommandV1{PageSize: mailCleanupPageSize, MaxPages: mailCleanupMaximumPages})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeMailCleanup, 1, command,
		"mail-cleanup:"+model.TimeUTC(occurrence).Format("2006-01-02"), model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func mailCleanupDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeMailCleanup, CommandVersions: []int{1}, ResultVersions: []int{1},
		PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute,
		Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second,
		BaseRetryDelay: time.Minute, MaximumRetryDelay: 30 * time.Minute, Visibility: jobengine.VisibilityOperator,
		SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}

func NewMailCleanupDescriptor(mail MailCleanupStore, sittings SittingMailMaintenanceStore, recorder MailDeliveryRecorder) jobengine.Descriptor {
	return mailCleanupDescriptor(mailCleanupHandler{mail: mail, sittings: sittings, recorder: recorder})
}
