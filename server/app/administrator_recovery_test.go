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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAdministratorRecoveryHashesPrivatePasswordBeforeNamedAggregate(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &installationStoreFake{events: &events, recoveryResult: &store.AdministratorRecoveryResult{RecordID: model.NewAdministratorRecoveryRecordID(), PasswordRotated: true}}
	hasher := &passwordHasherFake{events: &events, hash: "encoded-private-password"}
	service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: hasher, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: bootstrapRateLimitPolicy(10), Protection: bootstrapProtection(), NodeID: "node-recovery", Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{}})
	institutionID, userID := model.NewInstitutionID(), model.NewUserID()

	result, err := service.RecoverAdministratorAccess(context.Background(), AdministratorRecoveryCommand{
		InstitutionID: institutionID.String(), UserID: userID.String(), Password: "private-password",
	})
	require.NoError(t, err)
	require.NotNil(t, result, "result/input")
	require.True(t, result.PasswordRotated, "result/input")
	require.NotNil(t, persistence.recoveryInput, "result/input")
	require.Equal(t, institutionID, persistence.recoveryInput.InstitutionID)
	require.Equal(t, userID, persistence.recoveryInput.UserID)
	require.Equal(t, "encoded-private-password", persistence.recoveryInput.RotatePasswordHash)
	require.False(t, persistence.recoveryInput.EnableLocalLogin)
	require.Len(t, events, 2)
	require.Equal(t, "hash-password", events[0])
	require.Equal(t, "recover-administrator", events[1])
}

func TestAdministratorRecoveryPasswordWorkFailuresStopBeforeAggregate(t *testing.T) {
	testPasswordHashFailures(t, "administrator_recovery.unavailable", func(t *testing.T, ctx context.Context, hasher passwordHash) error {
		events := []string{}
		persistence := &installationStoreFake{events: &events}
		service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: hasher, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: bootstrapRateLimitPolicy(10), Protection: bootstrapProtection(), NodeID: "node-recovery", Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{}})
		result, err := service.RecoverAdministratorAccess(ctx, AdministratorRecoveryCommand{
			InstitutionID: model.NewInstitutionID().String(), UserID: model.NewUserID().String(),
			Password: "correct horse battery staple",
		})
		require.Nil(t, result, "failed password work reached offline administrator recovery")
		require.Nil(t, persistence.recoveryInput, "failed password work reached offline administrator recovery")
		require.Empty(t, events, "failed password work reached offline administrator recovery")
		return err
	})
}

func TestAdministratorRecoveryRejectsInvalidCommandBeforeHashing(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(bootstrapServiceDependencies{Installations: &installationStoreFake{events: &events}, Hasher: &passwordHasherFake{events: &events}, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: bootstrapRateLimitPolicy(10), Protection: bootstrapProtection(), NodeID: "node-recovery", Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{}})

	for name, command := range map[string]AdministratorRecoveryCommand{
		"missing action":      {InstitutionID: model.NewInstitutionID().String(), UserID: model.NewUserID().String()},
		"invalid institution": {InstitutionID: "wrong", UserID: model.NewUserID().String(), EnableLocalLogin: true},
		"invalid user":        {InstitutionID: model.NewInstitutionID().String(), UserID: "wrong", EnableLocalLogin: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.RecoverAdministratorAccess(context.Background(), command)
			require.Error(t, err, "RecoverAdministratorAccess() error = nil")
		})
	}
	require.Empty(t, events)
}

func TestAdministratorRecoveryStartupReconciliationFailsClosed(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistenceErr := errors.New("database unavailable")
	persistence := &installationStoreFake{events: &events, recoveryReconcileErr: persistenceErr}
	service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: &passwordHasherFake{events: &events}, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: bootstrapRateLimitPolicy(10), Protection: bootstrapProtection(), NodeID: "node-recovery", Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{}})

	err := service.ReconcileAdministratorRecovery(context.Background())
	require.ErrorIs(t, err, persistenceErr, "ReconcileAdministratorRecovery()")
	require.NotNil(t, persistence.recoveryReconcileInput)
	require.Equal(t, "node-recovery", persistence.recoveryReconcileInput.NodeID)
}
