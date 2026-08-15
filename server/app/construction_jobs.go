// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func constructJobs(
	deps Dependencies,
	foundation applicationFoundation,
	access accessAcademicConstruction,
	examinations examinationConstruction,
	profiles profileFileConstruction,
) (jobConstruction, error) {
	if deps.Store.Job() == nil {
		return jobConstruction{}, nil
	}
	if examinations.sittings == nil {
		return jobConstruction{}, errors.New("Exam Sitting use cases are required when Jobs are enabled")
	}

	defaultJobs := &defaultProfilePictureJobProposer{jobs: deps.Store.Job()}
	definitions := buildApplicationJobDefinitions(deps, examinations, profiles, defaultJobs)
	runtime, err := jobengine.New(jobengine.Config{
		Store: deps.Store.Job(), Descriptors: definitions.descriptors, NodeID: deps.NodeID,
		Diagnostics: deps.RecoveryDiagnostics,
		Policy:      jobengine.Policy{PollInterval: 500 * time.Millisecond},
		Recurrences: definitions.recurrences,
	})
	if err != nil {
		return jobConstruction{}, err
	}
	defaultJobs.wake = runtime.Wake
	profiles.profilePictures.reads.defaultJobs = defaultJobs

	operations, err := newJobOperationsService(
		runtime,
		jobOperationsAuthorization{authorization: access.authorization, institutions: deps.Store.Institution()},
		mutationAuditAdapter{audit: foundation.audit}, time.Now,
	)
	if err != nil {
		return jobConstruction{}, err
	}
	return jobConstruction{runtime: runtime, operations: operations}, nil
}

type applicationJobDefinitions struct {
	descriptors []jobengine.Descriptor
	recurrences []jobengine.Recurrence
}

// buildApplicationJobDefinitions keeps the complete durable-work recipe
// inspectable without constructing or starting the runtime. Composition tests
// use the same definition graph that production passes to the Job engine.
func buildApplicationJobDefinitions(
	deps Dependencies,
	examinations examinationConstruction,
	profiles profileFileConstruction,
	defaultJobs *defaultProfilePictureJobProposer,
) applicationJobDefinitions {
	defaultHandler := defaultProfilePictureHandler{generator: profiles.profilePictures}
	reconciliationHandler := defaultProfilePictureReconciliationHandler{
		users: deps.Store.User(), defaults: defaultJobs, now: time.Now,
	}
	purgeHandler := newFilePurgeExpiredContentHandler(deps.Store.File(), deps.FileContent, deps.Store.ExamStarterWorkspace(), deps.FileContent)
	lifecycleUseCases := examSittingLifecycleJobUseCases{sittings: examinations.sittings}
	descriptors := []jobengine.Descriptor{
		defaultProfilePictureDescriptor(defaultHandler),
		defaultProfilePictureReconciliationDescriptor(reconciliationHandler),
		filePurgeExpiredContentDescriptor(purgeHandler),
		commandOutcomeCleanupDescriptor(commandOutcomeCleanupHandler{outcomes: deps.Store.CommandOutcome()}),
		examSittingLifecycleDescriptor(examSittingLifecycleHandler{reconciler: lifecycleUseCases}),
		examSittingLifecycleRecoveryDescriptor(examSittingLifecycleRecoveryHandler{service: lifecycleUseCases}),
	}
	retentionPolicies := jobRetentionPolicies(descriptors)
	cleanupHandler := jobHistoryCleanupHandler{jobs: deps.Store.Job(), policies: append(retentionPolicies, store.JobRetentionPolicy{
		Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour,
	})}
	descriptors = append(descriptors, jobHistoryCleanupDescriptor(cleanupHandler))
	recurrences := []jobengine.Recurrence{
		{Name: "profile-picture-default-reconciliation", Proposer: defaultProfilePictureReconciliationJobProposer{jobs: deps.Store.Job(), now: time.Now}},
		{Name: "file-purge-expired-content", Proposer: filePurgeExpiredContentProposer{jobs: deps.Store.Job(), now: time.Now}},
		{Name: "job-history-cleanup", Proposer: jobHistoryCleanupProposer{jobs: deps.Store.Job(), now: time.Now}},
		{Name: "command-outcome-cleanup", Proposer: commandOutcomeCleanupProposer{jobs: deps.Store.Job(), now: time.Now}},
		{Name: "exam-sitting-lifecycle-recovery", Proposer: examSittingLifecycleRecoveryProposer{jobs: deps.Store.Job(), now: time.Now}},
	}
	return applicationJobDefinitions{descriptors: descriptors, recurrences: recurrences}
}

func jobRetentionPolicies(descriptors []jobengine.Descriptor) []store.JobRetentionPolicy {
	policies := make([]store.JobRetentionPolicy, 0, len(descriptors))
	for _, descriptor := range descriptors {
		policies = append(policies, store.JobRetentionPolicy{
			Type: descriptor.Type, SucceededCanceledAge: descriptor.SuccessRetention, FailedAge: descriptor.FailureRetention,
		})
	}
	return policies
}
