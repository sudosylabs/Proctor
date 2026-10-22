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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAdministratorMFAResetRequiresEnabledServiceAndExactTargetBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name              string
		enabled           bool
		institution, user string
		code              string
	}{
		{"disabled", false, model.NewInstitutionID().String(), model.NewUserID().String(), "authentication.mfa.disabled"},
		{"invalid institution", true, "wrong", model.NewUserID().String(), "administrator_recovery.invalid"},
		{"invalid user", true, model.NewInstitutionID().String(), "wrong", "administrator_recovery.invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			persistence := &installationStoreFake{events: &events}
			service := &bootstrapService{deps: bootstrapServiceDependencies{Installations: persistence, RecoveryPolicy: administratorRecoveryPolicy{
				capabilities: &accessPolicyCapabilitiesFake{}, mfaEnabled: test.enabled,
			}}}
			result, err := service.ResetAdministratorMFA(context.Background(), AdministratorMFAResetCommand{InstitutionID: test.institution, UserID: test.user})
			require.Nil(t, result, "result=%#v error=%v events", result, err)
			require.True(t, apperror.Is(err, test.code), "result=%#v error=%v events", result, err)
			require.Empty(t, events, "result=%#v error=%v events", result, err)
		})
	}
}

func TestAdministratorMFAResetPassesCurrentDeploymentAndRequiresDurableEvidence(t *testing.T) {
	t.Parallel()
	institutionID, userID := model.NewInstitutionID(), model.NewUserID()
	at := model.NowUTC()
	valid := &store.AdministratorMFAResetResult{RecordID: model.NewAdministratorRecoveryRecordID(), Recovery: &model.UserMFARecovery{
		UserID: userID, Generation: 2, ResetAt: model.OptionalTimeFrom(at), UpdatedAt: at, ReenrollmentRequired: true,
	}}
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{
		{Descriptor: model.ExternalAuthenticationProvider{ID: "existing-provider"}},
	}}}
	for _, test := range []struct {
		name   string
		result *store.AdministratorMFAResetResult
		err    error
		wantOK bool
	}{
		{"committed", valid, nil, true},
		{"missing evidence", nil, nil, false},
		{"missing record", &store.AdministratorMFAResetResult{Recovery: valid.Recovery}, nil, false},
		{"failed transaction", nil, errors.New("database unavailable"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			persistence := &installationStoreFake{events: &events, mfaResetResult: test.result, mfaResetErr: test.err}
			service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: &passwordHasherFake{events: &events}, Attempts: nil, RateLimit: LoginRateLimitPolicy{}, Protection: BootstrapProtectionPolicy{}, NodeID: "offline", Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{capabilities: capabilities, mfaEnabled: true}})
			result, err := service.ResetAdministratorMFA(context.Background(), AdministratorMFAResetCommand{InstitutionID: " " + institutionID.String() + " ", UserID: userID.String()})
			require.Equal(t, test.wantOK, err == nil, "result=%#v error=%v events", result, err)
			require.Equal(t, []string{"reset-administrator-mfa"}, events, "result=%#v error=%v events", result, err)
			input := persistence.mfaResetInput
			require.NotNil(t, input)
			require.Equal(t, institutionID, input.InstitutionID)
			require.Equal(t, userID, input.UserID)
			require.True(t, input.MFAEnabled)
			require.Len(t, input.Capabilities.Providers, 1)
			if test.wantOK {
				require.NotNil(t, result)
				require.True(t, result.ReenrollmentRequired)
				require.Equal(t, int64(2), result.RecoveryGeneration)
			}
		})
	}
}
