// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/storetest/audit_store.go.

package storetest

import (
	"context"
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
		ActorId: user.Id, Action: string(model.ActionRoleManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
		ScopeType: model.RoleScopeInstitution, ScopeId: institution.Id,
		Status: model.AuditStatusAttempt, NodeId: "test-node",
		Parameters: []byte(`{"role_id":"safe"}`),
	})
	requireNoError(t, err)
	got, err := ss.Audit().Get(ctx, event.Id)
	requireNoError(t, err)
	if got.Status != model.AuditStatusAttempt || string(got.Parameters) == "" {
		t.Fatalf("Get() = %#v", got)
	}
	completed, err := ss.Audit().Complete(
		ctx, event.Id, model.AuditStatusSuccess, "", []byte(`{"updated":true}`),
		event.UpdateAt+1,
	)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("Complete() = %#v", completed)
	}
	if _, err := ss.Audit().Complete(
		ctx, event.Id, model.AuditStatusFail, "late", nil, event.UpdateAt+2,
	); !store.IsNotFound(err) {
		t.Fatalf("second Complete() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorId: user.Id, Action: string(model.ActionAuditView),
		Resource:  model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
		ScopeType: model.RoleScopeInstitution, ScopeId: institution.Id,
		Status: model.AuditStatusSuccess, NodeId: "test-node",
	})
	requireNoError(t, err)
	list, err := ss.Audit().List(ctx, store.AuditListOptions{ActorId: user.Id, Limit: 1})
	requireNoError(t, err)
	if len(list) != 1 || list[0].Id != second.Id {
		t.Fatalf("List() = %#v", list)
	}
	next, err := ss.Audit().List(ctx, store.AuditListOptions{
		ActorId: user.Id, Limit: 10,
		BeforeTime: list[0].CreateAt, BeforeId: list[0].Id,
	})
	requireNoError(t, err)
	if len(next) != 1 || next[0].Id != event.Id {
		t.Fatalf("cursor List() = %#v", next)
	}
}
