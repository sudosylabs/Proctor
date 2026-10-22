// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/exam/correction"
	"github.com/sudosylabs/proctor/server/app/exam/resource"
	"github.com/sudosylabs/proctor/server/app/exam/review"
	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/app/exam/workspace"

	"github.com/sudosylabs/proctor/server/app/job"
)

// Exam services live in child modules whose types share names such as
// Service; these aliases give each embedded field a distinct name.
type (
	examAuthoringService        = exam.Authoring
	examPublicationService      = exam.Publication
	examRecordsService          = exam.Records
	examExportsService          = exam.Exports
	examSittingService          = sitting.Service
	examAttemptService          = attempt.Service
	examReviewService           = review.Service
	examResourceService         = resource.Service
	examCorrectionService       = correction.Service
	examStarterWorkspaceService = workspace.Service
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
	*examAuthoringService
	*examPublicationService
	*examSittingService
	*examRecordsService
	*examExportsService
	*examAttemptService
	*examReviewService
	*examResourceService
	*examCorrectionService
	*examStarterWorkspaceService
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
	*realtimeAdmissionService
	jobs *job.Engine
	*jobOperationsService
	*mailService
}

// Jobs returns the root-owned durable Job runtime, or nil for lifecycle-only
// test graphs that intentionally provide no Job store.
func (a *App) Jobs() *job.Engine {
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
	identity.onboardingImports, err = constructOnboardingImports(deps, identity, administration, access, foundation)
	if err != nil {
		return nil, err
	}
	jobs, err := constructJobs(deps, foundation, access, identity, examinations, profiles)
	if err != nil {
		return nil, err
	}
	return assembleApplication(deps, foundation, identity, access, examinations, profiles, jobs, administration), nil
}

// ErrAuthenticationCacheMiss and ErrAuthenticationCacheNotStored are the
// transport-neutral sentinels authentication uses for disposable cache
// outcomes. Composition adapters translate infrastructure cache errors into
// these values so authentication never imports platform.
var (
	ErrAuthenticationCacheMiss      = errors.New("authentication cache: key not found")
	ErrAuthenticationCacheNotStored = errors.New("authentication cache: conditional write not applied")
)

// constructOnboardingImports projects the onboarding import stores. A
// deliberately reduced lifecycle test catalog omits whole capability
// families and so constructs no onboarding imports; production catalogs
// project every target store together.
func constructOnboardingImports(deps Dependencies, identity identityConstruction, administration administrationConstruction,
	access accessAcademicConstruction, foundation applicationFoundation,
) (*onboardingImportService, error) {
	if deps.Store == nil {
		return nil, errors.New("onboarding import store catalog is required")
	}
	catalog := deps.Store
	if catalog.OnboardingImport() == nil {
		return nil, errors.New("onboarding import persistence is required")
	}
	if deps.FileContent == nil {
		return nil, errors.New("onboarding import file content is required")
	}
	if catalog.PersonalAccessToken() == nil || catalog.User() == nil {
		return nil, errors.New("onboarding import credential stores are required")
	}
	if catalog.Institution() == nil || catalog.AcademicUnit() == nil || catalog.Programme() == nil || catalog.ProgrammeLevel() == nil ||
		catalog.AcademicPeriod() == nil || catalog.Class() == nil || catalog.Role() == nil {
		return nil, nil
	}
	return newOnboardingImportService(onboardingImportServiceDependencies{Imports: catalog.OnboardingImport(), Content: deps.FileContent, Invitations: identity.invitations, AdministrationBatches: administration.academicAdministrationBatches, Authentication: identity.authentication, Authorization: invitationAuthorizationAdapter{authorization: access.authorization}, Audit: mutationAuditAdapter{audit: foundation.audit}, Tokens: catalog.PersonalAccessToken(), Users: catalog.User(), Institutions: catalog.Institution(), Units: catalog.AcademicUnit(), Programmes: catalog.Programme(), Levels: catalog.ProgrammeLevel(), Periods: catalog.AcademicPeriod(), Classes: catalog.Class(), Roles: catalog.Role(), Affiliations: catalog.Affiliation(), UnitMembers: catalog.AcademicUnitMember(), ClassMembers: catalog.ClassMember(), RoleBindings: catalog.RoleBinding(), Now: time.Now})
}
