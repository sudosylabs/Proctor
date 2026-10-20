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

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examreview "github.com/sudosylabs/proctor/server/app/exam/review"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

// App is the long-lived application surface. It embeds each focused service
// that owns public use cases, so its method set is their exported methods and
// it declares no use case of its own. Construction receives only the explicit
// Dependencies bundle; infrastructure getters and platform location are not
// part of the public surface.
type App struct {
	*authenticationService
	*desktopAuthorizationService
	*desktopRegistrationService
	*browserInvitationService
	*selfSessionService
	*externalAuthenticationService
	*authenticationMethodService
	*mfaApplicationService
	*accountTokenService
	*publicRegistrationService
	*personalAccessTokenAdministrationService
	accessControlService *accessControlService
	*accessPolicyService
	*retentionPolicyService
	*desktopCompatibilityService
	*academicUnitQueryService
	*academicUnitCommandService
	*institutionService
	*programmeService
	*programmeLevelService
	*academicPeriodService
	*classService
	*affiliationService
	*academicUnitMemberService
	*classMemberService
	*invitationService
	*onboardingImportService
	exams                examUseCases
	examRevisions        *examengine.Publication
	examSittings         examSittingUseCases
	examRecords          *examengine.Records
	examExports          *examengine.Exports
	examAttempts         *examattempt.Service
	examReviews          *examreview.Service
	examResources        examResourceUseCases
	examCorrections      examCorrectionUseCases
	examStarterWorkspace examStarterWorkspaceUseCases
	*userProfileService
	*userSettingsService
	*profilePictureService
	*accountStateService
	*sessionAdministrationService
	*roleService
	*roleBindingService
	*auditListingService
	*bootstrapService
	*academicAdministrationBatchService
	*realtimeService
	jobs *jobengine.Engine
	*jobOperationsService
	*mailService
	operational OperationalRecorder
}

// Jobs returns the root-owned durable Job runtime, or nil for lifecycle-only
// test graphs that intentionally provide no Job store.
func (a *App) Jobs() *jobengine.Engine {
	if a == nil {
		return nil
	}
	return a.jobs
}

// New constructs the application graph from explicit dependencies. Only the
// module-root composition package should call this. The ordered recipe remains
// visible here while private construction modules retain each cohesive slice's
// projection and wiring knowledge.
func New(deps Dependencies) (*App, error) {
	if deps.OperationalRecorder == nil {
		deps.OperationalRecorder = nopOperationalRecorder{}
	}
	if err := validateApplicationDependencies(deps); err != nil {
		return nil, err
	}
	foundation, err := constructApplicationFoundation(deps)
	if err != nil {
		return nil, err
	}
	access, err := constructAccessAndAcademics(deps, foundation)
	if err != nil {
		return nil, err
	}
	identity, err := constructIdentity(deps, foundation, access.authorization, access.capabilities, access.desktopCompatibility)
	if err != nil {
		return nil, err
	}
	examinations, err := constructExaminations(deps, foundation, access)
	if err != nil {
		return nil, err
	}
	profiles, err := constructProfilesAndFiles(deps, foundation, access)
	if err != nil {
		return nil, err
	}
	administration := constructAdministration(deps, foundation, access, identity)
	identity.onboardingImports, err = newOnboardingImportService(deps, identity.invitations, administration.academicAdministrationBatches,
		identity.authentication, invitationAuthorizationAdapter{authorization: access.authorization}, mutationAuditAdapter{audit: foundation.audit})
	if err != nil {
		return nil, err
	}
	jobs, err := constructJobs(deps, foundation, access, identity, examinations, profiles)
	if err != nil {
		return nil, err
	}
	return assembleApplication(deps, foundation, identity, access, examinations, profiles, jobs, administration), nil
}

// Authorize records the current allow or deny decision durably and fails
// closed if either policy resolution or decision audit fails.
func (a *App) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	err := a.accessControlService.Authorize(ctx, invocation, action, resource)
	a.recordOperational("authorization", "decision", err)
	return err
}

func (a *App) recordOperational(subsystem, event string, err error) {
	if a != nil && a.operational != nil {
		a.operational.RecordOperationalEvent(operationalEvent(subsystem, event, err))
	}
}

// ErrAuthenticationCacheMiss and ErrAuthenticationCacheNotStored are the
// transport-neutral sentinels authentication uses for disposable cache
// outcomes. Composition adapters translate infrastructure cache errors into
// these values so authentication never imports platform.
var (
	ErrAuthenticationCacheMiss      = errors.New("authentication cache: key not found")
	ErrAuthenticationCacheNotStored = errors.New("authentication cache: conditional write not applied")
)
