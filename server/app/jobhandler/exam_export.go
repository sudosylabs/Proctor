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

type ExamExportBuilder interface {
	BuildFromJob(context.Context, store.ExamExportBuild) error
}
type ExamExportMaintenanceStore interface {
	Reconcile(context.Context, int) (int, error)
	BeginPurgeBatch(context.Context, int) ([]store.ExamExportArtifact, error)
	CompletePurge(context.Context, store.ExamExportArtifact) error
}
type ExamExportPurger interface {
	PurgeExamExport(context.Context, model.ExamExportID, model.JobAttemptID) error
}

type examExportBuildHandler struct{ service ExamExportBuilder }
type examExportBuildCommand struct {
	ExportID model.ExamExportID `json:"export_id"`
}

func (h examExportBuildHandler) Run(ctx context.Context, e job.Execution) job.Outcome {
	var command examExportBuildCommand
	if h.service == nil || e.Job == nil || e.Attempt == nil || !e.Job.ID.IsValid() || !e.Attempt.ID.IsValid() || !e.Attempt.ClaimToken.IsValid() || e.Attempt.JobID != e.Job.ID ||
		decodeStrictUniqueJobDocument(e.Job.Command, &command) != nil || !command.ExportID.IsValid() || e.Job.WorkReserved < 0 || e.Job.WorkReserved > model.ExamExportMaximumAttempts {
		return job.PermanentFailure("job.command.invalid", errors.New("invalid Exam export build command"))
	}
	reserved, err := e.ReserveWork(ctx, 1, model.ExamExportMaximumAttempts)
	if err != nil {
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export work reservation failed"))
	}
	if !reserved {
		return job.PermanentFailure("exam.export.unavailable", errors.New("Exam export construction budget exhausted"))
	}
	input := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: command.ExportID, AttemptID: e.Attempt.ID}, JobID: e.Job.ID, ClaimToken: e.Attempt.ClaimToken}
	if err = h.service.BuildFromJob(ctx, input); err != nil {
		// SQL/provider details and archive content never enter ordinary Job logs.
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export construction failed"))
	}
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: json.RawMessage(`{"ready":true}`)}
}

func examExportBuildDescriptor(handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: model.JobTypeExamExportBuild, PublicErrorCodes: []string{"job.command.invalid", "exam.export.unavailable", "exam.export.expired"},
		Timeout: 30 * time.Minute, Concurrency: 1, MaximumAttempts: model.ExamExportMaximumAttempts, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute, MaximumRetryDelay: 5 * time.Minute,
		Visibility: job.VisibilityOperator, SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 7 * 24 * time.Hour, Handler: handler}
}

type examExportCleanupHandler struct {
	exports ExamExportMaintenanceStore
	content ExamExportPurger
}
type examExportCleanupCommand struct {
	BatchSize int `json:"batch_size"`
}

func (h examExportCleanupHandler) Run(ctx context.Context, e job.Execution) job.Outcome {
	var command examExportCleanupCommand
	if h.exports == nil || h.content == nil || e.Job == nil || decodeStrictUniqueJobDocument(e.Job.Command, &command) != nil || command.BatchSize < 1 || command.BatchSize > 100 || e.Job.WorkReserved < 0 || e.Job.WorkReserved > 3 {
		return job.PermanentFailure("job.command.invalid", errors.New("invalid Exam export cleanup command"))
	}
	reserved, err := e.ReserveWork(ctx, 1, 3)
	if err != nil {
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export cleanup reservation failed"))
	}
	if !reserved {
		return job.PermanentFailure("exam.export.unavailable", errors.New("Exam export cleanup budget exhausted"))
	}
	reconciled, err := h.exports.Reconcile(ctx, command.BatchSize)
	if err != nil || reconciled < 0 || reconciled > command.BatchSize {
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export expiry reconciliation failed"))
	}
	artifacts, err := h.exports.BeginPurgeBatch(ctx, command.BatchSize)
	if err != nil || len(artifacts) > command.BatchSize {
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export cleanup inventory failed"))
	}
	purged, failed := 0, false
	for _, a := range artifacts {
		if err = ctx.Err(); err != nil {
			return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export cleanup interrupted"))
		}
		if !a.ExportID.IsValid() || !a.AttemptID.IsValid() {
			failed = true
			continue
		}
		if err = h.content.PurgeExamExport(ctx, a.ExportID, a.AttemptID); err != nil {
			failed = true
			continue
		}
		if err = h.exports.CompletePurge(ctx, a); err != nil {
			failed = true
			continue
		}
		purged++
	}
	if failed {
		return job.RetryableFailure("exam.export.unavailable", errors.New("Exam export cleanup remains incomplete"))
	}
	result, _ := json.Marshal(struct {
		Reconciled     int `json:"reconciled"`
		ObservedAbsent int `json:"observed_absent"`
	}{reconciled, purged})
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: result}
}

func examExportCleanupDescriptor(handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: model.JobTypeExamExportCleanup, PublicErrorCodes: []string{"job.command.invalid", "exam.export.unavailable"},
		Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 3, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute, MaximumRetryDelay: 5 * time.Minute,
		Visibility: job.VisibilityOperator, SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 7 * 24 * time.Hour, Handler: handler}
}

type examExportCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p examExportCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	at := model.TimeUTC(p.now())
	command, _ := json.Marshal(examExportCleanupCommand{BatchSize: 100})
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamExportCleanup, command, "exam-export-cleanup:"+model.TimeUTC(occurrence).Format("2006-01-02T15"), model.JobDedupePermanent, at, at, 3)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}
