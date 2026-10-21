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
	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	examreview "github.com/sudosylabs/proctor/server/app/exam/review"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
)

type examinationConstruction struct {
	authoring              *examengine.Authoring
	revisions              *examengine.Publication
	sittings               *examsitting.Service
	sittingMail            *appmail.SittingComposer
	sittingMailPreparation sittingScheduleMailPreparationAdapter
	attempts               *examattempt.Service
	reviews                *examreview.Service
	records                *examengine.Records
	exports                *examengine.Exports
	resources              *examresource.Service
	corrections            *examcorrection.Service
	starterWorkspace       *examworkspace.Service
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
	mail        *appmail.Composer
	mailHealth  *MailHealth
}

type identityConstruction struct {
	mail                              *appmail.Composer
	authentication                    *authenticationService
	desktopAuthorization              *desktopAuthorizationService
	desktopRegistrations              *desktopRegistrationService
	browserInvitations                *browserInvitationService
	selfSessions                      *selfSessionService
	externalAuthentication            *externalAuthenticationService
	authenticationMethods             *authenticationMethodService
	mfaApplication                    *mfaApplicationService
	accountTokens                     *accountTokenService
	publicRegistration                *publicRegistrationService
	invitations                       *invitationService
	onboardingImports                 *onboardingImportService
	personalAccessTokenAdministration *personalAccessTokenAdministrationService
}

type accessAcademicConstruction struct {
	authorization        *accessControlService
	capabilities         accessPolicyCapabilitySource
	accessPolicies       *accessPolicyService
	retentionPolicy      *retentionPolicyService
	desktopCompatibility *desktopCompatibilityService
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
	accountStates                 *accountStateService
	sessionAdministrations        *sessionAdministrationService
	roles                         *roleService
	roleBindings                  *roleBindingService
	auditListings                 *auditListingService
	bootstrap                     *bootstrapService
	academicAdministrationBatches *academicAdministrationBatchService
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
	mfa, err := newMFAMechanics(deps.MFA, deps.MFASecretSealer)
	if err != nil {
		return applicationFoundation{}, err
	}
	if deps.MFASecretSealer != nil {
		if err = validateMFASecretKeys(context.Background(), deps.Store.MFA(), deps.MFASecretSealer); err != nil {
			return applicationFoundation{}, err
		}
	}
	hasher, err := newPasswordHasher(deps.Password, deps.PasswordWorkRecorder)
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
	realtime.attempts = deps.Store.ExamAttempt()
	audit, err := newAuditService(deps.Store.Audit(), deps.Store.Institution(), deps.NodeID)
	if err != nil {
		return applicationFoundation{}, err
	}
	mail, err := newDirectMailPreparer(deps.MailTemplateRenderer, deps.MailDeliverySender, deps.MailSecretSealer)
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
		mail:        mail,
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
		authenticationService:                    identity.authentication,
		desktopAuthorizationService:              identity.desktopAuthorization,
		desktopRegistrationService:               identity.desktopRegistrations,
		browserInvitationService:                 identity.browserInvitations,
		selfSessionService:                       identity.selfSessions,
		externalAuthenticationService:            identity.externalAuthentication,
		authenticationMethodService:              identity.authenticationMethods,
		mfaApplicationService:                    identity.mfaApplication,
		accountTokenService:                      identity.accountTokens,
		publicRegistrationService:                identity.publicRegistration,
		invitationService:                        identity.invitations,
		onboardingImportService:                  identity.onboardingImports,
		personalAccessTokenAdministrationService: identity.personalAccessTokenAdministration,
		accessPolicyService:                      access.accessPolicies,
		retentionPolicyService:                   access.retentionPolicy,
		desktopCompatibilityService:              access.desktopCompatibility,
		academicUnitQueryService:                 access.academicUnits,
		academicUnitCommandService:               access.academicUnitCommands,
		institutionService:                       access.institutions,
		programmeService:                         access.programmes,
		programmeLevelService:                    access.programmeLevels,
		academicPeriodService:                    access.academicPeriods,
		classService:                             access.classes,
		affiliationService:                       access.affiliations,
		academicUnitMemberService:                access.academicUnitMembers,
		classMemberService:                       access.classMembers,
		examAuthoringService:                     examinations.authoring,
		examPublicationService:                   examinations.revisions,
		examSittingService:                       examinations.sittings,
		examAttemptService:                       examinations.attempts,
		examReviewService:                        examinations.reviews,
		examRecordsService:                       examinations.records,
		examExportsService:                       examinations.exports,
		examResourceService:                      examinations.resources,
		examCorrectionService:                    examinations.corrections,
		examStarterWorkspaceService:              examinations.starterWorkspace,
		userProfileService:                       profiles.userProfiles,
		userSettingsService:                      profiles.userSettings,
		profilePictureService:                    profiles.profilePictures,
		accountStateService:                      administration.accountStates,
		sessionAdministrationService:             administration.sessionAdministrations,
		roleService:                              administration.roles,
		roleBindingService:                       administration.roleBindings,
		auditListingService:                      administration.auditListings,
		bootstrapService:                         administration.bootstrap,
		academicAdministrationBatchService:       administration.academicAdministrationBatches,
		realtimeService:                          foundation.realtime,
		realtimeAdmissionService: &realtimeAdmissionService{
			principals: identity.authentication, exams: examinations.authoring, sittings: examinations.sittings,
			authorization: access.authorization, operational: deps.OperationalRecorder,
		},
		jobs:                 jobs.runtime,
		jobOperationsService: jobs.operations,
		mailService:          jobs.mail,
	}
}
