// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	dpop, err := newDPoPSecurity(dpopSecurityDependencies{Cache: deps.Cache, Policy: dpopPolicy{Origin: deps.PublicURL,
		NonceLifetime: 5 * time.Minute, ProofLifetime: 5 * time.Minute, ClockSkew: time.Minute,
		NewNonce: model.NewCredentialToken, Now: time.Now}})
	if err != nil {
		return identityConstruction{}, err
	}
	authenticationAccess, err := newCurrentAuthenticationAccessPolicy(currentAuthenticationAccessPolicyDependencies{Policies: deps.Store.AccessPolicy()})
	if err != nil {
		return identityConstruction{}, err
	}
	accountMail := foundation.mail
	mfaApplication, err := newMFAApplicationService(mfaApplicationServiceDependencies{Users: deps.Store.User(), Credentials: deps.Store.MFA(), Sessions: deps.Store.Session(), Institutions: deps.Store.Institution(), Audit: mfaAuditAdapter{audit: foundation.audit}, Effects: foundation.realtime, Mail: accountMail, Mechanics: foundation.mfa, Security: mfaSecurityDependencies{authorization: authorization, capabilities: capabilities, attempts: foundation.attempts, rateLimit: deps.LoginRateLimit}, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	// Expand PAT policy used both at bearer resolution and administration.
	patPolicy := deps.PersonalAccessToken
	patResolver, err := newPersonalAccessTokenBearerResolver(personalAccessTokenBearerResolverDependencies{Tokens: deps.Store.PersonalAccessToken(), Policy: patPolicy, Diagnostics: deps.AuthenticationDiagnostics})
	if err != nil {
		return identityConstruction{}, err
	}
	authentication, err := newAuthenticationService(authenticationServiceDependencies{Users: deps.Store.User(), Passwords: deps.Store.PasswordCredential(), Sessions: deps.Store.Session(), SessionCredentials: deps.Store.SessionCredential(), AccessPolicy: authenticationAccess, Cache: deps.Cache, Attempts: foundation.attempts, SecurityEffects: foundation.realtime, Audit: mutationAuditAdapter{audit: foundation.audit}, LoginAudit: localLoginAuditAdapter{audit: foundation.audit}, Hasher: foundation.hasher, MFA: mfaApplication, PersonalTokens: patResolver, SessionPolicy: deps.Sessions, LoginRateLimit: deps.LoginRateLimit, Diagnostics: deps.AuthenticationDiagnostics, NewCredential: model.NewCredentialToken, Now: time.Now, Desktop: authenticationDesktopDependencies{registrations: deps.Store.DesktopRegistration(), dpop: dpop}})
	if err != nil {
		return identityConstruction{}, err
	}
	authentication.operational = deps.OperationalRecorder
	desktopAuthorization, err := newDesktopAuthorizationService(desktopAuthorizationServiceDependencies{Transactions: deps.Store.BrowserAuthentication(), Institutions: deps.Store.Institution(), AccessPolicy: authenticationAccess, Capabilities: capabilities, Audit: desktopAuthorizationAuditAdapter{audit: foundation.audit}, Attempts: desktopAuthorizationAttemptAccounting{attempts: foundation.attempts, policy: deps.LoginRateLimit}, SessionPolicy: deps.Sessions, Policy: DesktopAuthorizationPolicy{Issuer: deps.PublicURL, AllowLoopbackHTTPDevelopment: deps.LoopbackHTTPDevelopment}, NewCredential: model.NewCredentialToken, Now: time.Now, Identity: desktopAuthorizationIdentityDependencies{users: deps.Store.User(), authentication: authentication,
		compatibility: desktopCompatibility, dpop: dpop}})
	if err != nil {
		return identityConstruction{}, err
	}
	selfSessions, err := newSelfSessionService(selfSessionServiceDependencies{Sessions: deps.Store.Session(), Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: foundation.realtime, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	invitations, err := newInvitationService(invitationServiceDependencies{Store: deps.Store.Invitation(), Classes: deps.Store.Class(), Periods: deps.Store.AcademicPeriod(), AcademicUnits: deps.Store.AcademicUnit(), Roles: deps.Store.Role(), Authorization: invitationAuthorizationAdapter{authorization: authorization}, Mail: accountMail, Hasher: foundation.hasher, Audit: mutationAuditAdapter{audit: foundation.audit}, Attempts: invitationAttemptAccounting{attempts: foundation.attempts, policy: deps.AccountRecovery.RateLimit}, MembershipEffects: classMemberRealtimeEffects{sittings: deps.Store.ExamSitting(), realtime: foundation.realtime}, NodeID: deps.NodeID, PublicURL: deps.PublicURL, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, NewClaim: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	browserInvitations, err := newBrowserInvitationService(browserInvitationServiceDependencies{Transactions: deps.Store.BrowserAuthentication(), Institutions: deps.Store.Institution(), Invitations: invitations, Issuer: deps.PublicURL, NewProof: model.NewCredentialToken})
	if err != nil {
		return identityConstruction{}, err
	}
	accountTokens, err := newAccountTokenService(accountTokenServiceDependencies{Users: deps.Store.User(), Passwords: deps.Store.PasswordCredential(), Tokens: deps.Store.UserToken(), AccessPolicy: authenticationAccess, Institutions: deps.Store.Institution(), Mail: accountMail, Attempts: foundation.attempts, Hasher: foundation.hasher, Audit: accountTokenAuditRecorder{nodeID: deps.NodeID}, Effects: foundation.realtime, Diagnostics: deps.RecoveryDiagnostics, Policy: deps.AccountRecovery, PublicURL: deps.PublicURL, NewToken: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	accountTokens.administration = userEmailAdministration{
		authorization: authorization, audit: mutationAuditAdapter{audit: foundation.audit},
		recentAuthenticationTTL: deps.RecentAuthenticationTTL,
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
	personalAccessTokenAdministration, err := newPersonalAccessTokenAdministrationService(personalAccessTokenAdministrationServiceDependencies{Tokens: deps.Store.PersonalAccessToken(), Users: deps.Store.User(), AcademicUnits: deps.Store.AcademicUnit(), Institutions: deps.Store.Institution(), Audit: personalAccessTokenAuditAdapter{audit: foundation.audit}, Authorization: authorization, Mail: accountMail, Policy: patPolicy, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, NewCredential: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	desktopRegistrations, err := newDesktopRegistrationService(desktopRegistrationServiceDependencies{Registrations: deps.Store.DesktopRegistration(), Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: foundation.realtime, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Now: time.Now})
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
	externalAuthentication, err := newExternalAuthenticationService(externalAuthenticationServiceDependencies{Registry: deps.Registry, LoginStates: deps.Store.ExternalLoginState(), Institutions: deps.Store.Institution(), Identities: deps.Store.ExternalIdentity(), Sessions: deps.Store.Session(), AccessPolicy: authenticationAccess, Attempts: foundation.attempts, Authentication: authentication, Invalidator: foundation.invalidator, Audit: foundation.audit, MutationAudit: mutationAuditAdapter{audit: foundation.audit}, Capabilities: capabilities, InvitationAcceptor: invitations, DesktopAuthorization: desktopAuthorization, Policy: externalPolicy, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Diagnostics: deps.AuthenticationDiagnostics, NewCredential: model.NewCredentialToken, Now: time.Now})
	if err != nil {
		return identityConstruction{}, err
	}
	authenticationMethods, err := newAuthenticationMethodService(authenticationMethodServiceDependencies{Passwords: deps.Store.PasswordCredential(), Identities: deps.Store.ExternalIdentity(), Providers: deps.Registry, Capabilities: capabilities, Hasher: foundation.hasher, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: foundation.realtime, RecentTTL: deps.RecentAuthenticationTTL, Now: time.Now})
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
