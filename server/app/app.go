// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
)

// App is the long-lived application facade. Construction receives only the
// explicit Dependencies bundle; infrastructure getters and platform location
// are not part of the public surface.
type App struct {
	authentication                    *authenticationService
	selfSessions                      *selfSessionService
	externalAuthentication            *externalAuthenticationService
	mfaApplication                    *mfaApplicationService
	accountTokens                     *accountTokenService
	personalAccessTokenAdministration *personalAccessTokenAdministrationService
	authorization                     *accessControlService
	academicUnits                     *academicUnitQueryService
	academicUnitCommands              *academicUnitCommandService
	institutions                      *institutionService
	programmes                        *programmeService
	programmeLevels                   *programmeLevelService
	academicPeriods                   *academicPeriodService
	classes                           *classService
	affiliations                      *affiliationService
	academicUnitMembers               *academicUnitMemberService
	classMembers                      *classMemberService
	exams                             examUseCases
	examRevisions                     examRevisionUseCases
	examSittings                      examSittingUseCases
	examAttempts                      examAttemptUseCases
	examReviews                       examReviewUseCases
	examResources                     examResourceUseCases
	examCorrections                   examCorrectionUseCases
	examStarterWorkspace              examStarterWorkspaceUseCases
	userProfiles                      *userProfileService
	userSettings                      *userSettingsService
	profilePictures                   *profilePictureService
	accountStates                     *accountStateService
	sessionAdministrations            *sessionAdministrationService
	roles                             *roleService
	roleBindings                      *roleBindingService
	auditListings                     *auditListingService
	bootstrap                         *bootstrapService
	audit                             *auditService
	realtime                          *realtimeService
	jobs                              *jobengine.Engine
	jobOperations                     *jobOperationsService
	mail                              *mailService
	mailSecretSealer                  *secretseal.Sealer

	// Cross-cutting policy and ports still used by App-method facades that
	// have not yet been extracted into focused services.
	recentAuthenticationTTL time.Duration
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
	if err := validateApplicationDependencies(deps); err != nil {
		return nil, err
	}
	foundation, err := constructApplicationFoundation(deps)
	if err != nil {
		return nil, err
	}
	identity, err := constructIdentity(deps, foundation)
	if err != nil {
		return nil, err
	}
	access, err := constructAccessAndAcademics(deps, foundation)
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
	jobs, err := constructJobs(deps, foundation, access, examinations, profiles)
	if err != nil {
		return nil, err
	}
	administration := constructAdministration(deps, foundation, access)
	return assembleApplication(deps, foundation, identity, access, examinations, profiles, jobs, administration), nil
}

func (a *App) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, error) {
	return a.authorization.Can(ctx, principal, action, resource)
}

// Authorize records the current allow or deny decision durably and fails
// closed if either policy resolution or decision audit fails.
func (a *App) Authorize(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) error {
	return a.authorization.Authorize(ctx, principal, action, resource, metadata)
}

// ErrAuthenticationCacheMiss and ErrAuthenticationCacheNotStored are the
// transport-neutral sentinels authentication uses for disposable cache
// outcomes. Composition adapters translate infrastructure cache errors into
// these values so authentication never imports platform.
var (
	ErrAuthenticationCacheMiss      = errors.New("authentication cache: key not found")
	ErrAuthenticationCacheNotStored = errors.New("authentication cache: conditional write not applied")
)
