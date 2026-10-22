// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// These adapters translate application-owned policy and use cases into the
// narrow capabilities consumed by concrete Jobs. They intentionally do not
// expose App, Dependencies, store.Catalog, or platform.Service.

func prepareUserDefaultProfilePictureJob(user *model.User, at time.Time) (*model.User, *model.Job, error) {
	return jobhandler.PrepareUserDefaultProfilePictureJob(user, at)
}

type jobDefaultProfilePictureGenerator struct{ service *profilePictureService }

func (a jobDefaultProfilePictureGenerator) EnsureDefaultProfilePicture(ctx context.Context, id model.UserID) (model.FileEntryID, error) {
	return a.service.EnsureDefaultProfilePicture(ctx, id)
}

func (jobDefaultProfilePictureGenerator) ClassifyDefaultProfilePictureError(err error) string {
	switch {
	case errors.Is(err, errDefaultProfilePictureUserNotFound):
		return "user.not_found"
	case errors.Is(err, errDefaultProfilePictureInvariant):
		return "job.invariant_failed"
	default:
		return ""
	}
}

type jobMailDeliveryRecorder struct{ recorder MailDeliveryRecorder }

func (a jobMailDeliveryRecorder) RecordJobMailDelivery(ctx context.Context, metric jobhandler.MailDeliveryMetric) {
	if a.recorder == nil {
		return
	}
	a.recorder.RecordMailDelivery(ctx, MailDeliveryMetric{
		TemplateKey: metric.TemplateKey, State: metric.State, OutcomeCode: metric.OutcomeCode,
		ProcessingLatency: metric.ProcessingLatency,
	})
}

func (a jobMailDeliveryRecorder) RecordJobMailAttempt(ctx context.Context, metric jobhandler.MailAttemptMetric) {
	if a.recorder == nil {
		return
	}
	a.recorder.RecordMailAttempt(ctx, MailAttemptMetric{TemplateKey: metric.TemplateKey, State: metric.State})
}

type jobMailHealth struct{ health *MailHealth }

func (a jobMailHealth) SetJobMailHealth(code string) {
	if a.health != nil {
		a.health.set(code)
	}
}

func jobMailDeliveryIsRelevant(ctx context.Context, delivery *model.MailDelivery) (bool, error) {
	relevance, err := evaluateMailDeliveryRelevance(ctx, delivery)
	return relevance == mailDeliveryRelevant, err
}

type onboardingImportJobs struct{ service *onboardingImportService }

func (a onboardingImportJobs) PreviewProgression(ctx context.Context, id model.OnboardingImportID) (string, error) {
	err := a.service.previewProgression(ctx, id)
	if err == nil {
		return "", nil
	}
	if failure, ok := apperror.As(err); ok {
		switch failure.Code() {
		case "authorization.denied", "authentication.token_invalid":
			return "student_progression.authorization_lost", err
		case "student_progression.target_conflict", "student_progression.lineage_conflict", "student_progression.effective_date_conflict", "student_progression.roster_too_large":
			return failure.Code(), err
		}
	} else if store.IsNotFound(err) || store.IsConflict(err) {
		return "student_progression.target_conflict", err
	}
	return "", err
}

func (a onboardingImportJobs) Parse(ctx context.Context, id model.OnboardingImportID) (string, error) {
	err := a.service.parse(ctx, id)
	if err == nil {
		return "", nil
	}
	if failure, ok := apperror.As(err); ok && (failure.Code() == "authentication.token_invalid" || failure.Code() == "authorization.denied") {
		return "onboarding_import.authorization_lost", err
	}
	if errors.Is(err, errOnboardingImportInvalidFile) {
		return "onboarding_import.file_invalid", err
	}
	return "", err
}

func (a onboardingImportJobs) Get(ctx context.Context, id model.OnboardingImportID) (*store.OnboardingImport, error) {
	return a.service.imports.GetOnboardingImport(ctx, id)
}

func (a onboardingImportJobs) Execute(ctx context.Context, id model.OnboardingImportID, after int,
	checkpoint func(int, int) error,
) error {
	return a.service.execute(ctx, id, after, checkpoint)
}

func (a onboardingImportJobs) Fail(ctx context.Context, id model.OnboardingImportID, code string) error {
	_, err := a.service.imports.FailOnboardingImport(ctx, id, code, model.TimeUTC(a.service.now()))
	return err
}

type examSittingSealingJobUseCases struct {
	sittings *sitting.Service
	attempts *attempt.Service
	jobs     store.JobStore
	now      func() time.Time
	newID    func() model.JobID
}

func (uc examSittingSealingJobUseCases) ListExamSittingSealTargetsFromJob(ctx context.Context,
	sittingID model.ExamSittingID, after model.ExamAttemptID, limit int,
) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	return uc.attempts.ListAutomaticSealTargets(ctx, sittingID, after, limit)
}

func (uc examSittingSealingJobUseCases) SealExamAttemptForSittingCloseFromJob(ctx context.Context,
	target store.ExamSubmissionAutomaticSealTarget, jobID model.JobID, attemptID model.JobAttemptID,
) (attempt.AutomaticSubmissionResult, error) {
	return uc.attempts.SealForSittingClose(ctx, invocation.System{JobID: jobID, AttemptID: attemptID}, target)
}

func (uc examSittingSealingJobUseCases) FinishExamSittingSealingFromJob(ctx context.Context,
	sittingID model.ExamSittingID, jobID model.JobID, attemptID model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	result, err := uc.sittings.FinishSealing(ctx, invocation.System{JobID: jobID, AttemptID: attemptID}, sittingID)
	return &result, err
}

func (uc examSittingSealingJobUseCases) EnqueueExamSittingSealingContinuationFromJob(ctx context.Context,
	sittingID model.ExamSittingID, parentJobID model.JobID,
) error {
	if uc.jobs == nil || uc.now == nil || uc.newID == nil || !sittingID.IsValid() || !parentJobID.IsValid() {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", nil)
	}
	document, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommand{ExamSittingID: sittingID})
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", err)
	}
	at := model.TimeUTC(uc.now())
	job, err := model.NewJobWithDedupePolicy(uc.newID(), model.JobTypeExamSittingSealing, document,
		"exam-sitting-sealing-continuation:"+parentJobID.String(), model.JobDedupePermanent, at, at, 8)
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", err)
	}
	_, _, err = uc.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}
