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
				role, err := ss.Role().Save(ctx, &model.Role{
					Name: "context-" + model.NewId(), DisplayName: "Context role",
					Permissions: []string{string(test.action)},
				})
				requireNoError(t, err)
				_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
					UserID: user.ID, RoleID: role.ID, ScopeType: test.scopeType, ScopeID: test.scopeID,
					StartsAt: model.TimeUTC(time.Now().Add(-time.Minute)),
				})
				requireNoError(t, err)
			}
			got, err := ss.User().GetCurrentContext(ctx, user.ID, 2)
			requireNoError(t, err)
			wantAreas := []store.CurrentUserProductArea{store.CurrentUserProductAreaAccount}
			if test.administration {
				wantAreas = append(wantAreas, store.CurrentUserProductAreaAdministration)
			}
			wantAreas = append(wantAreas, store.CurrentUserProductAreaSettings)
			if got.UserID != user.ID || !slices.Equal(got.AvailableProductAreas, wantAreas) || got.NoAssignedAccess != (test.action == "") {
				t.Fatalf("current context = %#v, want areas %v", got, wantAreas)
			}
			if got.ManagementScopesHasMore || got.UnresolvedAttempt != nil {
				t.Fatalf("unexpected continuation or Attempt: %#v", got)
			}
			if test.administration {
				if len(got.ManagementScopes) != 1 || got.ManagementScopes[0].ScopeType != test.scopeType || got.ManagementScopes[0].ScopeID != test.scopeID {
					t.Fatalf("management scopes = %#v", got.ManagementScopes)
				}
			} else if len(got.ManagementScopes) != 0 {
				t.Fatalf("unexpected management scopes = %#v", got.ManagementScopes)
			}
		})
	}
}
