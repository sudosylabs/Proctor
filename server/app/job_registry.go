// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type JobExecution struct {
	Job        *model.Job
	Attempt    *model.JobAttempt
	checkpoint func(context.Context, JobCheckpointValue) error
}

type JobCheckpointValue struct {
	Version  int
	Progress *model.JobProgress
	Document json.RawMessage
}

func (e JobExecution) Checkpoint(ctx context.Context, value JobCheckpointValue) error {
	if e.checkpoint == nil {
		return errors.New("job execution does not support checkpointing")
	}
	return e.checkpoint(ctx, value)
}

var jobContractCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type JobOutcomeKind string

const (
	JobOutcomeSucceeded        JobOutcomeKind = "succeeded"
	JobOutcomeRetryableFailure JobOutcomeKind = "retryable_failure"
	JobOutcomePermanentFailure JobOutcomeKind = "permanent_failure"
	JobOutcomeCanceled         JobOutcomeKind = "canceled"
)

type JobOutcome struct {
	Kind            JobOutcomeKind
	ResultVersion   int
	Result          json.RawMessage
	PublicErrorCode string
	Err             error
}

func JobRetryableFailure(code string, err error) JobOutcome {
	return JobOutcome{Kind: JobOutcomeRetryableFailure, PublicErrorCode: code, Err: err}
}

func JobPermanentFailure(code string, err error) JobOutcome {
	return JobOutcome{Kind: JobOutcomePermanentFailure, PublicErrorCode: code, Err: err}
}

func JobCanceled(code string) JobOutcome {
	return JobOutcome{Kind: JobOutcomeCanceled, PublicErrorCode: code}
}

type JobHandler interface {
	Run(context.Context, JobExecution) JobOutcome
}

type JobVisibility string

const (
	JobVisibilityOperator JobVisibility = "operator"
	JobVisibilityDomain   JobVisibility = "domain"
)

type JobDescriptor struct {
	Type               model.JobType
	CommandVersions    []int
	CheckpointVersions []int
	ResultVersions     []int
	ProgressStages     []string
	PublicErrorCodes   []string
	Timeout            time.Duration
	Concurrency        int
	MaximumAttempts    int
	LeaseDuration      time.Duration
	HeartbeatInterval  time.Duration
	BaseRetryDelay     time.Duration
	MaximumRetryDelay  time.Duration
	Cancelable         bool
	Visibility         JobVisibility
	SuccessRetention   time.Duration
	FailureRetention   time.Duration
	Handler            JobHandler
}

type JobRegistry struct {
	descriptors map[model.JobType]JobDescriptor
	types       []model.JobType
}

func NewJobRegistry(values []JobDescriptor) (*JobRegistry, error) {
	if len(values) == 0 {
		return nil, errors.New("job registry requires at least one descriptor")
	}
	registry := &JobRegistry{descriptors: make(map[model.JobType]JobDescriptor, len(values)), types: make([]model.JobType, 0, len(values))}
	for _, value := range values {
		if value.Handler == nil || len(value.CommandVersions) == 0 || len(value.ResultVersions) == 0 || value.Timeout <= 0 || value.Concurrency <= 0 || value.MaximumAttempts <= 0 || value.LeaseDuration <= 0 || value.HeartbeatInterval <= 0 || value.HeartbeatInterval >= value.LeaseDuration || value.BaseRetryDelay <= 0 || value.MaximumRetryDelay < value.BaseRetryDelay || (value.Visibility != JobVisibilityOperator && value.Visibility != JobVisibilityDomain) || value.SuccessRetention <= 0 || value.FailureRetention < value.SuccessRetention {
			return nil, fmt.Errorf("invalid descriptor for job type %q", value.Type)
		}
		if _, exists := registry.descriptors[value.Type]; exists {
			return nil, fmt.Errorf("duplicate job type %q", value.Type)
		}
		var versionsErr error
		value.CommandVersions, versionsErr = normalizedJobVersions(value.CommandVersions, false)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid command versions for job type %q: %w", value.Type, versionsErr)
		}
		value.CheckpointVersions, versionsErr = normalizedJobVersions(value.CheckpointVersions, true)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid checkpoint versions for job type %q: %w", value.Type, versionsErr)
		}
		value.ResultVersions, versionsErr = normalizedJobVersions(value.ResultVersions, false)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid result versions for job type %q: %w", value.Type, versionsErr)
		}
		value.ProgressStages, versionsErr = normalizedJobCodes(value.ProgressStages)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid progress stages for job type %q: %w", value.Type, versionsErr)
		}
		value.PublicErrorCodes, versionsErr = normalizedJobCodes(value.PublicErrorCodes)
		if versionsErr != nil {
			return nil, fmt.Errorf("invalid public error codes for job type %q: %w", value.Type, versionsErr)
		}
		registry.descriptors[value.Type] = value
		registry.types = append(registry.types, value.Type)
	}
	slices.Sort(registry.types)
	return registry, nil
}

func (r *JobRegistry) Resolve(jobType model.JobType, commandVersion int) (JobDescriptor, error) {
	if r == nil {
		return JobDescriptor{}, errors.New("job registry is nil")
	}
	descriptor, ok := r.descriptors[jobType]
	if !ok {
		return JobDescriptor{}, fmt.Errorf("job type %q is not registered", jobType)
	}
	if _, ok = slices.BinarySearch(descriptor.CommandVersions, commandVersion); !ok {
		return JobDescriptor{}, fmt.Errorf("job type %q does not support command version %d", jobType, commandVersion)
	}
	descriptor.CommandVersions = append([]int(nil), descriptor.CommandVersions...)
	descriptor.CheckpointVersions = append([]int(nil), descriptor.CheckpointVersions...)
	descriptor.ResultVersions = append([]int(nil), descriptor.ResultVersions...)
	descriptor.ProgressStages = append([]string(nil), descriptor.ProgressStages...)
	descriptor.PublicErrorCodes = append([]string(nil), descriptor.PublicErrorCodes...)
	return descriptor, nil
}

func (r *JobRegistry) Descriptor(jobType model.JobType) (JobDescriptor, error) {
	if r == nil {
		return JobDescriptor{}, errors.New("job registry is nil")
	}
	descriptor, ok := r.descriptors[jobType]
	if !ok {
		return JobDescriptor{}, fmt.Errorf("job type %q is not registered", jobType)
	}
	descriptor.CommandVersions = append([]int(nil), descriptor.CommandVersions...)
	descriptor.CheckpointVersions = append([]int(nil), descriptor.CheckpointVersions...)
	descriptor.ResultVersions = append([]int(nil), descriptor.ResultVersions...)
	descriptor.ProgressStages = append([]string(nil), descriptor.ProgressStages...)
	descriptor.PublicErrorCodes = append([]string(nil), descriptor.PublicErrorCodes...)
	return descriptor, nil
}

func (d JobDescriptor) SupportsResultVersion(version int) bool {
	_, ok := slices.BinarySearch(d.ResultVersions, version)
	return ok
}

func (d JobDescriptor) SupportsCheckpointVersion(version int) bool {
	_, ok := slices.BinarySearch(d.CheckpointVersions, version)
	return ok
}

func (d JobDescriptor) SupportsProgressStage(stage string) bool {
	_, ok := slices.BinarySearch(d.ProgressStages, stage)
	return ok
}

func (d JobDescriptor) SupportsPublicErrorCode(code string) bool {
	if code == "" {
		return true
	}
	if slices.Contains([]string{"job.handler_panic", "job.timeout", "job.result.invalid", "job.outcome.invalid"}, code) {
		return true
	}
	_, ok := slices.BinarySearch(d.PublicErrorCodes, code)
	return ok
}

func normalizedJobVersions(values []int, optional bool) ([]int, error) {
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

func normalizedJobCodes(values []string) ([]string, error) {
	codes := append([]string(nil), values...)
	slices.Sort(codes)
	for index, code := range codes {
		if !jobContractCode.MatchString(code) || (index > 0 && codes[index-1] == code) {
			return nil, errors.New("codes must be unique safe identifiers")
		}
	}
	return codes, nil
}

func (r *JobRegistry) Types() []model.JobType {
	if r == nil {
		return nil
	}
	return append([]model.JobType(nil), r.types...)
}

func (r *JobRegistry) MaximumTimeout() time.Duration {
	var maximum time.Duration
	if r == nil {
		return 0
	}
	for _, descriptor := range r.descriptors {
		maximum = max(maximum, descriptor.Timeout)
	}
	return maximum
}

type DefaultProfilePictureCommandV1 struct {
	UserID model.UserID `json:"user_id"`
}

type DefaultProfilePictureResultV1 struct {
	FileEntryID model.FileEntryID `json:"file_entry_id"`
}

func DefaultProfilePictureJobSucceeded(fileEntryID model.FileEntryID) JobOutcome {
	if !fileEntryID.IsValid() {
		return JobOutcome{Kind: JobOutcomeSucceeded, Err: errors.New("default profile-picture result has invalid file entry ID")}
	}
	document, err := json.Marshal(DefaultProfilePictureResultV1{FileEntryID: fileEntryID})
	return JobOutcome{Kind: JobOutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func EncodeDefaultProfilePictureCommand(command DefaultProfilePictureCommandV1) (json.RawMessage, error) {
	if !command.UserID.IsValid() {
		return nil, errors.New("default profile-picture command has invalid user ID")
	}
	return json.Marshal(command)
}

func DecodeDefaultProfilePictureCommand(version int, document json.RawMessage) (DefaultProfilePictureCommandV1, error) {
	var command DefaultProfilePictureCommandV1
	if version != 1 {
		return command, fmt.Errorf("unsupported default profile-picture command version %d", version)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, fmt.Errorf("decode default profile-picture command: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return command, err
	}
	if !command.UserID.IsValid() {
		return command, errors.New("default profile-picture command has invalid user ID")
	}
	return command, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("job command contains trailing JSON")
		}
		return fmt.Errorf("decode trailing job command: %w", err)
	}
	return nil
}
