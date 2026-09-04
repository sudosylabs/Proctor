// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package jobs owns Proctor's concrete durable Job definitions.
//
// The sibling app/job package is the generic execution engine. This package
// contains application-specific handlers, commands, schedules, and the single
// catalog that makes the enabled durable work visible in one place.
package jobs

import (
	"context"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type DefaultProfilePictureGenerator interface {
	EnsureDefaultProfilePicture(context.Context, model.UserID) (model.FileEntryID, error)
	ClassifyDefaultProfilePictureError(error) string
}

type JobEnqueuer interface {
	Enqueue(context.Context, *store.JobEnqueue) (*model.Job, bool, error)
}

// CatalogDependencies is deliberately capability-oriented. It does not expose
// the application or infrastructure composition roots to workers.
type CatalogDependencies struct {
	JobStore store.JobStore

	DefaultProfilePictureGenerator DefaultProfilePictureGenerator
	Users                          DefaultProfilePictureReconciliationUserLister
	DefaultProfilePictureJobs      ProfilePictureDefaultJobs

	Files                   FilePurgeStore
	FileContent             FileRevisionContentPurger
	StarterWorkspaces       StarterWorkspaceCleanupStore
	StarterWorkspaceContent StarterWorkspaceObjectPurger
	AttemptWorkspaces       AttemptWorkspaceCleanupStore
	AttemptWorkspaceContent AttemptWorkspaceObjectPurger
	CommandOutcomes         CommandOutcomeCleaner

	MailDeliveries         MailDeliveryLifecycleStore
	MailSender             appmail.Sender
	MailSealer             *secretseal.Sealer
	MailRecorder           MailDeliveryRecorder
	MailHealth             MailHealth
	MailRelevance          MailDeliveryRelevance
	SittingMailStore       SittingMailExpansionStore
	SittingMail            *appmail.SittingComposer
	MailCleanup            MailCleanupStore
	SittingMailMaintenance SittingMailMaintenanceStore
	MailRekey              MailRekeyJobStore

	Invitations       InvitationMaintenanceStore
	OnboardingImports OnboardingImportMaintenanceStore
	OnboardingFiles   OnboardingImportMaintenanceFiles

	ExamSittingLifecycle ExamSittingLifecycleRecoveryUseCases
	ExamSittingSealing   ExamSittingSealingService
	Onboarding           OnboardingImportService

	Now func() time.Time
}

type Catalog struct {
	Descriptors []jobengine.Descriptor
	Recurrences []jobengine.Recurrence
}

func NewCatalog(deps CatalogDependencies) Catalog {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	purgeHandler := newFilePurgeExpiredContentHandler(
		deps.Files, deps.FileContent,
		deps.StarterWorkspaces, deps.StarterWorkspaceContent,
		deps.AttemptWorkspaces, deps.AttemptWorkspaceContent,
	)
	descriptors := []jobengine.Descriptor{
		defaultProfilePictureDescriptor(defaultProfilePictureHandler{generator: deps.DefaultProfilePictureGenerator}),
		defaultProfilePictureReconciliationDescriptor(defaultProfilePictureReconciliationHandler{users: deps.Users, defaults: deps.DefaultProfilePictureJobs, now: now}),
		filePurgeExpiredContentDescriptor(purgeHandler),
		commandOutcomeCleanupDescriptor(commandOutcomeCleanupHandler{outcomes: deps.CommandOutcomes}),
		mailDeliveryDescriptor(mailDeliveryHandler{deliveries: deps.MailDeliveries, sender: deps.MailSender, sealer: deps.MailSealer, recorder: deps.MailRecorder, health: deps.MailHealth, relevance: deps.MailRelevance, now: now}),
		mailCredentialDeliveryDescriptor(mailDeliveryHandler{deliveries: deps.MailDeliveries, sender: deps.MailSender, sealer: deps.MailSealer, recorder: deps.MailRecorder, health: deps.MailHealth, relevance: deps.MailRelevance, now: now}),
		sittingMailExpansionDescriptor(sittingMailExpansionHandler{sittings: deps.SittingMailStore, mail: deps.SittingMail}),
		mailCleanupDescriptor(mailCleanupHandler{mail: deps.MailCleanup, sittings: deps.SittingMailMaintenance, recorder: deps.MailRecorder}),
		mailRekeyDescriptor(mailRekeyHandler{mail: deps.MailRekey, sealer: deps.MailSealer}),
		invitationMaintenanceDescriptor(invitationMaintenanceHandler{invitations: deps.Invitations, imports: deps.OnboardingImports, content: deps.OnboardingFiles, now: now}),
		examSittingLifecycleDescriptor(examSittingLifecycleHandler{reconciler: deps.ExamSittingLifecycle}),
		examSittingSealingDescriptor(examSittingSealingHandler{service: deps.ExamSittingSealing}),
		examSittingLifecycleRecoveryDescriptor(examSittingLifecycleRecoveryHandler{service: deps.ExamSittingLifecycle}),
	}
	if deps.Onboarding != nil {
		descriptors = append(descriptors,
			onboardingImportParseDescriptor(onboardingImportParseHandler{service: deps.Onboarding}),
			studentProgressionPreviewDescriptor(studentProgressionPreviewHandler{service: deps.Onboarding}),
			onboardingImportExecuteDescriptor(onboardingImportExecuteHandler{service: deps.Onboarding}),
		)
	}
	descriptors = append(descriptors, jobHistoryCleanupDescriptor(jobHistoryCleanupHandler{
		jobs: deps.JobStore,
		policies: append(retentionPolicies(descriptors), store.JobRetentionPolicy{
			Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour,
		}),
	}))

	recurrences := []jobengine.Recurrence{
		{Name: "profile-picture-default-reconciliation", Proposer: defaultProfilePictureReconciliationJobProposer{jobs: deps.JobStore, now: now}},
		{Name: "file-purge-expired-content", Proposer: filePurgeExpiredContentProposer{jobs: deps.JobStore, now: now}},
		{Name: "job-history-cleanup", Proposer: jobHistoryCleanupProposer{jobs: deps.JobStore, now: now}},
		{Name: "command-outcome-cleanup", Proposer: commandOutcomeCleanupProposer{jobs: deps.JobStore, now: now}},
		{Name: "mail-cleanup", Proposer: mailCleanupProposer{jobs: deps.JobStore, now: now}},
		{Name: "invitation-maintenance", Proposer: invitationMaintenanceProposer{jobs: deps.JobStore, now: now}},
		{Name: "exam-sitting-lifecycle-recovery", Proposer: examSittingLifecycleRecoveryProposer{jobs: deps.JobStore, now: now}},
	}
	return Catalog{Descriptors: descriptors, Recurrences: recurrences}
}

func retentionPolicies(descriptors []jobengine.Descriptor) []store.JobRetentionPolicy {
	policies := make([]store.JobRetentionPolicy, 0, len(descriptors))
	for _, descriptor := range descriptors {
		policies = append(policies, store.JobRetentionPolicy{
			Type: descriptor.Type, SucceededCanceledAge: descriptor.SuccessRetention, FailedAge: descriptor.FailureRetention,
		})
	}
	return policies
}
