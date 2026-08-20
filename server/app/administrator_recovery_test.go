// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAdministratorRecoveryHashesPrivatePasswordBeforeNamedAggregate(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &installationStoreFake{events: &events, recoveryResult: &store.AdministratorRecoveryResult{RecordID: model.NewId(), PasswordRotated: true}}
	hasher := &passwordHasherFake{events: &events, hash: "encoded-private-password"}
	service := newBootstrapService(persistence, hasher,
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), bootstrapRateLimitPolicy(10),
		bootstrapProtection(), "node-recovery", time.Now)
	institutionID, userID := model.NewInstitutionID(), model.NewUserID()

	result, err := service.RecoverAdministratorAccess(context.Background(), AdministratorRecoveryCommand{
		InstitutionID: institutionID.String(), UserID: userID.String(), Password: "private-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.PasswordRotated || persistence.recoveryInput == nil {
		t.Fatalf("result/input = %#v / %#v", result, persistence.recoveryInput)
	}
	if persistence.recoveryInput.InstitutionID != institutionID || persistence.recoveryInput.UserID != userID ||
		persistence.recoveryInput.RotatePasswordHash != "encoded-private-password" || persistence.recoveryInput.EnableLocalLogin {
		t.Fatalf("recovery input = %#v", persistence.recoveryInput)
	}
	if len(events) != 2 || events[0] != "hash-password" || events[1] != "recover-administrator" {
		t.Fatalf("events = %#v", events)
	}
}

func TestAdministratorRecoveryRejectsInvalidCommandBeforeHashing(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(&installationStoreFake{events: &events}, &passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), bootstrapRateLimitPolicy(10),
		bootstrapProtection(), "node-recovery", time.Now)

	for name, command := range map[string]AdministratorRecoveryCommand{
		"missing action":      {InstitutionID: model.NewInstitutionID().String(), UserID: model.NewUserID().String()},
		"invalid institution": {InstitutionID: "wrong", UserID: model.NewUserID().String(), EnableLocalLogin: true},
		"invalid user":        {InstitutionID: model.NewInstitutionID().String(), UserID: "wrong", EnableLocalLogin: true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.RecoverAdministratorAccess(context.Background(), command); err == nil {
				t.Fatal("RecoverAdministratorAccess() error = nil")
			}
		})
	}
	if len(events) != 0 {
		t.Fatalf("invalid commands reached dependencies: %#v", events)
	}
}

func TestAdministratorRecoveryStartupReconciliationFailsClosed(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistenceErr := errors.New("database unavailable")
	persistence := &installationStoreFake{events: &events, recoveryReconcileErr: persistenceErr}
	service := newBootstrapService(persistence, &passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), bootstrapRateLimitPolicy(10),
		bootstrapProtection(), "node-recovery", time.Now)

	err := service.ReconcileAdministratorRecovery(context.Background())
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("ReconcileAdministratorRecovery() = %v, want %v", err, persistenceErr)
	}
	if persistence.recoveryReconcileInput == nil || persistence.recoveryReconcileInput.NodeID != "node-recovery" {
		t.Fatalf("reconciliation input = %#v", persistence.recoveryReconcileInput)
	}
}
