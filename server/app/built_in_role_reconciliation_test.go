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

type builtInRoleReconciliationStoreFake struct {
	store.InstallationStore
	input  *store.SystemAdministratorRoleReconciliation
	result *store.SystemAdministratorRoleReconciliationResult
	err    error
}

func (f *builtInRoleReconciliationStoreFake) ReconcileSystemAdministratorRole(
	_ context.Context,
	input *store.SystemAdministratorRoleReconciliation,
) (*store.SystemAdministratorRoleReconciliationResult, error) {
	f.input = input
	return f.result, f.err
}

func TestBootstrapServiceReconcilesSystemAdministratorRoleFromRegistry(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 16, 0, 0, 0, time.UTC)
	events := []string{}
	persistence := &builtInRoleReconciliationStoreFake{
		result: &store.SystemAdministratorRoleReconciliationResult{},
	}
	service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: &passwordHasherFake{events: &events}, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 3}, Protection: bootstrapProtection(), NodeID: "node-reconcile", Now: func() time.Time { return at }, RecoveryPolicy: administratorRecoveryPolicy{}})

	require.NoError(t, service.ReconcileSystemAdministratorRole(context.Background()))
	require.NotNil(t, persistence.input, "reconciliation input was not persisted")
	require.Equal(t, model.AllActions(), persistence.input.RequiredPermissions, "required permissions")
	require.Equal(t, at.UnixMilli(), persistence.input.ReconciledAt, "reconciled at")
	event := persistence.input.AuditEvent
	require.NotNil(t, event)
	require.Equal(t, "role.system_admin.reconcile", event.Action)
	require.Equal(t, "node-reconcile", event.NodeID)
	require.Equal(t, model.AuditStatusSuccess, event.Status)
	require.True(t, event.ActorID.IsZero())
	require.True(t, event.SessionID.IsZero())
}

func TestBootstrapServiceFailsStartupWhenSystemAdministratorRoleCannotReconcile(t *testing.T) {
	t.Parallel()
	persistenceErr := errors.New("database unavailable")
	events := []string{}
	persistence := &builtInRoleReconciliationStoreFake{err: persistenceErr}
	service := newBootstrapService(bootstrapServiceDependencies{Installations: persistence, Hasher: &passwordHasherFake{events: &events}, Attempts: bootstrapAttemptAccounting(t, &bootstrapAttemptCacheFake{}), RateLimit: LoginRateLimitPolicy{Window: time.Minute, MaximumSourceAttempts: 3}, Protection: bootstrapProtection(), NodeID: "node-reconcile", Now: model.NowUTC, RecoveryPolicy: administratorRecoveryPolicy{}})

	err := service.ReconcileSystemAdministratorRole(context.Background())
	require.ErrorIs(t, err, persistenceErr)
}
