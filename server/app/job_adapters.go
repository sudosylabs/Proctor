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

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// These adapters translate application-owned policy and use cases into the
// narrow capabilities consumed by concrete Jobs. They intentionally do not
// expose App, Dependencies, store.Catalog, or platform.Service.

type MailRekeyCommandV1 = appjobs.MailRekeyCommandV1
type MailRekeyCheckpointV1 = appjobs.MailRekeyCheckpointV1
type MailRekeyResultV1 = appjobs.MailRekeyResultV1

func prepareUserDefaultProfilePictureJob(user *model.User, at time.Time) (*model.User, *model.Job, error) {
	return appjobs.PrepareUserDefaultProfilePictureJob(user, at)
}

type jobDefaultProfilePictureGenerator struct{ service *profilePictureService }

func (adapter jobDefaultProfilePictureGenerator) EnsureDefaultProfilePicture(ctx context.Context, id model.UserID) (model.FileEntryID, error) {
	return adapter.service.EnsureDefaultProfilePicture(ctx, id)
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

func (adapter jobMailDeliveryRecorder) RecordJobMailDelivery(ctx context.Context, metric appjobs.MailDeliveryMetric) {
	if adapter.recorder == nil {
		return
	}
	adapter.recorder.RecordMailDelivery(ctx, MailDeliveryMetric{
		TemplateKey: metric.TemplateKey, State: metric.State, OutcomeCode: metric.OutcomeCode,
		ProcessingLatency: metric.ProcessingLatency,
	})
}

func (adapter jobMailDeliveryRecorder) RecordJobMailAttempt(ctx context.Context, metric appjobs.MailAttemptMetric) {
	if adapter.recorder == nil {
		return
	}
	adapter.recorder.RecordMailAttempt(ctx, MailAttemptMetric{TemplateKey: metric.TemplateKey, State: metric.State})
}

type jobMailHealth struct{ health *MailHealth }

func (adapter jobMailHealth) SetJobMailHealth(code string) {
	if adapter.health != nil {
		adapter.health.set(code)
	}
}

func jobMailDeliveryIsRelevant(ctx context.Context, delivery *model.MailDelivery) (bool, error) {
	relevance, err := evaluateMailDeliveryRelevance(ctx, delivery)
	return relevance == mailDeliveryRelevant, err
}

type onboardingImportJobs struct{ service *onboardingImportService }

func (adapter onboardingImportJobs) PreviewProgression(ctx context.Context, id model.OnboardingImportID) (string, error) {
	err := adapter.service.previewProgression(ctx, id)
	if err == nil {
		return "", nil
	}
	if failure, ok := As(err); ok {
		switch failure.Code() {
		case "authorization.denied", "authentication.invalid_token":
			return "student_progression.authorization_lost", err
		case "student_progression.target_conflict", "student_progression.lineage_conflict", "student_progression.effective_date_conflict", "student_progression.roster_too_large":
			return failure.Code(), err
		}
	} else if store.IsNotFound(err) || store.IsConflict(err) {
		return "student_progression.target_conflict", err
	}
	return "", err
}

func (adapter onboardingImportJobs) Parse(ctx context.Context, id model.OnboardingImportID) (string, error) {
	err := adapter.service.parse(ctx, id)
	if err == nil {
		return "", nil
	}
	if failure, ok := As(err); ok && (failure.Code() == "authentication.invalid_token" || failure.Code() == "authorization.denied") {
		return "onboarding_import.authorization_lost", err
	}
	if errors.Is(err, errOnboardingImportInvalidFile) {
		return "onboarding_import.invalid_file", err
	}
	return "", err
}

func (adapter onboardingImportJobs) Get(ctx context.Context, id model.OnboardingImportID) (*store.OnboardingImport, error) {
	return adapter.service.imports.GetOnboardingImport(ctx, id)
}

func (adapter onboardingImportJobs) Execute(ctx context.Context, id model.OnboardingImportID, after int,
	checkpoint func(int, int) error,
) error {
	return adapter.service.execute(ctx, id, after, checkpoint)
}

func (adapter onboardingImportJobs) Fail(ctx context.Context, id model.OnboardingImportID, code string) error {
	_, err := adapter.service.imports.FailOnboardingImport(ctx, id, code, model.TimeUTC(adapter.service.now()))
	return err
}

type examSittingSealingJobUseCases struct {
	sittings examSittingUseCases
	attempts examAttemptUseCases
	jobs     store.JobStore
	now      func() time.Time
	newID    func() model.JobID
}

func (useCases examSittingSealingJobUseCases) ListExamSittingSealTargetsFromJob(ctx context.Context,
	sittingID model.ExamSittingID, after model.ExamAttemptID, limit int,
) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	return useCases.attempts.ListAutomaticSealTargets(ctx, sittingID, after, limit)
}

func (useCases examSittingSealingJobUseCases) SealExamAttemptForSittingCloseFromJob(ctx context.Context,
	target store.ExamSubmissionAutomaticSealTarget, jobID model.JobID, attemptID model.JobAttemptID,
) (examattempt.AutomaticSubmissionResult, error) {
	return useCases.attempts.SealForSittingClose(ctx, examattempt.SystemCall{JobID: jobID, AttemptID: attemptID}, target)
}

func (useCases examSittingSealingJobUseCases) FinishExamSittingSealingFromJob(ctx context.Context,
	sittingID model.ExamSittingID, jobID model.JobID, attemptID model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	result, err := useCases.sittings.FinishSealing(ctx, examsitting.SystemCall{JobID: jobID, AttemptID: attemptID}, sittingID)
	return &result, err
}

func (useCases examSittingSealingJobUseCases) EnqueueExamSittingSealingContinuationFromJob(ctx context.Context,
	sittingID model.ExamSittingID, parentJobID model.JobID,
) error {
	if useCases.jobs == nil || useCases.now == nil || useCases.newID == nil || !sittingID.IsValid() || !parentJobID.IsValid() {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", nil)
	}
	document, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", err)
	}
	at := model.TimeUTC(useCases.now())
	job, err := model.NewJobWithDedupePolicy(useCases.newID(), model.JobTypeExamSittingSealing, 1, document,
		"exam-sitting-sealing-continuation:"+parentJobID.String(), model.JobDedupePermanent, at, at, 8)
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting_sealing", "continuation", err)
	}
	_, _, err = useCases.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}
