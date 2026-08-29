// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// AuthenticateDPoP resolves a Desktop access credential and establishes the
// request-scoped registered_desktop_key assurance only after proof, nonce,
// replay, Session, User, and Registration validation.
func (a *App) AuthenticateDPoP(
	ctx context.Context,
	rawToken string,
	proof string,
	method string,
	path string,
) (*model.Principal, error) {
	principal, err := a.authentication.authenticateDPoP(ctx, rawToken, proof, method, path)
	a.recordOperational("authentication", "dpop", err)
	return principal, err
}

// DPoPChallengeNonce exposes only the standard replacement nonce carried by a
// nonce challenge. Other application errors do not reveal internal state.
func DPoPChallengeNonce(err error) (string, bool) {
	var challenge *dpopChallengeError
	if !errors.As(err, &challenge) {
		return "", false
	}
	return challenge.Nonce(), challenge.Nonce() != ""
}

func (s *authenticationService) authenticateDPoP(
	ctx context.Context,
	rawToken string,
	proof string,
	method string,
	path string,
) (*model.Principal, error) {
	resolved, err := s.resolveDesktopCredential(ctx, rawToken, model.SessionCredentialAccess)
	if err != nil {
		return nil, err
	}
	if _, err = s.verifyDesktopProof(ctx, resolved, rawToken, proof, method, path, true); err != nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	if err = s.updateActivity(ctx, resolved, now); err != nil {
		return nil, err
	}
	s.cacheAuthentication(ctx, model.HashToken(rawToken), resolved, now)
	principal := principalFromDesktopAuthentication(resolved)
	if principal.Validate() != nil {
		return nil, authenticationUnavailable(errors.New("resolved DPoP principal is invalid"))
	}
	return principal, nil
}

func (s *authenticationService) validateDPoPRefresh(
	ctx context.Context,
	rawToken string,
	proof string,
	method string,
	path string,
) error {
	resolved, err := s.resolveDesktopCredential(ctx, rawToken, model.SessionCredentialRefresh)
	if err != nil {
		return err
	}
	_, err = s.verifyDesktopProof(ctx, resolved, "", proof, method, path, false)
	return err
}

func (s *authenticationService) resolveDesktopCredential(
	ctx context.Context,
	rawToken string,
	kind model.SessionCredentialKind,
) (*cachedAuthentication, error) {
	if s.registrations == nil || s.dpop == nil || !validRawCredential(rawToken) {
		return nil, invalidTokenAppError()
	}
	credential, session, err := s.sessionCredentials.GetSessionByTokenHash(
		ctx, model.HashToken(rawToken), kind,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	now := s.now().UTC()
	if session.ClientType != model.SessionClientDesktop || credential.IsExpiredAt(now) ||
		!session.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(session.DPoPKeyThumbprint) {
		return nil, invalidTokenAppError()
	}
	session, err = s.enforceSessionExpiry(ctx, session, now)
	if err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, session.UserID.String())
	if err != nil || !user.IsActive() {
		if store.IsNotFound(err) || err == nil {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	return &cachedAuthentication{Credential: credential, Session: session, User: user}, nil
}

func (s *authenticationService) verifyDesktopProof(
	ctx context.Context,
	resolved *cachedAuthentication,
	accessToken string,
	proof string,
	method string,
	path string,
	access bool,
) (*model.DesktopRegistration, error) {
	if resolved == nil || resolved.Session == nil || s.registrations == nil || s.dpop == nil {
		return nil, invalidTokenAppError()
	}
	registration, err := s.registrations.Get(ctx, resolved.Session.DesktopRegistrationID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	if !registration.IsActive() || registration.UserID != resolved.Session.UserID ||
		registration.KeyThumbprint != resolved.Session.DPoPKeyThumbprint {
		return nil, invalidTokenAppError()
	}
	target, err := s.dpopTarget(path)
	if err != nil {
		return nil, NewError("authentication.dpop.invalid")
	}
	if !access {
		accessToken = ""
	}
	_, err = s.dpop.Verify(ctx, proof, method, target, accessToken, &registration.PublicJWK, dpopBinding{
		Kind: dpopBindingSession, SessionID: resolved.Session.ID,
		DesktopRegistrationID: registration.ID, KeyThumbprint: registration.KeyThumbprint,
		Origin: s.dpop.policy.Origin,
	})
	return registration, err
}

func (s *authenticationService) dpopTarget(path string) (string, error) {
	if s.dpop == nil || path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "?#") {
		return "", errors.New("invalid DPoP request path")
	}
	return canonicalDPoPTarget(s.dpop.policy.Origin + path)
}

func principalFromDesktopAuthentication(resolved *cachedAuthentication) *model.Principal {
	return &model.Principal{
		UserID: resolved.User.ID, SessionID: resolved.Session.ID,
		CredentialID:             model.PrincipalCredentialID(resolved.Credential.ID),
		CredentialType:           model.CredentialSessionAccess,
		AuthenticationMethod:     resolved.Session.AuthenticationMethod,
		AuthenticationProviderID: resolved.Session.AuthenticationProviderID,
		ExternalIdentityID:       resolved.Session.ExternalIdentityID,
		AuthenticationStrength:   resolved.Session.AuthenticationStrength,
		ClientType:               resolved.Session.ClientType,
		DesktopRegistrationID:    resolved.Session.DesktopRegistrationID,
		DPoPKeyThumbprint:        resolved.Session.DPoPKeyThumbprint,
		RegisteredDesktopKey:     true,
		DesktopRelease:           resolved.Session.DesktopRelease,
		DesktopBuildID:           resolved.Session.DesktopBuildID,
		DesktopPlatform:          resolved.Session.DesktopPlatform,
		DesktopArchitecture:      resolved.Session.DesktopArchitecture,
		DesktopRealtimeProtocol:  resolved.Session.DesktopRealtimeProtocol,
		AuthenticatedAt:          resolved.Session.AuthenticatedAt,
		MFACompletedAt:           resolved.Session.MFACompletedAt,
	}
}
