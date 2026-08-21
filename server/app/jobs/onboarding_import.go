// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

type onboardingImportCheckpointV1 struct {
	AfterRow  int `json:"after_row"`
	Processed int `json:"processed"`
}

type onboardingImportJobCommandV1 struct {
	ImportID string `json:"import_id"`
}

type OnboardingImportService interface {
	PreviewProgression(context.Context, model.OnboardingImportID) (string, error)
	Parse(context.Context, model.OnboardingImportID) (string, error)
	Get(context.Context, model.OnboardingImportID) (*store.OnboardingImport, error)
	Execute(context.Context, model.OnboardingImportID, int, func(int, int) error) error
	Fail(context.Context, model.OnboardingImportID, string) error
}

type onboardingImportParseHandler struct{ service OnboardingImportService }

type studentProgressionPreviewHandler struct{ service OnboardingImportService }

func (h studentProgressionPreviewHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	command, err := decodeOnboardingImportJob(execution, model.JobTypeStudentProgressionPreview)
	if err != nil || h.service == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("student progression preview command is invalid"))
	}
	terminalCode, err := h.service.PreviewProgression(ctx, model.OnboardingImportID(command.ImportID))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return jobengine.Canceled("job.canceled")
		}
		if terminalCode != "" {
			if failErr := h.service.Fail(ctx, model.OnboardingImportID(command.ImportID), terminalCode); failErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", failErr)
			}
			return jobengine.PermanentFailure(terminalCode, err)
		}
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{"preview_ready":true}`)}
}

func (h onboardingImportParseHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	command, err := decodeOnboardingImportJob(execution, model.JobTypeOnboardingImportParse)
	if err != nil || h.service == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("onboarding import parse command is invalid"))
	}
	terminalCode, err := h.service.Parse(ctx, model.OnboardingImportID(command.ImportID))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return jobengine.Canceled("job.canceled")
		}
		if terminalCode != "" {
			return jobengine.PermanentFailure(terminalCode, err)
		}
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{"preview_ready":true}`)}
}

type onboardingImportExecuteHandler struct{ service OnboardingImportService }

func (h onboardingImportExecuteHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	command, err := decodeOnboardingImportJob(execution, model.JobTypeOnboardingImportExecute)
	if err != nil || h.service == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("onboarding import execution command is invalid"))
	}
	checkpoint := onboardingImportCheckpointV1{}
	if execution.Job.CheckpointVersion != 0 {
		if execution.Job.CheckpointVersion != 1 || decodeStrictJobDocument(execution.Job.Checkpoint, &checkpoint) != nil || checkpoint.AfterRow < 0 || checkpoint.Processed < 0 {
			return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("onboarding import checkpoint is invalid"))
		}
	}
	current, err := h.service.Get(ctx, model.OnboardingImportID(command.ImportID))
	if err != nil {
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	total := max(current.ValidRows, 1)
	lastAfter, lastProcessed := checkpoint.AfterRow, checkpoint.Processed
	err = h.service.Execute(ctx, model.OnboardingImportID(command.ImportID), checkpoint.AfterRow, func(after, processed int) error {
		lastAfter, lastProcessed = after, checkpoint.Processed+processed
		document, marshalErr := json.Marshal(onboardingImportCheckpointV1{AfterRow: lastAfter, Processed: lastProcessed})
		if marshalErr != nil {
			return marshalErr
		}
		return execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document,
			Progress: &model.JobProgress{Current: int64(lastProcessed), Total: int64(total), Stage: "executing"}})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return jobengine.Canceled("job.canceled")
		}
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	result, _ := json.Marshal(onboardingImportCheckpointV1{AfterRow: lastAfter, Processed: lastProcessed})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: result}
}

func decodeOnboardingImportJob(execution jobengine.Execution, expected model.JobType) (onboardingImportJobCommandV1, error) {
	var command onboardingImportJobCommandV1
	if execution.Job == nil || execution.Job.Type != expected || execution.Job.CommandVersion != 1 || decodeStrictJobDocument(execution.Job.Command, &command) != nil || !model.OnboardingImportID(command.ImportID).IsValid() {
		return command, errors.New("invalid onboarding import job")
	}
	return command, nil
}

func onboardingImportParseDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return onboardingImportDescriptor(model.JobTypeOnboardingImportParse, handler, true)
}

func studentProgressionPreviewDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	descriptor := onboardingImportDescriptor(model.JobTypeStudentProgressionPreview, handler, true)
	descriptor.PublicErrorCodes = append(descriptor.PublicErrorCodes, "student_progression.authorization_lost", "student_progression.target_conflict",
		"student_progression.lineage_conflict", "student_progression.effective_date_conflict", "student_progression.roster_too_large")
	return descriptor
}

func onboardingImportExecuteDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	descriptor := onboardingImportDescriptor(model.JobTypeOnboardingImportExecute, handler, true)
	descriptor.CheckpointVersions = []int{1}
	descriptor.ProgressStages = []string{"executing"}
	descriptor.Timeout = 30 * time.Minute
	return descriptor
}

func NewOnboardingImportExecuteDescriptor(service OnboardingImportService) jobengine.Descriptor {
	return onboardingImportExecuteDescriptor(onboardingImportExecuteHandler{service: service})
}

func onboardingImportDescriptor(kind model.JobType, handler jobengine.Handler, cancelable bool) jobengine.Descriptor {
	return jobengine.Descriptor{Type: kind, CommandVersions: []int{1}, ResultVersions: []int{1},
		PublicErrorCodes: []string{"dependency.unavailable", "job.canceled", "job.checkpoint.invalid", "job.command.invalid", "onboarding_import.authorization_lost", "onboarding_import.invalid_file"},
		Timeout:          10 * time.Minute, Concurrency: 2, MaximumAttempts: 8, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second,
		BaseRetryDelay: 10 * time.Second, MaximumRetryDelay: 5 * time.Minute, Cancelable: cancelable, Visibility: jobengine.VisibilityDomain,
		SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 7 * 24 * time.Hour, Handler: handler}
}
