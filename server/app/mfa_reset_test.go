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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type assistedResetStoreFake struct {
	*mfaApplicationStoreFake
	input *store.MFAAssistedReset
	err   error
}

func (s *assistedResetStoreFake) ResetWithAudit(_ context.Context, input *store.MFAAssistedReset) (*store.MFAResetResult, error) {
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
			command := ResetUserMFACommand{UserID: model.NewUserID().String(), IdentityVerified: true, Reason: "Authenticator lost", VerificationReference: "helpdesk-124"}
			switch name {
			case "self":
				command.UserID = principal.UserID.String()
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
				service.security.authorization = mfaResetAuthorizationFake{err: NewError("authorization.denied")}
			case "disabled":
				service.mechanics.settings.Enabled = false
			case "commit failure":
				persistence.err = errors.New("atomic reset failed")
			}
			err := service.ResetUser(context.Background(), NewInvocation(principal, model.RequestMetadata{}), command)
			if name != "success" {
				if err == nil || effects.userID != "" {
					t.Fatal("failed reset returned success or emitted post-commit effects")
				}
				if name != "commit failure" && persistence.input != nil {
					t.Fatal("invalid reset reached durable mutation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if persistence.input == nil || persistence.input.Principal.UserID != principal.UserID || persistence.input.UserID.String() != command.UserID || !persistence.input.IdentityVerified || persistence.input.Notice.Occurrence.TemplateKey != model.MailTemplateIdentityMFAReset || effects.userID != command.UserID {
				t.Fatal("assisted reset lost its exact actor, target, attestation, notice or post-commit effects")
			}
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
	status, err := service.GetStatus(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil {
		t.Fatal(err)
	}
	if status.ServiceEnabled || !status.MFARecoveryRequired || status.AuthenticationMethod != "password" {
		t.Fatal("disabled service concealed mandatory reenrollment")
	}
	if _, err := service.Setup(context.Background(), NewInvocation(principal, model.RequestMetadata{}), SetupMFACommand{}); !Is(err, "authentication.mfa.disabled") {
		t.Fatalf("disabled setup = %v", err)
	}
	if _, err := service.Challenge(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ChallengeMFACommand{Code: "123456"}); !Is(err, "authentication.session_required") {
		t.Fatalf("restricted challenge = %v", err)
	}
}

func TestMFAFactorAttemptsAreBoundedBeforeVerification(t *testing.T) {
	now := time.Now()
	principal := mfaTestPrincipal(now, model.AuthenticationSingleFactor)
	base := &mfaApplicationStoreFake{}
	service := newTestMFAApplicationService(t, base, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now)
	service.security.rateLimit.MaximumAttempts = 1
	invocation := NewInvocation(principal, model.RequestMetadata{IPAddress: "127.0.0.1"})
	_, _ = service.Challenge(context.Background(), invocation, ChallengeMFACommand{Code: "000000"})
	_, err := service.Challenge(context.Background(), invocation, ChallengeMFACommand{Code: "000001"})
	if !Is(err, "authentication.rate_limited") {
		t.Fatalf("unbounded factor challenge: %v", err)
	}
}
