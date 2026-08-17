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
) (identityConstruction, error) {
	authenticationAccess, err := newCurrentAuthenticationAccessPolicy(deps.Store.AccessPolicy())
	if err != nil {
		return identityConstruction{}, err
	}
	mfaApplication, err := newMFAApplicationService(
		deps.Store.User(), deps.Store.MFA(), deps.Store.Session(), deps.Store.Institution(),
		mfaAuditAdapter{audit: foundation.audit}, foundation.realtime, foundation.mfa,
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
	)
	if err != nil {
		return identityConstruction{}, err
	}
	selfSessions, err := newSelfSessionService(deps.Store.Session(), foundation.realtime, time.Now)
	if err != nil {
		return identityConstruction{}, err
	}
	accountTokens, err := newAccountTokenService(
		deps.Store.User(), deps.Store.PasswordCredential(), deps.Store.UserToken(), authenticationAccess,
		deps.Store.Institution(),
		deps.Mailer, foundation.attempts, foundation.hasher, accountTokenAuditRecorder{nodeID: deps.NodeID},
		foundation.realtime, deps.RecoveryDiagnostics, deps.AccountRecovery, deps.PublicURL,
		model.NewCredentialToken, time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	personalAccessTokenAdministration, err := newPersonalAccessTokenAdministrationService(
		deps.Store.PersonalAccessToken(), deps.Store.AcademicUnit(), deps.Store.Institution(),
		personalAccessTokenAuditAdapter{audit: foundation.audit}, authorization, patPolicy, deps.RecentAuthenticationTTL,
		model.NewCredentialToken, time.Now,
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
		externalPolicy,
		deps.AuthenticationDiagnostics,
		model.NewCredentialToken,
		time.Now,
	)
	if err != nil {
		return identityConstruction{}, err
	}
	return identityConstruction{
		authentication:                    authentication,
		selfSessions:                      selfSessions,
		externalAuthentication:            externalAuthentication,
		mfaApplication:                    mfaApplication,
		accountTokens:                     accountTokens,
		personalAccessTokenAdministration: personalAccessTokenAdministration,
	}, nil
}
