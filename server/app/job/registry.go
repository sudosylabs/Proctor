// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type Execution struct {
	Job         *model.Job
	Attempt     *model.JobAttempt
	checkpoint  func(context.Context, CheckpointValue) error
	reserveWork func(context.Context, int, int) (bool, error)
}

func NewExecution(record *model.Job, attempt *model.JobAttempt, checkpoint func(context.Context, CheckpointValue) error, reserveWork func(context.Context, int, int) (bool, error)) Execution {
	return Execution{Job: record, Attempt: attempt, checkpoint: checkpoint, reserveWork: reserveWork}
}

func (e Execution) ReserveWork(ctx context.Context, units, limit int) (bool, error) {
	if e.reserveWork == nil {
		return false, errors.New("job execution does not support work reservation")
	}
	return e.reserveWork(ctx, units, limit)
}

type CheckpointValue struct {
	Version  int
	Progress *model.JobProgress
	Document json.RawMessage
}

func (e Execution) Checkpoint(ctx context.Context, value CheckpointValue) error {
	if e.checkpoint == nil {
		return errors.New("job execution does not support checkpointing")
	}
	return e.checkpoint(ctx, value)
}

var jobContractCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type OutcomeKind string

const (
	OutcomeSucceeded        OutcomeKind = "succeeded"
	OutcomeRetryableFailure OutcomeKind = "retryable_failure"
	OutcomePermanentFailure OutcomeKind = "permanent_failure"
	OutcomeCanceled         OutcomeKind = "canceled"
)

type Outcome struct {
	Kind            OutcomeKind
	ResultVersion   int
	Result          json.RawMessage
	PublicErrorCode string
	Err             error
}

func RetryableFailure(code string, err error) Outcome {
	return Outcome{Kind: OutcomeRetryableFailure, PublicErrorCode: code, Err: err}
}

func PermanentFailure(code string, err error) Outcome {
	return Outcome{Kind: OutcomePermanentFailure, PublicErrorCode: code, Err: err}
}

func Canceled(code string) Outcome {
	return Outcome{Kind: OutcomeCanceled, PublicErrorCode: code}
}

type Handler interface {
	Run(context.Context, Execution) Outcome
}

type Visibility string

const (
	VisibilityOperator Visibility = "operator"
	VisibilityDomain   Visibility = "domain"
)

type Descriptor struct {
	Type                  model.JobType
	CommandVersions       []int
	CheckpointVersions    []int
	ResultVersions        []int
	ProgressStages        []string
	PublicErrorCodes      []string
	Timeout               time.Duration
	Concurrency           int
	MaximumAttempts       int
	LeaseDuration         time.Duration
	HeartbeatInterval     time.Duration
	BaseRetryDelay        time.Duration
	MaximumRetryDelay     time.Duration
	Cancelable            bool
	ExplicitRetryStatuses []model.JobStatus
	Visibility            Visibility
	SuccessRetention      time.Duration
	FailureRetention      time.Duration
	Handler               Handler
}

type Registry struct {
	descriptors map[model.JobType]Descriptor
	types       []model.JobType
}

func NewRegistry(values []Descriptor) (*Registry, error) {
	if len(values) == 0 {
		return nil, errors.New("job registry requires at least one descriptor")
	}
	registry := &Registry{descriptors: make(map[model.JobType]Descriptor, len(values)), types: make([]model.JobType, 0, len(values))}
	for _, value := range values {
		if value.Handler == nil || len(value.CommandVersions) == 0 || len(value.ResultVersions) == 0 || value.Timeout <= 0 || value.Concurrency <= 0 || value.MaximumAttempts <= 0 || value.LeaseDuration <= 0 || value.HeartbeatInterval <= 0 || value.HeartbeatInterval >= value.LeaseDuration || value.BaseRetryDelay <= 0 || value.MaximumRetryDelay < value.BaseRetryDelay || (value.Visibility != VisibilityOperator && value.Visibility != VisibilityDomain) || value.SuccessRetention <= 0 || value.FailureRetention < value.SuccessRetention {
			return nil, fmt.Errorf("invalid descriptor for job type %q", value.Type)
		}
		if _, exists := registry.descriptors[value.Type]; exists {
			return nil, fmt.Errorf("duplicate job type %q", value.Type)
		}
		var versionsErr error
		value.CommandVersions, versionsErr = normalizedVersions(value.CommandVersions, false)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid command versions for job type %q: %w", value.Type, versionsErr)
		}
		value.CheckpointVersions, versionsErr = normalizedVersions(value.CheckpointVersions, true)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid checkpoint versions for job type %q: %w", value.Type, versionsErr)
		}
		value.ResultVersions, versionsErr = normalizedVersions(value.ResultVersions, false)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid result versions for job type %q: %w", value.Type, versionsErr)
		}
		value.ProgressStages, versionsErr = normalizedCodes(value.ProgressStages)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid progress stages for job type %q: %w", value.Type, versionsErr)
		}
		value.PublicErrorCodes, versionsErr = normalizedCodes(value.PublicErrorCodes)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid public error codes for job type %q: %w", value.Type, versionsErr)
		}
		value.ExplicitRetryStatuses, versionsErr = normalizedTerminalJobStatuses(value.ExplicitRetryStatuses)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid explicit retry statuses for job type %q: %w", value.Type, versionsErr)
		}
		registry.descriptors[value.Type] = value
		registry.types = append(registry.types, value.Type)
	}
	slices.Sort(registry.types)
	return registry, nil
}

func (r *Registry) Resolve(jobType model.JobType, commandVersion int) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, errors.New("job registry is nil")
	}
	descriptor, ok := r.descriptors[jobType]
	if !ok {
		return Descriptor{}, fmt.Errorf("job type %q is not registered", jobType)
	}
	if _, ok = slices.BinarySearch(descriptor.CommandVersions, commandVersion); !ok {
		return Descriptor{}, fmt.Errorf("job type %q does not support command version %d", jobType, commandVersion)
	}
	descriptor.CommandVersions = append([]int(nil), descriptor.CommandVersions...)
	descriptor.CheckpointVersions = append([]int(nil), descriptor.CheckpointVersions...)
	descriptor.ResultVersions = append([]int(nil), descriptor.ResultVersions...)
	descriptor.ProgressStages = append([]string(nil), descriptor.ProgressStages...)
	descriptor.PublicErrorCodes = append([]string(nil), descriptor.PublicErrorCodes...)
	descriptor.ExplicitRetryStatuses = append([]model.JobStatus(nil), descriptor.ExplicitRetryStatuses...)
	return descriptor, nil
}

func (r *Registry) Descriptor(jobType model.JobType) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, errors.New("job registry is nil")
	}
	descriptor, ok := r.descriptors[jobType]
	if !ok {
		return Descriptor{}, fmt.Errorf("job type %q is not registered", jobType)
	}
	descriptor.CommandVersions = append([]int(nil), descriptor.CommandVersions...)
	descriptor.CheckpointVersions = append([]int(nil), descriptor.CheckpointVersions...)
	descriptor.ResultVersions = append([]int(nil), descriptor.ResultVersions...)
	descriptor.ProgressStages = append([]string(nil), descriptor.ProgressStages...)
	descriptor.PublicErrorCodes = append([]string(nil), descriptor.PublicErrorCodes...)
	descriptor.ExplicitRetryStatuses = append([]model.JobStatus(nil), descriptor.ExplicitRetryStatuses...)
	return descriptor, nil
}

func (d Descriptor) SupportsResultVersion(version int) bool {
	_, ok := slices.BinarySearch(d.ResultVersions, version)
	return ok
}

func (d Descriptor) SupportsCheckpointVersion(version int) bool {
	_, ok := slices.BinarySearch(d.CheckpointVersions, version)
	return ok
}

func (d Descriptor) SupportsProgressStage(stage string) bool {
	_, ok := slices.BinarySearch(d.ProgressStages, stage)
	return ok
}

func (d Descriptor) SupportsPublicErrorCode(code string) bool {
	if code == "" {
		return true
	}
	if slices.Contains([]string{"job.canceled", "job.handler_panic", "job.timeout", "job.result.invalid", "job.outcome.invalid"}, code) {
		return true
	}
	_, ok := slices.BinarySearch(d.PublicErrorCodes, code)
	return ok
}

func (d Descriptor) SupportsExplicitRetry(status model.JobStatus) bool {
	return slices.Contains(d.ExplicitRetryStatuses, status)
}

func normalizedTerminalJobStatuses(values []model.JobStatus) ([]model.JobStatus, error) {
	statuses := append([]model.JobStatus(nil), values...)
	slices.Sort(statuses)
	for index, status := range statuses {
		if (status != model.JobStatusFailed && status != model.JobStatusCanceled) ||
			(index > 0 && statuses[index-1] == status) {
			return nil, errors.New("statuses must be unique terminal retry states")
		}
	}
	return statuses, nil
}

func normalizedVersions(values []int, optional bool) ([]int, error) {
	if len(values) == 0 {
		if optional {
			return nil, nil
		}
		return nil, errors.New("at least one version is required")
	}
	versions := append([]int(nil), values...)
	slices.Sort(versions)
	for index, version := range versions {
		if version <= 0 || (index > 0 && versions[index-1] == version) {
			return nil, errors.New("versions must be unique positive integers")
		}
	}
	return versions, nil
}

func normalizedCodes(values []string) ([]string, error) {
	codes := append([]string(nil), values...)
	slices.Sort(codes)
	for index, code := range codes {
		if !jobContractCode.MatchString(code) || (index > 0 && codes[index-1] == code) {
			return nil, errors.New("codes must be unique safe identifiers")
		}
	}
	return codes, nil
}

func (r *Registry) Types() []model.JobType {
	if r == nil {
		return nil
	}
	return append([]model.JobType(nil), r.types...)
}

func (r *Registry) MaximumTimeout() time.Duration {
	var maximum time.Duration
	if r == nil {
		return 0
	}
	for _, descriptor := range r.descriptors {
		maximum = max(maximum, descriptor.Timeout)
	}
	return maximum
}
