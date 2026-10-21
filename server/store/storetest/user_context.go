// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testUserStoreCurrentContext(t *testing.T, ss store.Store) {
	ctx := t.Context()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "context-unit")
	for _, test := range []struct {
		name           string
		action         model.Action
		scopeType      model.RoleScopeType
		scopeID        string
		administration bool
	}{
		{name: "unassigned"},
		{name: "institution", action: model.ActionInstitutionManage, scopeType: model.RoleScopeInstitution, scopeID: institution.ID.String(), administration: true},
		{name: "academic-unit", action: model.ActionAcademicProgressionManage, scopeType: model.RoleScopeAcademicUnit, scopeID: unit.ID.String(), administration: true},
		{name: "exam-permission-without-managed-exam", action: model.ActionExamView, scopeType: model.RoleScopeAcademicUnit, scopeID: unit.ID.String()},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := saveUser(t, ctx, ss)
			if test.action != "" {
				role, err := SaveRole(ctx, ss, &model.Role{
					Name: "context-" + model.NewID(), DisplayName: "Context role",
					Permissions: []string{string(test.action)},
				})
				require.NoError(t, err)
				_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
					UserID: user.ID, RoleID: role.ID, ScopeType: test.scopeType, ScopeID: test.scopeID,
					StartsAt: model.TimeUTC(time.Now().Add(-time.Minute)),
				})
				require.NoError(t, err)
			}
			got, err := ss.User().GetCurrentContext(ctx, user.ID, 2)
			require.NoError(t, err)
			wantAreas := []store.CurrentUserProductArea{store.CurrentUserProductAreaAccount}
			if test.administration {
				wantAreas = append(wantAreas, store.CurrentUserProductAreaAdministration)
			}
			wantAreas = append(wantAreas, store.CurrentUserProductAreaSettings)
			require.Equal(t, user.ID, got.UserID, "current context = %#v, want areas %v", got, wantAreas)
			require.True(t, slices.Equal(got.AvailableProductAreas, wantAreas), "current context = %#v, want areas %v", got, wantAreas)
			require.Equal(t, test.action == "", got.NoAssignedAccess, "current context = %#v, want areas %v", got, wantAreas)
			require.False(t, got.ManagementScopesHasMore)
			require.Nil(t, got.UnresolvedAttempt)
			if test.administration {
				require.Len(t, got.ManagementScopes, 1)
				require.Equal(t, test.scopeType, got.ManagementScopes[0].ScopeType)
				require.Equal(t, test.scopeID, got.ManagementScopes[0].ScopeID)
			} else {
				require.Empty(t, got.ManagementScopes, "unexpected management scopes")
			}
		})
	}
}
