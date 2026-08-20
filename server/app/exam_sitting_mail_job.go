// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type sittingMailExpansionStore interface {
	GetMailFanout(context.Context, model.MailOccurrenceID) (*store.ExamSittingMailFanoutSnapshot, error)
	ListMailRecipients(context.Context, store.ExamSittingMailRecipientPageRequest) (*store.ExamSittingMailRecipientPage, error)
	CommitMailRecipient(context.Context, *store.ExamSittingMailRecipientCommit) (*store.ExamSittingMailRecipientResult, error)
	CompleteMailExpansion(context.Context, *store.ExamSittingMailExpansionCompletion) (*store.ExamSittingMailFanoutSnapshot, error)
}

type sittingMailExpansionHandler struct {
	sittings sittingMailExpansionStore
	mail     *sittingMailPreparer
}

func (handler sittingMailExpansionHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Sitting mail expansion Job is missing"))
	}
	command, err := model.DecodeSittingMailExpansionCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	if handler.sittings == nil || handler.mail == nil {
		return jobengine.RetryableFailure("mail.sitting.unavailable", errors.New("Sitting mail expansion dependencies are unavailable"))
	}
	checkpoint := model.SittingMailExpansionCheckpointV1{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = model.DecodeSittingMailExpansionCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
	}
	fanout, err := handler.sittings.GetMailFanout(ctx, command.OccurrenceID)
	if err != nil {
		return sittingMailExpansionDependencyOutcome(err)
	}
	if fanout.CompletedAt.Valid {
		return sittingMailExpansionSucceeded(checkpoint)
	}
	bundle, err := handler.mail.open(fanout.Bundle)
	if err != nil {
		return jobengine.RetryableFailure("mail.sitting.bundle_unavailable", err)
	}
	for {
		page, pageErr := handler.sittings.ListMailRecipients(ctx, store.ExamSittingMailRecipientPageRequest{
			OccurrenceID: command.OccurrenceID, AfterUserID: checkpoint.AfterUserID, Limit: model.SittingMailExpansionPageSize,
		})
		if pageErr != nil {
			return sittingMailExpansionDependencyOutcome(pageErr)
		}
		for index := range page.Recipients {
			recipient := page.Recipients[index]
			commit := &store.ExamSittingMailRecipientCommit{OccurrenceID: command.OccurrenceID,
				SittingRevision: fanout.SittingRevision, Recipient: recipient.User}
			if recipient.TemplateKey.IsValid() {
				commit.Delivery, commit.DeliveryJob, err = handler.mail.prepareRecipient(fanout, recipient.User, recipient.TemplateKey, bundle)
				if err != nil {
					return jobengine.RetryableFailure("mail.sitting.recipient_unavailable", err)
				}
			}
			result, commitErr := handler.sittings.CommitMailRecipient(ctx, commit)
			if commitErr != nil {
				if store.IsConflict(commitErr) || store.IsNotFound(commitErr) {
					checkpoint.Suppressed++
					continue
				}
				return sittingMailExpansionDependencyOutcome(commitErr)
			}
			if result.Inserted {
				checkpoint.Expanded++
			} else {
				checkpoint.Suppressed++
			}
		}
		if len(page.Recipients) != 0 {
			checkpoint.AfterUserID = page.Recipients[len(page.Recipients)-1].User.ID
			document, encodeErr := model.EncodeSittingMailExpansionCheckpoint(checkpoint)
			if encodeErr != nil {
				return jobengine.PermanentFailure("job.checkpoint.invalid", encodeErr)
			}
			if checkpointErr := execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1,
				Progress: &model.JobProgress{Current: checkpoint.Expanded + checkpoint.Suppressed,
					Total: checkpoint.Expanded + checkpoint.Suppressed, Stage: "expanding"}, Document: document}); checkpointErr != nil {
				return jobengine.RetryableFailure("mail.sitting.unavailable", checkpointErr)
			}
		}
		if !page.More {
			break
		}
	}
	if _, err = handler.sittings.CompleteMailExpansion(ctx, &store.ExamSittingMailExpansionCompletion{OccurrenceID: command.OccurrenceID}); err != nil {
		return sittingMailExpansionDependencyOutcome(err)
	}
	return sittingMailExpansionSucceeded(checkpoint)
}

func sittingMailExpansionSucceeded(checkpoint model.SittingMailExpansionCheckpointV1) jobengine.Outcome {
	document, err := model.EncodeSittingMailExpansionResult(model.SittingMailExpansionResultV1{
		Expanded: checkpoint.Expanded, Suppressed: checkpoint.Suppressed})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func sittingMailExpansionDependencyOutcome(err error) jobengine.Outcome {
	return jobengine.RetryableFailure("mail.sitting.unavailable", err)
}

func sittingMailExpansionDescriptor(handler sittingMailExpansionHandler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeMailExpandSitting, CommandVersions: []int{1}, CheckpointVersions: []int{1},
		ResultVersions: []int{1}, ProgressStages: []string{"expanding"},
		PublicErrorCodes: []string{"mail.sitting.bundle_unavailable", "mail.sitting.invalid", "mail.sitting.recipient_unavailable", "mail.sitting.unavailable"},
		Timeout:          30 * time.Minute, Concurrency: 2, MaximumAttempts: model.MailMaximumAttempts,
		LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: 30 * time.Second,
		MaximumRetryDelay: 30 * time.Minute, Visibility: jobengine.VisibilityOperator,
		SuccessRetention: 90 * 24 * time.Hour, FailureRetention: 180 * 24 * time.Hour, Handler: handler}
}
