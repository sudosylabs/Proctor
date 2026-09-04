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
	"errors"
	"testing"
	"time"

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
	requireNoError(t, err)
	got, err := ss.Audit().Get(ctx, event.ID.String())
	requireNoError(t, err)
	if got.Status != model.AuditStatusAttempt || string(got.Parameters) == "" {
		t.Fatalf("Get() = %#v", got)
	}
	completed, err := ss.Audit().Complete(
		ctx, event.ID.String(), model.AuditStatusSuccess, "", []byte(`{"updated":true}`),
		model.MillisFromTime(event.UpdatedAt)+1,
	)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("Complete() = %#v", completed)
	}
	if _, err := ss.Audit().Complete(
		ctx, event.ID.String(), model.AuditStatusFail, "late", nil, model.MillisFromTime(event.UpdatedAt)+2,
	); !store.IsNotFound(err) {
		t.Fatalf("second Complete() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAuditView),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	requireNoError(t, err)
	withoutVisibility, err := ss.Audit().List(ctx, store.AuditListOptions{ActorId: user.ID.String(), Limit: 10})
	requireNoError(t, err)
	if len(withoutVisibility) != 0 {
		t.Fatalf("List(zero visibility) = %#v, want empty", withoutVisibility)
	}
	list, err := ss.Audit().List(ctx, store.AuditListOptions{
		ActorId: user.ID.String(), Limit: 1,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("List() = %#v", list)
	}
	next, err := ss.Audit().List(ctx, store.AuditListOptions{
		ActorId: user.ID.String(), Limit: 10,
		BeforeTime: model.MillisFromTime(list[0].CreatedAt), BeforeId: list[0].ID.String(),
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(next) != 1 || next[0].ID != event.ID {
		t.Fatalf("cursor List() = %#v", next)
	}

	fixture := saveClassFixture(t, ctx, ss)
	visibleClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "audit-visible-class")
	sibling := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "audit-sibling")
	visibleUnitEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAcademicUnitMembersManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: fixture.programme.AcademicUnitID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	requireNoError(t, err)
	visibleClassEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionClassMembersManage),
		Resource:  model.Resource{Type: model.ResourceClass, ID: visibleClass.ID.String()},
		ScopeType: model.RoleScopeClass, ScopeID: visibleClass.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	requireNoError(t, err)
	siblingUnitEvent, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionAcademicUnitMembersManage),
		Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: sibling.ID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: sibling.ID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	requireNoError(t, err)
	_, err = ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionExternalIdentityManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		Status: model.AuditStatusSuccess, NodeID: "test-node",
	})
	requireNoError(t, err)
	scoped, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()},
			AllowedActions:      []string{string(model.ActionAcademicUnitMembersManage), string(model.ActionClassMembersManage)},
		},
	})
	requireNoError(t, err)
	seen := map[model.AuditEventID]bool{}
	for _, item := range scoped {
		seen[item.ID] = true
	}
	if len(scoped) != 2 || !seen[visibleUnitEvent.ID] || !seen[visibleClassEvent.ID] {
		t.Fatalf("scoped academic audit = %#v", scoped)
	}
	academicInstitutionWide, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
			AllowedActions: []string{
				string(model.ActionAcademicUnitMembersManage), string(model.ActionClassMembersManage),
			},
		},
	})
	requireNoError(t, err)
	seen = map[model.AuditEventID]bool{}
	for _, item := range academicInstitutionWide {
		seen[item.ID] = true
	}
	if len(academicInstitutionWide) != 3 || !seen[visibleUnitEvent.ID] || !seen[visibleClassEvent.ID] ||
		!seen[siblingUnitEvent.ID] {
		t.Fatalf("institution academic audit = %#v", academicInstitutionWide)
	}
	withoutAcademicActions, err := ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
		},
	})
	requireNoError(t, err)
	if len(withoutAcademicActions) != 0 {
		t.Fatalf("institution academic audit without action catalog = %#v, want empty", withoutAcademicActions)
	}
	_, err = ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			InstitutionWide:         true,
			AcademicInstitutionWide: true,
			AllowedActions:          []string{string(model.ActionAcademicUnitMembersManage)},
		},
	})
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("conflicting institution audit visibility error = %v, want invalid input", err)
	}
	_, err = ss.Audit().List(ctx, store.AuditListOptions{
		Limit: 10,
		Visibility: store.AuditVisibilityScope{
			AcademicInstitutionWide: true,
			AcademicUnitRootIDs:     []string{fixture.programme.AcademicUnitID.String()},
			AllowedActions:          []string{string(model.ActionAcademicUnitMembersManage)},
		},
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("mixed academic audit visibility error = %v, want invalid input", err)
	}
}
