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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type assistedResetStoreFake struct {
	*mfaApplicationStoreFake
	input *store.MFAAssistedReset
	err   error
}

func (s *assistedResetStoreFake) Reset(_ context.Context, input *store.MFAAssistedReset) (*store.MFAResetResult, error) {
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	return &store.MFAResetResult{Recovery: &model.UserMFARecovery{UserID: input.UserID, Generation: 1, ReenrollmentRequired: true}, Sessions: []*model.Session{{ID: model.NewSessionID(), UserID: input.UserID}}, AccessTokenHashes: []string{"retired-hash"}}, nil
}

func TestMFAAssistedResetRequiresIndependentVerifiedAdministrator(t *testing.T) {
	for _, name := range []string{"self", "unverified", "empty reason", "empty reference", "weak", "stale", "restricted", "denied", "disabled", "commit failure", "success"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			principal := mfaTestPrincipal(now, model.AuthenticationMultiFactor)
			base := &mfaApplicationStoreFake{}
			persistence := &assistedResetStoreFake{mfaApplicationStoreFake: base}
			effects := &mfaApplicationEffectsFake{}
			service := newTestMFAApplicationService(t, base, &mfaApplicationAuditFake{}, effects, now)
			service.credentials = persistence
			command := ResetUserMFACommand{UserID: model.NewUserID(), IdentityVerified: true, Reason: "Authenticator lost", VerificationReference: "helpdesk-124"}
			switch name {
			case "self":
				command.UserID = principal.UserID
			case "unverified":
				command.IdentityVerified = false
			case "empty reason":
				command.Reason = " "
			case "empty reference":
				command.VerificationReference = " "
			case "weak":
				principal.AuthenticationStrength = model.AuthenticationSingleFactor
				principal.MFACompletedAt = model.OptionalTime{}
			case "stale":
				principal.AuthenticatedAt = now.Add(-time.Hour)
				principal.MFACompletedAt = model.OptionalTimeFrom(principal.AuthenticatedAt)
			case "restricted":
				principal.AuthenticationGeneration = 1
				principal.MFARecoveryRequired = true
			case "denied":
				service.security.authorization = mfaResetAuthorizationFake{err: apperror.New("authorization.denied")}
			case "disabled":
				service.mechanics.settings.Enabled = false
			case "commit failure":
				persistence.err = errors.New("atomic reset failed")
			}
			err := service.ResetUserMFA(context.Background(), invocation.New(principal, model.RequestMetadata{}), command)
			if name != "success" {
				require.Error(t, err, "failed reset returned success or emitted post-commit effects")
				require.Empty(t, effects.userID, "failed reset returned success or emitted post-commit effects")
				if name != "commit failure" {
					require.Nil(t, persistence.input, "invalid reset reached durable mutation")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, persistence.input, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			require.Equal(t, principal.UserID, persistence.input.Principal.UserID, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			require.Equal(t, command.UserID, persistence.input.UserID, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			require.True(t, persistence.input.IdentityVerified, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			require.Equal(t, model.MailTemplateIdentityMFAReset, persistence.input.Notice.Occurrence.TemplateKey, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			require.Equal(t, command.UserID.String(), effects.userID, "assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
		})
	}
}

func TestMFARestrictedContextSurvivesDisabledService(t *testing.T) {
	now := time.Now()
	principal := mfaTestPrincipal(now, model.AuthenticationSingleFactor)
	principal.AuthenticationGeneration = 2
	principal.MFARecoveryRequired = true
	base := &mfaApplicationStoreFake{}
	service := newTestMFAApplicationService(t, base, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now)
	service.mechanics.settings.Enabled = false
	status, err := service.GetMFAStatus(context.Background(), invocation.New(principal, model.RequestMetadata{}), GetMFAStatusQuery{})
	require.NoError(t, err)
	require.False(t, status.ServiceEnabled, "disabled service concealed mandatory reenrollment")
	require.True(t, status.MFARecoveryRequired, "disabled service concealed mandatory reenrollment")
	require.Equal(t, "password", status.AuthenticationMethod, "disabled service concealed mandatory reenrollment")
	_, err = service.SetupMFA(context.Background(), invocation.New(principal, model.RequestMetadata{}), SetupMFACommand{})
	require.True(t, apperror.Is(err, "authentication.mfa.disabled"), "disabled setup = %v", err)
	_, err = service.ChallengeMFA(context.Background(), invocation.New(principal, model.RequestMetadata{}), ChallengeMFACommand{Code: "123456"})
	require.True(t, apperror.Is(err, "authentication.session_required"), "restricted challenge = %v", err)
}

func TestMFAFactorAttemptsAreBoundedBeforeVerification(t *testing.T) {
	now := time.Now()
	principal := mfaTestPrincipal(now, model.AuthenticationSingleFactor)
	base := &mfaApplicationStoreFake{}
	service := newTestMFAApplicationService(t, base, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now)
	service.security.rateLimit.MaximumAttempts = 1
	invocation := invocation.New(principal, model.RequestMetadata{IPAddress: "127.0.0.1"})
	_, _ = service.ChallengeMFA(context.Background(), invocation, ChallengeMFACommand{Code: "000000"})
	_, err := service.ChallengeMFA(context.Background(), invocation, ChallengeMFACommand{Code: "000001"})
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "unbounded factor challenge: %v", err)
}
