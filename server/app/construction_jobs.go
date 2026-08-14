// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func constructJobs(
	deps Dependencies,
	foundation applicationFoundation,
	access accessAcademicConstruction,
	profiles profileFileConstruction,
) (jobConstruction, error) {
	if deps.Store.Job() == nil {
		return jobConstruction{}, nil
	}

	defaultJobs := &defaultProfilePictureJobProposer{jobs: deps.Store.Job()}
	defaultHandler := defaultProfilePictureHandler{generator: profiles.profilePictures}
	reconciliationHandler := defaultProfilePictureReconciliationHandler{
		users: deps.Store.User(), defaults: defaultJobs, now: time.Now,
	}
	purgeHandler := newFilePurgeExpiredContentHandler(deps.Store.File(), deps.FileContent)
	descriptors := []jobengine.Descriptor{
		defaultProfilePictureDescriptor(defaultHandler),
		defaultProfilePictureReconciliationDescriptor(reconciliationHandler),
		filePurgeExpiredContentDescriptor(purgeHandler),
		commandOutcomeCleanupDescriptor(commandOutcomeCleanupHandler{outcomes: deps.Store.CommandOutcome()}),
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
	}
	runtime, err := jobengine.New(jobengine.Config{
		Store: deps.Store.Job(), Descriptors: descriptors, NodeID: deps.NodeID,
		Diagnostics: deps.RecoveryDiagnostics,
		Policy:      jobengine.Policy{PollInterval: 500 * time.Millisecond},
		Recurrences: recurrences,
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

func jobRetentionPolicies(descriptors []jobengine.Descriptor) []store.JobRetentionPolicy {
	policies := make([]store.JobRetentionPolicy, 0, len(descriptors))
	for _, descriptor := range descriptors {
		policies = append(policies, store.JobRetentionPolicy{
			Type: descriptor.Type, SucceededCanceledAge: descriptor.SuccessRetention, FailedAge: descriptor.FailureRetention,
		})
	}
	return policies
}
