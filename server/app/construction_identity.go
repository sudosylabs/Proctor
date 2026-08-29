// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func constructIdentity(
	deps Dependencies,
	foundation applicationFoundation,
	authorization *accessControlService,
	capabilities accessPolicyCapabilitySource,
	desktopCompatibility *desktopCompatibilityService,
) (identityConstruction, error) {
	dpop, err := newDPoPSecurity(deps.Cache, dpopPolicy{Origin: deps.PublicURL,
		NonceLifetime: 5 * time.Minute, ProofLifetime: 5 * time.Minute, ClockSkew: time.Minute,
		NewNonce: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	authenticationAccess, err := newCurrentAuthenticationAccessPolicy(deps.Store.AccessPolicy())
	if err != nil {
		return identityConstruction{}, err
	}
	accountMail := foundation.mail
	mfaApplication, err := newMFAApplicationService(
		deps.Store.User(), deps.Store.MFA(), deps.Store.Session(), deps.Store.Institution(),
		mfaAuditAdapter{audit: foundation.audit}, foundation.realtime, accountMail, foundation.mfa,
		deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	// Expand PAT policy used both at bearer resolution and administration.
	patPolicy := deps.PersonalAccessToken
	patResolver, err := newPersonalAccessTokenBearerResolver(
		deps.Store.PersonalAccessToken(), patPolicy, deps.AuthenticationDiagnostics,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	authentication, err := newAuthenticationService(
		deps.Store.User(),
		deps.Store.PasswordCredential(),
		deps.Store.Session(),
		deps.Store.SessionCredential(),
		authenticationAccess,
		deps.Cache,
		foundation.attempts,
		foundation.realtime,
		foundation.hasher,
		mfaApplication,
		patResolver,
		deps.Sessions,
		deps.LoginRateLimit,
		deps.AuthenticationDiagnostics,
		model.NewCredentialToken,
		time.Now,
		authenticationDesktopDependencies{registrations: deps.Store.DesktopRegistration(), dpop: dpop},
	)
	if err != nil {
		return identityConstruction{}, err
	}
	desktopAuthorization, err := newDesktopAuthorizationService(
		deps.Store.BrowserAuthentication(), deps.Store.Institution(), authenticationAccess,
		capabilities, desktopAuthorizationAuditAdapter{audit: foundation.audit},
		desktopAuthorizationAttemptAccounting{attempts: foundation.attempts, policy: deps.LoginRateLimit}, deps.Sessions,
		DesktopAuthorizationPolicy{Issuer: deps.PublicURL, AllowLoopbackHTTPDevelopment: deps.LoopbackHTTPDevelopment}, model.NewCredentialToken, time.Now,
		desktopAuthorizationIdentityDependencies{users: deps.Store.User(), authentication: authentication,
			compatibility: desktopCompatibility, dpop: dpop},
	)
	if err != nil {
		return identityConstruction{}, err
	}
	selfSessions, err := newSelfSessionService(deps.Store.Session(), foundation.realtime, time.Now)
	if err != nil {
		return identityConstruction{}, err
	}
	invitations, err := newInvitationService(
		deps.Store.Invitation(), deps.Store.Class(), deps.Store.AcademicPeriod(),
		deps.Store.AcademicUnit(), deps.Store.Role(),
		invitationAuthorizationAdapter{authorization: authorization},
		accountMail, foundation.hasher, invitationAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		invitationAttemptAccounting{attempts: foundation.attempts, policy: deps.AccountRecovery.RateLimit},
		classMemberRealtimeEffects{sittings: deps.Store.ExamSitting(), realtime: foundation.realtime}, deps.NodeID, deps.PublicURL,
		deps.RecentAuthenticationTTL, model.NewCredentialToken, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	browserInvitations, err := newBrowserInvitationService(
		deps.Store.BrowserAuthentication(), deps.Store.Institution(), invitations,
		deps.PublicURL, model.NewCredentialToken,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	accountTokens, err := newAccountTokenService(
		deps.Store.User(), deps.Store.PasswordCredential(), deps.Store.UserToken(), authenticationAccess,
		deps.Store.Institution(),
		accountMail, foundation.attempts, foundation.hasher, accountTokenAuditRecorder{nodeID: deps.NodeID},
		foundation.realtime, deps.RecoveryDiagnostics, deps.AccountRecovery, deps.PublicURL,
		model.NewCredentialToken, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	publicRegistration, err := newPublicRegistrationService(publicRegistrationDependencies{
		registrations: deps.Store.User(),
		policies:      currentPublicRegistrationPolicy{policies: deps.Store.AccessPolicy()},
		institutions:  publicRegistrationInstitutionAdapter{institutions: deps.Store.Institution()},
		mail:          accountMail,
		attempts:      foundation.attempts,
		hasher:        foundation.hasher,
		rateLimit:     deps.AccountRecovery.RateLimit,
		tokenTTL:      deps.AccountRecovery.EmailVerificationTTL,
		publicURL:     deps.PublicURL,
		nodeID:        deps.NodeID,
		newToken:      model.NewCredentialToken,
		now:           time.Now,
	})
	if err != nil {
		return identityConstruction{}, err
	}
	personalAccessTokenAdministration, err := newPersonalAccessTokenAdministrationService(
		deps.Store.PersonalAccessToken(), deps.Store.User(), deps.Store.AcademicUnit(), deps.Store.Institution(),
		personalAccessTokenAuditAdapter{audit: foundation.audit}, authorization, accountMail, patPolicy, deps.RecentAuthenticationTTL,
		model.NewCredentialToken, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	desktopRegistrations, err := newDesktopRegistrationService(
		deps.Store.DesktopRegistration(), mutationAuditAdapter{audit: foundation.audit}, foundation.realtime,
		deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
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
	externalAuthentication, err := newExternalAuthenticationService(
		deps.Registry,
		deps.Store.ExternalLoginState(),
		deps.Store.Institution(),
		deps.Store.ExternalIdentity(),
		deps.Store.Session(),
		authenticationAccess,
		foundation.attempts,
		authentication,
		foundation.invalidator,
		foundation.audit,
		mutationAuditAdapter{audit: foundation.audit},
		capabilities,
		invitations,
		externalPolicy,
		deps.RecentAuthenticationTTL,
		deps.AuthenticationDiagnostics,
		model.NewCredentialToken,
		time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	externalAuthentication.desktopAuthorization = desktopAuthorization
	authenticationMethods, err := newAuthenticationMethodService(
		deps.Store.PasswordCredential(), deps.Store.ExternalIdentity(), deps.Registry,
		capabilities, foundation.hasher, mutationAuditAdapter{audit: foundation.audit}, foundation.realtime,
		deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	return identityConstruction{
		mail:                              accountMail,
		authentication:                    authentication,
		desktopAuthorization:              desktopAuthorization,
		desktopRegistrations:              desktopRegistrations,
		browserInvitations:                browserInvitations,
		selfSessions:                      selfSessions,
		externalAuthentication:            externalAuthentication,
		authenticationMethods:             authenticationMethods,
		mfaApplication:                    mfaApplication,
		accountTokens:                     accountTokens,
		publicRegistration:                publicRegistration,
		invitations:                       invitations,
		personalAccessTokenAdministration: personalAccessTokenAdministration,
	}, nil
}
