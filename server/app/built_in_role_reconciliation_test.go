// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

type builtInRoleReconciliationStoreFake struct {
	installationStore
	input  *store.SystemAdministratorRoleReconciliation
	result *store.SystemAdministratorRoleReconciliationResult
	err    error
}

func (fake *builtInRoleReconciliationStoreFake) ReconcileSystemAdministratorRole(
	_ context.Context,
	input *store.SystemAdministratorRoleReconciliation,
) (*store.SystemAdministratorRoleReconciliationResult, error) {
	fake.input = input
	return fake.result, fake.err
}

func TestBootstrapServiceReconcilesSystemAdministratorRoleFromRegistry(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 16, 0, 0, 0, time.UTC)
	events := []string{}
	persistence := &builtInRoleReconciliationStoreFake{
		result: &store.SystemAdministratorRoleReconciliationResult{},
	}
	service := newBootstrapService(
		persistence, &passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}),
		LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 3},
		bootstrapProtection(),
		"node-reconcile", func() time.Time { return at },
	)

	if err := service.ReconcileSystemAdministratorRole(context.Background()); err != nil {
		t.Fatal(err)
	}
	if persistence.input == nil {
		t.Fatal("reconciliation input was not persisted")
	}
	if !reflect.DeepEqual(persistence.input.RequiredPermissions, model.AllActions()) {
		t.Fatalf("required permissions = %#v, want %#v", persistence.input.RequiredPermissions, model.AllActions())
	}
	if persistence.input.ReconciledAt != at.UnixMilli() {
		t.Fatalf("reconciled at = %d, want %d", persistence.input.ReconciledAt, at.UnixMilli())
	}
	event := persistence.input.AuditEvent
	if event == nil || event.Action != "role.system_admin.reconcile" || event.NodeID != "node-reconcile" ||
		event.Status != model.AuditStatusSuccess || !event.ActorID.IsZero() || !event.SessionID.IsZero() {
		t.Fatalf("audit event = %#v", event)
	}
}

func TestBootstrapServiceFailsStartupWhenSystemAdministratorRoleCannotReconcile(t *testing.T) {
	t.Parallel()
	persistenceErr := errors.New("database unavailable")
	events := []string{}
	persistence := &builtInRoleReconciliationStoreFake{err: persistenceErr}
	service := newBootstrapService(
		persistence, &passwordHasherFake{events: &events},
		bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}),
		LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 3},
		bootstrapProtection(),
		"node-reconcile", model.NowUTC,
	)

	err := service.ReconcileSystemAdministratorRole(context.Background())
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("error = %v, want wrapped %v", err, persistenceErr)
	}
}
