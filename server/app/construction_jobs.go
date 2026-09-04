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
	"fmt"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func constructJobs(
	deps Dependencies,
	foundation applicationFoundation,
	access accessAcademicConstruction,
	identity identityConstruction,
	examinations examinationConstruction,
	profiles profileFileConstruction,
) (jobConstruction, error) {
	if deps.Store.Job() == nil {
		return jobConstruction{}, nil
	}
	if examinations.sittings == nil || examinations.attempts == nil {
		return jobConstruction{}, errors.New("Examination lifecycle use cases are required when Jobs are enabled")
	}

	defaultJobs := appjobs.NewDefaultProfilePictureJobs(deps.Store.Job())
	// Enabled workers must be able to open every active payload before runtime
	// construction. Disabled workers suppress payloads before decryption, so a
	// deliberately absent or retired ring must not prevent that convergence.
	if deps.Store.Mail() != nil && deps.MailDeliverySender != nil && deps.MailDeliverySender.Enabled() {
		if err := validateActiveMailPayloadKeys(context.Background(), deps.Store.Mail(), deps.MailSecretSealer); err != nil {
			return jobConstruction{}, err
		}
	}
	mailHealth := foundation.mailHealth
	if mailHealth == nil {
		mailHealth = newMailHealth(deps.MailDeliverySender != nil && deps.MailDeliverySender.Enabled())
	}
	definitions := buildApplicationJobDefinitions(deps, identity, examinations, profiles, defaultJobs, mailHealth)
	runtime, err := jobengine.New(jobengine.Config{
		Store: deps.Store.Job(), Descriptors: definitions.descriptors, NodeID: deps.NodeID,
		Diagnostics:   deps.RecoveryDiagnostics,
		Recorder:      deps.JobRecorder,
		Policy:        jobengine.Policy{PollInterval: 500 * time.Millisecond},
		Recurrences:   definitions.recurrences,
		PeriodicTasks: definitions.periodicTasks,
	})
	if err != nil {
		return jobConstruction{}, err
	}
	defaultJobs.SetWake(runtime.Wake)
	if identity.onboardingImports != nil {
		identity.onboardingImports.wake = runtime.Wake
	}
	profiles.profilePictures.reads.defaultJobs = defaultJobs
	var mailService *mailService
	if deps.Store.Mail() != nil {
		mailService, err = newMailService(
			deps.Store.Mail(), deps.Store.User(),
			mailAuthorizationAdapter{authorization: access.authorization, institutions: deps.Store.Institution()},
			mailAuditAdapter{audit: foundation.audit}, foundation.attempts,
			deps.MailTemplateRenderer, deps.MailDeliverySender, deps.MailMetricsReader, deps.MailSecretSealer,
			deps.RecentAuthenticationTTL, time.Now,
		)
		if err != nil {
			return jobConstruction{}, err
		}
		mailService.rekeyJobs = deps.Store.Job()
		mailService.wake = runtime.Wake
	}

	operations, err := newJobOperationsService(
		runtime,
		jobOperationsAuthorization{authorization: access.authorization, institutions: deps.Store.Institution()},
		mutationAuditAdapter{audit: foundation.audit}, time.Now,
	)
	if err != nil {
		return jobConstruction{}, err
	}
	return jobConstruction{runtime: runtime, operations: operations, mail: mailService}, nil
}

type activeMailPayloadKeyStore interface {
	InspectKeyState(context.Context) (*store.MailKeyState, error)
}

func validateActiveMailPayloadKeys(ctx context.Context, persistence activeMailPayloadKeyStore, sealer interface {
	HasKey(string) bool
	PrimaryKeyID() string
}) error {
	if persistence == nil {
		return errors.New("mail payload-key persistence is unavailable")
	}
	state, err := persistence.InspectKeyState(ctx)
	if err != nil {
		return fmt.Errorf("inspect active mail payload keys: %w", err)
	}
	if state == nil {
		return errors.New("mail payload-key state is unavailable")
	}
	if state.RequiredPrimaryKeyID != "" {
		if sealer == nil {
			return fmt.Errorf("required mail primary key %s is unavailable", state.RequiredPrimaryKeyID)
		}
		if primary := sealer.PrimaryKeyID(); primary != state.RequiredPrimaryKeyID && !state.PrimaryPromotionAllowed {
			return fmt.Errorf("configured mail primary key %s does not match required primary key %s", primary, state.RequiredPrimaryKeyID)
		}
		if !sealer.HasKey(state.RequiredPrimaryKeyID) {
			return fmt.Errorf("required mail primary key %s is unavailable", state.RequiredPrimaryKeyID)
		}
	}
	if len(state.Active) == 0 {
		return nil
	}
	if sealer == nil {
		return errors.New("active mail payload key ring is unavailable")
	}
	for _, usage := range state.Active {
		if !sealer.HasKey(usage.KeyID) {
			return fmt.Errorf("active mail payload key %s is unavailable for %d references", usage.KeyID, usage.ActiveReferences)
		}
	}
	return nil
}

type applicationJobDefinitions struct {
	descriptors   []jobengine.Descriptor
	recurrences   []jobengine.Recurrence
	periodicTasks []jobengine.PeriodicTask
}

// buildApplicationJobDefinitions keeps the complete durable-work recipe
// inspectable without constructing or starting the runtime. Composition tests
// use the same definition graph that production passes to the Job engine.
func buildApplicationJobDefinitions(
	deps Dependencies,
	identity identityConstruction,
	examinations examinationConstruction,
	profiles profileFileConstruction,
	defaultJobs *appjobs.DefaultProfilePictureJobs,
	mailHealth *MailHealth,
) applicationJobDefinitions {
	if mailHealth == nil {
		mailHealth = newMailHealth(deps.MailDeliverySender != nil && deps.MailDeliverySender.Enabled())
	}
	lifecycleUseCases := examSittingLifecycleJobUseCases{sittings: examinations.sittings}
	sealingUseCases := examSittingSealingJobUseCases{sittings: examinations.sittings, attempts: examinations.attempts,
		jobs: deps.Store.Job(), now: time.Now, newID: model.NewJobID}
	var onboarding appjobs.OnboardingImportService
	if identity.onboardingImports != nil {
		onboarding = onboardingImportJobs{service: identity.onboardingImports}
	}
	catalog := appjobs.NewCatalog(appjobs.CatalogDependencies{
		JobStore:                       deps.Store.Job(),
		DefaultProfilePictureGenerator: jobDefaultProfilePictureGenerator{service: profiles.profilePictures}, Users: deps.Store.User(), DefaultProfilePictureJobs: defaultJobs,
		Files: deps.Store.File(), FileContent: deps.FileContent,
		StarterWorkspaces: deps.Store.ExamStarterWorkspace(), StarterWorkspaceContent: deps.FileContent,
		AttemptWorkspaces: deps.Store.ExamAttemptWorkspace(), AttemptWorkspaceContent: deps.FileContent,
		CommandOutcomes: deps.Store.CommandOutcome(),
		MailDeliveries:  deps.Store.Mail(), MailSender: deps.MailDeliverySender, MailSealer: deps.MailSecretSealer,
		MailRecorder: jobMailDeliveryRecorder{recorder: deps.MailDeliveryRecorder}, MailHealth: jobMailHealth{health: mailHealth},
		MailRelevance: jobMailDeliveryIsRelevant, SittingMailStore: deps.Store.ExamSitting(), SittingMail: examinations.sittingMail,
		MailCleanup: deps.Store.Mail(), SittingMailMaintenance: deps.Store.ExamSitting(), MailRekey: deps.Store.Mail(),
		Invitations: deps.Store.Invitation(), OnboardingImports: deps.Store.OnboardingImport(), OnboardingFiles: deps.FileContent,
		ExamSittingLifecycle: lifecycleUseCases, ExamSittingSealing: sealingUseCases, Onboarding: onboarding, Now: time.Now,
	})
	periodicTasks := []jobengine.PeriodicTask{
		{Name: examAttemptExpiryPeriodicTaskName, Interval: examAttemptExpiryScanInterval, Runner: examAttemptExpiryPeriodicRunner{attempts: examinations.attempts}},
		{Name: executionReconciliationPeriodicTaskName, Interval: executionReconciliationInterval, Runner: executionReconciliationPeriodicRunner{execution: examinations.execution}},
		{Name: "browser-authentication-maintenance", Interval: browserAuthenticationMaintenanceInterval,
			Runner: browserAuthenticationMaintenancePeriodicRunner{transactions: deps.Store.BrowserAuthentication()}},
		{Name: "external-authentication-maintenance", Interval: externalAuthenticationMaintenanceInterval,
			Runner: externalAuthenticationMaintenancePeriodicRunner{states: deps.Store.ExternalLoginState()}},
		{Name: "personal-access-token-maintenance", Interval: personalAccessTokenMaintenanceInterval,
			Runner: personalAccessTokenMaintenancePeriodicRunner{tokens: deps.Store.PersonalAccessToken()}},
		{Name: "exam-sitting-mail-reconciliation", Interval: sittingMailReconciliationInterval,
			Runner: sittingMailReconciliationPeriodicRunner{sittings: deps.Store.ExamSitting(), mail: examinations.sittingMailPreparation}},
	}
	if deps.Store.Mail() != nil && deps.MailDeliverySender != nil {
		periodicTasks = append(periodicTasks, jobengine.PeriodicTask{Name: "mail-maintenance-monitor", Interval: time.Minute, Runner: mailMaintenanceMonitor{mail: deps.Store.Mail(), sender: deps.MailDeliverySender, health: mailHealth, recorder: deps.MailDeliveryRecorder, now: time.Now}})
	}
	return applicationJobDefinitions{descriptors: catalog.Descriptors, recurrences: catalog.Recurrences, periodicTasks: periodicTasks}
}
