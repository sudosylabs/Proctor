// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/audit_store.go.

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAuditStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionRoleManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
		Parameters: []byte(`{"role_id":"safe"}`),
	})
	require.NoError(t, err)
	got, err := ss.Audit().Get(ctx, event.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, got.Status)
	require.NotEmpty(t, string(got.Parameters))
	completed, err := ss.Audit().Complete(
		ctx, event.ID, model.AuditStatusSuccess, "", []byte(`{"updated":true}`),
		model.MillisFromTime(event.UpdatedAt)+1,
	)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	_, err = ss.Audit().Complete(
		ctx, event.ID, model.AuditStatusFail, "late", nil, model.MillisFromTime(event.UpdatedAt)+2,
	)
	require.True(t, store.IsNotFound(err), "second Complete() error")
	time.Sleep(2 * time.Millisecond)
	second, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAuditView),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	require.NoError(t, err)
	withoutVisibility, err := ss.Audit().List(ctx, store.AuditListOptions{ActorID: user.ID, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, withoutVisibility, "List(zero visibility)")
	list, err := ss.Audit().List(ctx, store.AuditListOptions{
		ActorID: user.ID, Limit: 1,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, second.ID, list[0].ID)
	next, err := ss.Audit().List(ctx, store.AuditListOptions{
		ActorID: user.ID, Limit: 10,
		BeforeCreatedAt: list[0].CreatedAt, BeforeID: list[0].ID,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, event.ID, next[0].ID)

	fixture := saveClassFixture(t, ctx, ss)
	visibleClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "audit-visible-class")
	sibling := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "audit-sibling")
	visibleUnitEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAcademicUnitMembersManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: fixture.programme.AcademicUnitID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	require.NoError(t, err)
	visibleClassEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionClassMembersManage),
		Resource:  model.Resource{Type: model.ResourceClass, ID: visibleClass.ID.String()},
		ScopeType: model.RoleScopeClass, ScopeID: visibleClass.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	require.NoError(t, err)
	siblingUnitEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAcademicUnitMembersManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: sibling.ID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: sibling.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	require.NoError(t, err)
	_, err = ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionExternalIdentityManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	require.NoError(t, err)
	scoped, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID},
			AllowedActions:      []string{string(model.ActionAcademicUnitMembersManage), string(model.ActionClassMembersManage)},
		},
	})
	require.NoError(t, err)
	seen := map[model.AuditEventID]bool{}
	for _, item := range scoped {
		seen[item.ID] = true
	}
	require.Len(t, scoped, 2)
	require.True(t, seen[visibleUnitEvent.ID])
	require.True(t, seen[visibleClassEvent.ID])
	academicInstitutionWide, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
			AllowedActions: []string{
				string(model.ActionAcademicUnitMembersManage), string(model.ActionClassMembersManage),
			},
		},
	})
	require.NoError(t, err)
	seen = map[model.AuditEventID]bool{}
	for _, item := range academicInstitutionWide {
		seen[item.ID] = true
	}
	require.Len(t, academicInstitutionWide, 3)
	require.True(t, seen[visibleUnitEvent.ID])
	require.True(t, seen[visibleClassEvent.ID])
	require.True(t, seen[siblingUnitEvent.ID])
	withoutAcademicActions, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
		},
	})
	require.NoError(t, err)
	require.Empty(t, withoutAcademicActions, "institution academic audit without action catalog")
	_, err = ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			InstitutionWide:         true,
			AcademicInstitutionWide: true,
			AllowedActions:          []string{string(model.ActionAcademicUnitMembersManage)},
		},
	})
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "conflicting institution audit visibility error")
	_, err = ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
			AcademicUnitRootIDs:     []model.AcademicUnitID{fixture.programme.AcademicUnitID},
			AllowedActions:          []string{string(model.ActionAcademicUnitMembersManage)},
		},
	})
	require.ErrorAs(t, err, &invalid, "mixed academic audit visibility error")

	testAuditAbandonedAttempts(t, ctx, ss, institution.ID.String(), user.ID)
}

func testAuditAbandonedAttempts(t *testing.T, ctx context.Context, ss store.Store, institutionID string, userID model.UserID) {
	t.Helper()
	_, err := ss.Audit().FailAbandonedAttempts(ctx, &store.AuditAttemptAbandonment{Age: time.Hour})
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "abandonment without a limit")
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: userID, Action: string(model.ActionRoleManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institutionID},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID,
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	_, err = ss.Audit().FailAbandonedAttempts(ctx, &store.AuditAttemptAbandonment{Age: time.Hour, Limit: 1000})
	require.NoError(t, err)
	fresh, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, fresh.Status, "an attempt younger than the bound stays open")

	time.Sleep(5 * time.Millisecond)
	for {
		changed, sweepErr := ss.Audit().FailAbandonedAttempts(ctx, &store.AuditAttemptAbandonment{Age: time.Millisecond, Limit: 1000})
		require.NoError(t, sweepErr)
		if changed < 1000 {
			break
		}
	}
	abandoned, err := ss.Audit().Get(ctx, attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusFail, abandoned.Status)
	require.Equal(t, model.AuditErrorAttemptAbandoned, abandoned.ErrorCode)
	require.False(t, abandoned.UpdatedAt.Before(attempt.UpdatedAt), "abandonment never moves updated_at backwards")
	_, err = ss.Audit().Complete(ctx, attempt.ID, model.AuditStatusSuccess, "", nil, model.MillisFromTime(abandoned.UpdatedAt)+1)
	require.True(t, store.IsNotFound(err), "an abandoned attempt is terminal")
}
