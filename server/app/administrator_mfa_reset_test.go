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
	"reflect"
	"testing"
	"time"

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
			service := &bootstrapService{installations: persistence, recoveryPolicy: administratorRecoveryPolicy{
				capabilities: &accessPolicyCapabilitiesFake{}, mfaEnabled: test.enabled,
			}}
			result, err := service.ResetAdministratorMFA(context.Background(), AdministratorMFAResetCommand{InstitutionID: test.institution, UserID: test.user})
			if result != nil || !Is(err, test.code) || len(events) != 0 {
				t.Fatalf("result=%#v error=%v events=%v", result, err, events)
			}
		})
	}
}

func TestAdministratorMFAResetPassesCurrentDeploymentAndRequiresDurableEvidence(t *testing.T) {
	t.Parallel()
	institutionID, userID := model.NewInstitutionID(), model.NewUserID()
	at := model.NowUTC()
	valid := &store.AdministratorMFAResetResult{RecordID: model.NewId(), Recovery: &model.UserMFARecovery{
		UserID: userID, Generation: 2, ResetAt: model.OptionalTimeFrom(at), UpdatedAt: at, ReenrollmentRequired: true,
	}}
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{
		{Descriptor: model.ExternalAuthenticationProvider{Id: "existing-provider"}},
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
			service := newBootstrapService(persistence, &passwordHasherFake{events: &events}, nil, LoginRateLimitPolicy{}, BootstrapProtectionPolicy{}, "offline", time.Now,
				administratorRecoveryPolicy{capabilities: capabilities, mfaEnabled: true})
			result, err := service.ResetAdministratorMFA(context.Background(), AdministratorMFAResetCommand{InstitutionID: " " + institutionID.String() + " ", UserID: userID.String()})
			if test.wantOK != (err == nil) || !reflect.DeepEqual(events, []string{"reset-administrator-mfa"}) {
				t.Fatalf("result=%#v error=%v events=%v", result, err, events)
			}
			input := persistence.mfaResetInput
			if input == nil || input.InstitutionID != institutionID || input.UserID != userID || !input.MFAEnabled || len(input.Capabilities.Providers) != 1 {
				t.Fatalf("input=%#v", input)
			}
			if test.wantOK && (result == nil || !result.ReenrollmentRequired || result.RecoveryGeneration != 2) {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}
