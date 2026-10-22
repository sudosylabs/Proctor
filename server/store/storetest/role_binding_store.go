// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// This conformance structure is adapted from Mattermost's per-model store
// tests. See server/NOTICE for exact provenance.

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRoleBindingStore(t *testing.T, ss store.Store) {
	t.Run("ActiveIntervalPrecision", func(t *testing.T) { testRoleBindingActiveIntervalPrecision(t, ss) })
	t.Run("AuditedMutations", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		user := saveUser(t, ctx, ss)
		role, err := SaveRole(ctx, ss, &model.Role{Name: "audited-binding-role", DisplayName: "Audited", Permissions: []string{string(model.ActionClassView)}})
		require.NoError(t, err)
		at := model.NowUTC()
		_, err = ss.RoleBinding().Save(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(at),
			},
			AuditEventID: model.NewAuditEventID(), AuditAt: at,
		})
		require.Error(t, err, "Save succeeded without audit attempt")
		attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
			Action:    string(model.ActionRoleManage),
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		require.NoError(t, err)
		staleUpdatedAt := role.UpdatedAt
		stalePermissions := append([]string(nil), role.Permissions...)
		role.DisplayName = "Audited Updated"
		role, err = UpdateRole(ctx, ss, role)
		require.NoError(t, err)
		_, err = ss.RoleBinding().Save(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(at),
			},
			ExpectedRoleUpdatedAt: staleUpdatedAt, ExpectedRolePermissions: stalePermissions,
			AuditEventID: attempt.ID, AuditAt: at,
		})
		require.True(t, store.IsConflict(err), "Save(stale role snapshot) error")
		attempt, err = ss.Audit().Save(ctx, &model.AuditEvent{
			Action: string(model.ActionRoleManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		require.NoError(t, err)
		createNotice := classMemberPreparedMail(t, &model.ClassMember{UserID: user.ID},
			model.MailTemplateAuthorizationInstitutionRoleAssigned, model.TimeUTC(at))
		saved, err := ss.RoleBinding().Save(ctx, &store.RoleBindingCreation{
			Binding: &model.RoleBinding{
				UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(at),
			},
			ExpectedRoleUpdatedAt: role.UpdatedAt, ExpectedRolePermissions: append([]string(nil), role.Permissions...),
			ExpectedRecipientRevision: user.Revision, Notice: createNotice,
			AuditEventID: attempt.ID, AuditAt: at,
		})
		require.NoError(t, err)
		require.NoError(t, requireClassMemberMail(t, ctx, ss, createNotice, model.MailTemplateAuthorizationInstitutionRoleAssigned))
		endAttempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
			Action:    string(model.ActionRoleManage),
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node",
		})
		require.NoError(t, err)
		endNotice := classMemberPreparedMail(t, &model.ClassMember{UserID: user.ID},
			model.MailTemplateAuthorizationInstitutionRoleEnded, model.TimeUTC(at.Add(1*time.Millisecond)))
		ended, err := ss.RoleBinding().End(ctx, &store.RoleBindingEnd{
			ID: saved.ID, EndAt: at.Add(1 * time.Millisecond), ExpectedRecipientRevision: user.Revision, Notice: endNotice,
			AuditEventID: endAttempt.ID, AuditAt: at.Add(1 * time.Millisecond),
		})
		require.NoError(t, err)
		require.NoError(t, requireClassMemberMail(t, ctx, ss, endNotice, model.MailTemplateAuthorizationInstitutionRoleEnded))
		require.True(t, at.Add(1*time.Millisecond).Equal(ended.EndsAt.Time))
	})
	t.Run("ScopedUserProjection", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "binding-root")
		child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "binding-child")
		sibling := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "binding-sibling")
		programme := saveProgramme(t, ctx, ss, child.ID.String(), "binding-programme")
		level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "binding-level")
		period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "binding-period", 1_800_000_000_000)
		class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "binding-class")
		user := saveUser(t, ctx, ss)
		role, err := SaveRole(ctx, ss, &model.Role{Name: "binding-scope-reader", DisplayName: "Reader", Permissions: []string{string(model.ActionClassView)}})
		require.NoError(t, err)
		start := model.GetMillis()
		bindings := make([]*model.RoleBinding, 0, 4)
		for _, scope := range []struct {
			type_ model.RoleScopeType
			id    string
		}{
			{model.RoleScopeInstitution, institution.ID.String()},
			{model.RoleScopeAcademicUnit, child.ID.String()},
			{model.RoleScopeAcademicUnit, sibling.ID.String()},
			{model.RoleScopeClass, class.ID.String()},
		} {
			saved, saveErr := SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: user.ID, RoleID: role.ID, ScopeType: scope.type_, ScopeID: scope.id, StartsAt: model.TimeFromMillis(start)})
			require.NoError(t, saveErr)
			bindings = append(bindings, saved)
		}
		visible, err := ss.RoleBinding().ListVisibleByUser(ctx, user.ID, store.UserVisibilityScope{AcademicUnitRootIDs: []model.AcademicUnitID{root.ID}})
		require.NoError(t, err)
		seen := map[model.RoleBindingID]bool{}
		for _, binding := range visible {
			seen[binding.ID] = true
		}
		require.Len(t, visible, 2)
		require.True(t, seen[bindings[1].ID])
		require.True(t, seen[bindings[3].ID])
		require.False(t, seen[bindings[0].ID])
		require.False(t, seen[bindings[2].ID])
	})
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUserWithPassword(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name: "class-reader", DisplayName: "Class Reader",
		Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	start := model.NowUTC()
	binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(start),
	})
	require.NoError(t, err)
	require.True(t, binding.ID.IsValid())
	got, err := ss.RoleBinding().Get(ctx, binding.ID)
	require.NoError(t, err)
	byUser, err := ss.RoleBinding().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	byScope, err := ss.RoleBinding().ListByScope(
		ctx, model.RoleScopeInstitution, institution.ID.String(),
	)
	require.NoError(t, err)
	active, err := ss.RoleBinding().ListActiveByUser(ctx, user.ID, model.TimeUTC(start.Add(1*time.Millisecond)))
	require.NoError(t, err)
	require.Equal(t, binding.ID, got.ID, "binding queries = %#v/%d/%d", got, len(byUser), len(byScope))
	require.Len(t, byUser, 1, "binding queries = %#v/%d/%d", got, len(byUser), len(byScope))
	require.Len(t, byScope, 1, "binding queries = %#v/%d/%d", got, len(byUser), len(byScope))
	require.Len(t, active, 1, "binding queries = %#v/%d/%d", got, len(byUser), len(byScope))
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(start.Add(1 * time.Millisecond)),
	})
	require.True(t, store.IsConflict(err), "overlapping Save() error")
	ended, err := EndRoleBinding(ctx, ss, binding.ID, start.Add(2*time.Millisecond))
	require.NoError(t, err)
	require.True(t, start.Add(2*time.Millisecond).Equal(ended.EndsAt.Time))
	active, err = ss.RoleBinding().ListActiveByUser(ctx, user.ID, model.TimeUTC(start.Add(3*time.Millisecond)))
	require.NoError(t, err)
	require.Empty(t, active)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeClass,
		ScopeID: model.NewID(), StartsAt: model.TimeUTC(start),
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference)
	require.Equal(t, "role_bindings_class_scope_fkey", reference.Constraint)

	adminRole, err := SaveRole(ctx, ss, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Permissions: model.AllActions(), BuiltIn: true,
	})
	require.NoError(t, err)
	firstAdmin, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: user.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(start),
	})
	require.NoError(t, err)
	_, err = EndRoleBinding(ctx, ss, firstAdmin.ID, start.Add(10*time.Millisecond))
	require.True(t, store.IsConflict(err), "End(last system administrator) error")
	secondUser := saveUserWithPassword(t, ctx, ss)
	secondAdmin, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: secondUser.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(start),
	})
	require.NoError(t, err)
	_, err = EndRoleBinding(ctx, ss, firstAdmin.ID, start.Add(10*time.Millisecond))
	require.NoError(t, err)
	thirdUser := saveUserWithPassword(t, ctx, ss)
	thirdAdmin, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: thirdUser.ID, RoleID: adminRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeUTC(start),
	})
	require.NoError(t, err)
	startRace := make(chan struct{})
	endErrors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{secondAdmin.ID.String(), thirdAdmin.ID.String()} {
		id := id
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-startRace
			_, endErr := EndRoleBinding(ctx, ss, model.RoleBindingID(id), start.Add(20*time.Millisecond))
			endErrors <- endErr
		}()
	}
	close(startRace)
	workers.Wait()
	close(endErrors)
	succeeded, conflicted := 0, 0
	for endErr := range endErrors {
		switch {
		case endErr == nil:
			succeeded++
		case store.IsConflict(endErr):
			conflicted++
		default:
			require.FailNow(t, fmt.Sprintf("concurrent administrator End() error = %v", endErr))
		}
	}
	require.Equal(t, 1, succeeded, "concurrent administrator End() results = success")
	require.Equal(t, 1, conflicted, "concurrent administrator End() results = success")
}

func testRoleBindingActiveIntervalPrecision(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name: "precise-binding-reader", DisplayName: "Precise Binding Reader",
		Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	start := time.Date(2026, 9, 5, 12, 0, 0, 123200000, time.FixedZone("offset", 3600))
	end := start.Add(time.Second + 500*time.Microsecond)
	binding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: start, EndsAt: model.OptionalTimeFrom(end),
	})
	require.NoError(t, err)
	for _, test := range []struct {
		name   string
		at     time.Time
		active bool
	}{
		{"before start", start.Add(-time.Microsecond), false},
		{"at start", start, true},
		{"before end", end.Add(-time.Microsecond), true},
		{"submicrosecond before end", end.Add(-time.Nanosecond), true},
		{"at end", end, false},
		{"after end in same millisecond", end.Add(time.Microsecond), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			active, err := ss.RoleBinding().ListActiveByUser(ctx, user.ID, test.at)
			require.NoError(t, err)
			if test.active {
				require.Len(t, active, 1, "active bindings at %v", test.at)
				require.Equal(t, binding.ID, active[0].ID, "active bindings at %v", test.at)
			} else {
				require.Empty(t, active, "active bindings at %v", test.at)
			}
		})
	}
}
