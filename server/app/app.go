// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package app owns Proctor's application use cases and orchestration.
package app

import (
	"context"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// App is the long-lived application facade. Construction receives only the
// explicit Dependencies bundle; infrastructure getters and platform location
// are not part of the public surface.
type App struct {
	store store.Store

	authentication         *AuthenticationService
	externalAuthentication *ExternalAuthenticationService
	mfa                    *MFAService
	authorization          *AuthorizationService
	academicUnits          *academicUnitQueryService
	academicUnitCommands   *academicUnitCommandService
	institutions           *institutionService
	programmes             *programmeService
	programmeLevels        *programmeLevelService
	academicPeriods        *academicPeriodService
	classes                *classService
	affiliations           *affiliationService
	academicUnitMembers    *academicUnitMemberService
	classMembers           *classMemberService
	userProfiles           *userProfileService
	profilePictures        *profilePictureService
	accountStates          *accountStateService
	sessionAdministrations *sessionAdministrationService
	roles                  *roleService
	roleBindings           *roleBindingService
	auditListings          *auditListingService
	bootstrap              *bootstrapService
	audit                  *AuditService
	realtime               *RealtimeService
	jobs                   *jobengine.Engine

	// Cross-cutting policy and ports still used by App-method facades that
	// have not yet been extracted into focused services.
	mailer                  AccountMailer
	cache                   authenticationCache
	nodeID                  string
	publicURL               string
	accountRecovery         AccountRecoveryPolicy
	personalAccessTokens    PersonalAccessTokenPolicy
	recentAuthenticationTTL time.Duration
	recoveryDiagnostics     recoveryDiagnostics
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
// module-root composition package should call this.
func New(deps Dependencies) (*App, error) {
	if deps.Store == nil {
		return nil, errors.New("store is required")
	}
	if deps.Cache == nil {
		return nil, errors.New("cache is required")
	}
	if deps.Mailer == nil {
		return nil, errors.New("mailer is required")
	}
	if deps.Registry == nil {
		return nil, errors.New("external provider registry is required")
	}
	if deps.FileContent == nil {
		return nil, errors.New("file content is required")
	}
	if deps.NodeID == "" {
		return nil, errors.New("node ID is required")
	}
	if deps.AuthenticationDiagnostics == nil {
		return nil, errors.New("authentication diagnostics is required")
	}
	if deps.RealtimeDiagnostics == nil {
		return nil, errors.New("realtime diagnostics is required")
	}
	if deps.RecoveryDiagnostics == nil {
		return nil, errors.New("recovery diagnostics is required")
	}

	mfa, err := newMFAService(deps.MFA)
	if err != nil {
		return nil, err
	}
	hasher, err := newPasswordHasher(deps.Password)
	if err != nil {
		return nil, err
	}

	// Expand PAT policy used both at bearer resolution and administration.
	patPolicy := deps.PersonalAccessToken
	authentication := newAuthenticationService(
		deps.Store,
		deps.Cache,
		hasher,
		mfa,
		deps.Sessions,
		deps.LoginRateLimit,
		patPolicy,
		deps.AuthenticationDiagnostics,
		time.Now,
	)
	audit := newAuditService(deps.Store, deps.NodeID)
	externalPolicy := deps.ExternalAuth
	if externalPolicy.PublicURL == "" {
		externalPolicy.PublicURL = deps.PublicURL
	}
	if externalPolicy.NodeID == "" {
		externalPolicy.NodeID = deps.NodeID
	}
	if externalPolicy.LoginRateLimit == (LoginRateLimitPolicy{}) {
		externalPolicy.LoginRateLimit = deps.LoginRateLimit
	}
	externalAuthentication := newExternalAuthenticationService(
		deps.Registry,
		deps.Store,
		deps.Cache,
		authentication,
		audit,
		externalPolicy,
		deps.AuthenticationDiagnostics,
		time.Now,
	)
	authorization := newAuthorizationService(deps.Store, audit)
	academicAuthorization := academicUnitAuthorization{
		authorization: authorization,
		institutions:  deps.Store.Institution(),
	}
	academicUnits := newAcademicUnitQueryService(
		deps.Store.AcademicUnit(), academicAuthorization,
	)
	realtime := newRealtimeService(authentication, deps.RealtimeDiagnostics)
	authentication.propagateAuthenticationCacheInvalidation =
		realtime.PropagateAuthenticationCacheInvalidation
	authentication.propagateSessionRevocation =
		realtime.PropagateSessionRevocation
	academicUnitCommands := newAcademicUnitCommandService(
		deps.Store.AcademicUnit(), academicAuthorization,
		mutationAuditAdapter{audit: audit},
		academicUnitRealtimeEffects{realtime: realtime},
		academicUnitEffectReporter{realtime: realtime},
		time.Now, model.NewId,
	)
	institutions := newInstitutionService(
		deps.Store.Institution(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now,
	)
	programmes := newProgrammeService(
		deps.Store.Programme(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	programmeLevels := newProgrammeLevelService(
		deps.Store.ProgrammeLevel(), deps.Store.Programme(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	academicPeriods := newAcademicPeriodService(
		deps.Store.AcademicPeriod(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	classes := newClassService(
		deps.Store.Class(), deps.Store.ProgrammeLevel(),
		deps.Store.Programme(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	affiliations := newAffiliationService(
		deps.Store.Affiliation(), deps.Store.ClassMember(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	academicUnitMembers := newAcademicUnitMemberService(
		deps.Store.AcademicUnitMember(), academicAuthorization,
		mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	classMembers := newClassMemberService(
		deps.Store.ClassMember(), deps.Store.Class(),
		academicAuthorization, mutationAuditAdapter{audit: audit}, time.Now, model.NewId,
	)
	userProfiles := newUserProfileService(
		deps.Store.User(),
		userProfileAuthorization{
			authorization: authorization,
			institutions:  deps.Store.Institution(),
			classMembers:  deps.Store.ClassMember(),
			now:           time.Now,
		},
		mutationAuditAdapter{audit: audit}, time.Now,
	)
	profilePictures := newProfilePictureService(
		deps.Store.User(), deps.Store.File(), deps.FileContent,
		userProfileAuthorization{
			authorization: authorization,
			institutions:  deps.Store.Institution(),
			classMembers:  deps.Store.ClassMember(),
			now:           time.Now,
		},
		mutationAuditAdapter{audit: audit}, profilePictureRealtimeEffects{realtime: realtime}, profilePictureEffectReporter{realtime: realtime},
		nil,
		time.Now,
	)
	var jobs *jobengine.Engine
	if deps.Store.Job() != nil {
		defaultJobs := &defaultProfilePictureJobProposer{jobs: deps.Store.Job()}
		defaultHandler := defaultProfilePictureHandler{generator: profilePictures}
		reconciliationHandler := defaultProfilePictureReconciliationHandler{
			users: deps.Store.User(), defaults: defaultJobs, now: time.Now,
		}
		purgeHandler := newFilePurgeExpiredContentHandler(deps.Store.File(), deps.FileContent)
		descriptors := []jobengine.Descriptor{
			defaultProfilePictureDescriptor(defaultHandler),
			defaultProfilePictureReconciliationDescriptor(reconciliationHandler),
			filePurgeExpiredContentDescriptor(purgeHandler),
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
		}
		jobs, err = jobengine.New(jobengine.Config{
			Store: deps.Store.Job(), Descriptors: descriptors, NodeID: deps.NodeID,
			Diagnostics: deps.RecoveryDiagnostics,
			Policy:      jobengine.Policy{PollInterval: 500 * time.Millisecond},
			Recurrences: recurrences,
		})
		if err != nil {
			return nil, err
		}
		defaultJobs.wake = jobs.Wake
		profilePictures.defaultJobs = defaultJobs
	}
	accountStates := newAccountStateService(
		deps.Store.User(),
		userProfileAuthorization{
			authorization: authorization,
			institutions:  deps.Store.Institution(),
			classMembers:  deps.Store.ClassMember(),
			now:           time.Now,
		},
		mutationAuditAdapter{audit: audit},
		accountStateRealtimeEffects{realtime: realtime},
		time.Now,
	)
	sessionAdministrations := newSessionAdministrationService(
		deps.Store.Session(),
		sessionAdministrationAuthorization{authorization: authorization},
		mutationAuditAdapter{audit: audit},
		sessionAdministrationRealtimeEffects{realtime: realtime},
		time.Now,
	)
	roles := newRoleService(
		deps.Store.Role(),
		roleAuthorization{authorization: authorization, institutions: deps.Store.Institution()},
		mutationAuditAdapter{audit: audit},
		roleRealtimeEffects{realtime: realtime},
		time.Now,
	)
	roleBindings := newRoleBindingService(
		deps.Store.RoleBinding(),
		deps.Store.Role(),
		roleAuthorization{authorization: authorization, institutions: deps.Store.Institution()},
		mutationAuditAdapter{audit: audit},
		roleBindingRealtimeEffects{realtime: realtime},
		time.Now,
	)
	auditListings := newAuditListingService(
		deps.Store.Audit(),
		auditListingAuthorization{authorization: authorization, institutions: deps.Store.Institution()},
	)
	bootstrap := newBootstrapService(
		deps.Store.Installation(),
		authentication.hasher,
		bootstrapRateLimit{
			cache:                 deps.Cache,
			window:                deps.LoginRateLimit.Window,
			maximumSourceAttempts: deps.LoginRateLimit.MaximumSourceAttempts,
		},
		deps.NodeID,
		time.Now,
	)
	return &App{
		store:                   deps.Store,
		authentication:          authentication,
		externalAuthentication:  externalAuthentication,
		mfa:                     mfa,
		authorization:           authorization,
		academicUnits:           academicUnits,
		academicUnitCommands:    academicUnitCommands,
		institutions:            institutions,
		programmes:              programmes,
		programmeLevels:         programmeLevels,
		academicPeriods:         academicPeriods,
		classes:                 classes,
		affiliations:            affiliations,
		academicUnitMembers:     academicUnitMembers,
		classMembers:            classMembers,
		userProfiles:            userProfiles,
		profilePictures:         profilePictures,
		accountStates:           accountStates,
		sessionAdministrations:  sessionAdministrations,
		roles:                   roles,
		roleBindings:            roleBindings,
		auditListings:           auditListings,
		bootstrap:               bootstrap,
		audit:                   audit,
		realtime:                realtime,
		jobs:                    jobs,
		mailer:                  deps.Mailer,
		cache:                   deps.Cache,
		nodeID:                  deps.NodeID,
		publicURL:               deps.PublicURL,
		accountRecovery:         deps.AccountRecovery,
		personalAccessTokens:    patPolicy,
		recentAuthenticationTTL: deps.RecentAuthenticationTTL,
		recoveryDiagnostics:     deps.RecoveryDiagnostics,
	}, nil
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

func (a *App) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, error) {
	return a.PrincipalHasPermissionTo(ctx, principal, action, resource)
}

// Store returns the root persistence contract. Focused services receive narrow
// store ports; App-method facades that still share this root should migrate
// onto explicit ports when their stable dependency seams are available.
func (a *App) Store() store.Store {
	return a.store
}

// errAuthenticationCacheMiss and errAuthenticationCacheNotStored are the
// transport-neutral sentinels authentication uses for disposable cache
// outcomes. Composition adapters translate infrastructure cache errors into
// these values so authentication never imports platform.
var (
	ErrAuthenticationCacheMiss      = errors.New("authentication cache: key not found")
	ErrAuthenticationCacheNotStored = errors.New("authentication cache: conditional write not applied")
)

// Deprecated aliases kept unexported for any same-package references during
// the transition; prefer the exported Err* names in new code.
var (
	errAuthenticationCacheMiss      = ErrAuthenticationCacheMiss
	errAuthenticationCacheNotStored = ErrAuthenticationCacheNotStored
)
