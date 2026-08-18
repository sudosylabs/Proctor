// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
)

type examinationConstruction struct {
	authoring        examUseCases
	revisions        examRevisionUseCases
	sittings         examSittingUseCases
	attempts         examAttemptUseCases
	reviews          examReviewUseCases
	resources        examResourceUseCases
	corrections      examCorrectionUseCases
	starterWorkspace examStarterWorkspaceUseCases
}

// applicationFoundation holds the shared mechanics constructed before any
// focused application service. It is a private construction result, not a
// runtime service locator.
type applicationFoundation struct {
	mfa         *mfaMechanics
	hasher      *passwordHasher
	attempts    *authenticationAttemptAccounting
	invalidator *authenticationCacheInvalidator
	realtime    *realtimeService
	audit       *auditService
	mailHealth  *MailHealth
}

type identityConstruction struct {
	authentication                    *authenticationService
	desktopAuthorization              *desktopAuthorizationService
	selfSessions                      *selfSessionService
	externalAuthentication            *externalAuthenticationService
	mfaApplication                    *mfaApplicationService
	accountTokens                     *accountTokenService
	invitations                       *invitationService
	personalAccessTokenAdministration *personalAccessTokenAdministrationService
}

type accessAcademicConstruction struct {
	authorization        *accessControlService
	capabilities         accessPolicyCapabilitySource
	accessPolicies       *accessPolicyService
	academicUnits        *academicUnitQueryService
	academicUnitCommands *academicUnitCommandService
	institutions         *institutionService
	programmes           *programmeService
	programmeLevels      *programmeLevelService
	academicPeriods      *academicPeriodService
	classes              *classService
	affiliations         *affiliationService
	academicUnitMembers  *academicUnitMemberService
	classMembers         *classMemberService
}

type profileFileConstruction struct {
	userProfiles    *userProfileService
	userSettings    *userSettingsService
	profilePictures *profilePictureService
}

type jobConstruction struct {
	runtime    *jobengine.Engine
	operations *jobOperationsService
	mail       *mailService
}

type administrationConstruction struct {
	accountStates          *accountStateService
	sessionAdministrations *sessionAdministrationService
	roles                  *roleService
	roleBindings           *roleBindingService
	auditListings          *auditListingService
	bootstrap              *bootstrapService
}

func validateApplicationDependencies(deps Dependencies) error {
	if deps.Store == nil {
		return errors.New("store is required")
	}
	if deps.Cache == nil {
		return errors.New("cache is required")
	}
	if deps.MailDeliverySender == nil {
		return errors.New("mail delivery sender is required")
	}
	if deps.MailTemplateRenderer == nil {
		return errors.New("mail template renderer is required")
	}
	if deps.Registry == nil {
		return errors.New("external provider registry is required")
	}
	if deps.FileContent == nil {
		return errors.New("file content is required")
	}
	if deps.NodeID == "" {
		return errors.New("node ID is required")
	}
	if deps.AuthenticationDiagnostics == nil {
		return errors.New("authentication diagnostics is required")
	}
	if deps.RealtimeDiagnostics == nil {
		return errors.New("realtime diagnostics is required")
	}
	if deps.RecoveryDiagnostics == nil {
		return errors.New("recovery diagnostics is required")
	}
	return nil
}

func constructApplicationFoundation(deps Dependencies) (applicationFoundation, error) {
	mfa, err := newMFAMechanics(deps.MFA)
	if err != nil {
		return applicationFoundation{}, err
	}
	hasher, err := newPasswordHasher(deps.Password)
	if err != nil {
		return applicationFoundation{}, err
	}
	attempts, err := newAuthenticationAttemptAccounting(deps.Cache)
	if err != nil {
		return applicationFoundation{}, err
	}
	invalidator, err := newAuthenticationCacheInvalidator(
		deps.Cache,
		deps.AuthenticationDiagnostics,
	)
	if err != nil {
		return applicationFoundation{}, err
	}
	realtimeDelivery, err := apprealtime.New(
		invalidator,
		deps.RealtimeDiagnostics,
	)
	if err != nil {
		return applicationFoundation{}, err
	}
	realtime, err := newRealtimeServiceWithDelivery(
		realtimeDelivery,
		deps.RealtimeDiagnostics,
	)
	if err != nil {
		return applicationFoundation{}, err
	}
	audit, err := newAuditService(deps.Store.Audit(), deps.Store.Institution(), deps.NodeID)
	if err != nil {
		return applicationFoundation{}, err
	}
	return applicationFoundation{
		mfa:         mfa,
		hasher:      hasher,
		attempts:    attempts,
		invalidator: invalidator,
		realtime:    realtime,
		audit:       audit,
		mailHealth:  newMailHealth(deps.MailDeliverySender.Enabled()),
	}, nil
}

func assembleApplication(
	deps Dependencies,
	foundation applicationFoundation,
	identity identityConstruction,
	access accessAcademicConstruction,
	examinations examinationConstruction,
	profiles profileFileConstruction,
	jobs jobConstruction,
	administration administrationConstruction,
) *App {
	return &App{
		authentication:                    identity.authentication,
		desktopAuthorization:              identity.desktopAuthorization,
		selfSessions:                      identity.selfSessions,
		externalAuthentication:            identity.externalAuthentication,
		mfaApplication:                    identity.mfaApplication,
		accountTokens:                     identity.accountTokens,
		invitations:                       identity.invitations,
		personalAccessTokenAdministration: identity.personalAccessTokenAdministration,
		authorization:                     access.authorization,
		accessPolicies:                    access.accessPolicies,
		academicUnits:                     access.academicUnits,
		academicUnitCommands:              access.academicUnitCommands,
		institutions:                      access.institutions,
		programmes:                        access.programmes,
		programmeLevels:                   access.programmeLevels,
		academicPeriods:                   access.academicPeriods,
		classes:                           access.classes,
		affiliations:                      access.affiliations,
		academicUnitMembers:               access.academicUnitMembers,
		classMembers:                      access.classMembers,
		exams:                             examinations.authoring,
		examRevisions:                     examinations.revisions,
		examSittings:                      examinations.sittings,
		examAttempts:                      examinations.attempts,
		examReviews:                       examinations.reviews,
		examResources:                     examinations.resources,
		examCorrections:                   examinations.corrections,
		examStarterWorkspace:              examinations.starterWorkspace,
		userProfiles:                      profiles.userProfiles,
		userSettings:                      profiles.userSettings,
		profilePictures:                   profiles.profilePictures,
		accountStates:                     administration.accountStates,
		sessionAdministrations:            administration.sessionAdministrations,
		roles:                             administration.roles,
		roleBindings:                      administration.roleBindings,
		auditListings:                     administration.auditListings,
		bootstrap:                         administration.bootstrap,
		audit:                             foundation.audit,
		realtime:                          foundation.realtime,
		jobs:                              jobs.runtime,
		jobOperations:                     jobs.operations,
		mail:                              jobs.mail,
		mailSecretSealer:                  deps.MailSecretSealer,
		recentAuthenticationTTL:           deps.RecentAuthenticationTTL,
	}
}
