// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const systemAdministratorAuthenticationPathLock = "proctor:system-administrator-authentication-paths"

type systemAdministratorAuthenticationPathScope struct {
	OnlyUserID            string
	OnlyRoleBindingID     string
	ExcludedUserID        string
	ExcludedRoleBindingID string
}

func validAccessDeploymentCapabilities(capabilities store.AccessDeploymentCapabilities) bool {
	if len(capabilities.Providers) > model.AccessPolicyProviderMaxCount {
		return false
	}
	for providerID := range capabilities.Providers {
		if !model.IsValidIdentityProviderID(providerID) {
			return false
		}
	}
	return true
}

func lockSystemAdministratorAuthenticationPaths(ctx context.Context, tx *sqlxTxWrapper) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, systemAdministratorAuthenticationPathLock); err != nil {
		return fmt.Errorf("lock system administrator authentication paths: %w", err)
	}
	return nil
}

func hasUsableSystemAdministratorAuthenticationPath(ctx context.Context, executor sqlxExecutor,
	settings model.AccessPolicySettings, capabilities store.AccessDeploymentCapabilities, at time.Time,
	scope systemAdministratorAuthenticationPathScope,
) (bool, error) {
	providerIDs := make([]string, 0, len(settings.ProviderAdmissions))
	for providerID := range settings.ProviderAdmissions {
		if _, available := capabilities.Providers[providerID]; available {
			providerIDs = append(providerIDs, providerID)
		}
	}
	sort.Strings(providerIDs)
	var found bool
	if err := executor.Get(ctx, &found, `SELECT EXISTS (
		SELECT 1 FROM role_bindings rb
		JOIN roles r ON r.id=rb.role_id
		JOIN users u ON u.id=rb.user_id
		WHERE r.name=$1 AND r.built_in=TRUE AND r.archived_at IS NULL
		AND rb.scope_type='institution' AND rb.archived_at IS NULL
		AND rb.start_at<=$2 AND (rb.end_at IS NULL OR rb.end_at>$2)
		AND u.archived_at IS NULL AND u.disabled_at IS NULL
		AND ($3='' OR u.id=$3) AND ($4='' OR rb.id=$4)
		AND ($5='' OR u.id<>$5) AND ($6='' OR rb.id<>$6)
		AND (
			($7 AND EXISTS (
				SELECT 1 FROM password_credentials pc
				WHERE pc.user_id=u.id AND pc.archived_at IS NULL
			)) OR EXISTS (
				SELECT 1 FROM external_identities ei
				WHERE ei.user_id=u.id AND ei.archived_at IS NULL AND ei.provider=ANY($8)
			)
		)
	)`, model.SystemAdministratorRoleName, at, scope.OnlyUserID, scope.OnlyRoleBindingID,
		scope.ExcludedUserID, scope.ExcludedRoleBindingID, settings.LocalLoginEnabled, pq.Array(providerIDs)); err != nil {
		return false, fmt.Errorf("check system administrator authentication paths: %w", err)
	}
	return found, nil
}

func isActiveSystemAdministrator(ctx context.Context, executor sqlxExecutor, userID string, at time.Time) (bool, error) {
	var found bool
	if err := executor.Get(ctx, &found, `SELECT EXISTS (
		SELECT 1 FROM role_bindings rb
		JOIN roles r ON r.id=rb.role_id
		JOIN users u ON u.id=rb.user_id
		WHERE rb.user_id=$1 AND r.name=$2 AND r.built_in=TRUE AND r.archived_at IS NULL
		AND rb.scope_type='institution' AND rb.archived_at IS NULL
		AND rb.start_at<=$3 AND (rb.end_at IS NULL OR rb.end_at>$3)
		AND u.archived_at IS NULL AND u.disabled_at IS NULL
	)`, userID, model.SystemAdministratorRoleName, at); err != nil {
		return false, fmt.Errorf("check active system administrator: %w", err)
	}
	return found, nil
}
