//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthorizationAuditCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	at := model.NowUTC()
	canonical := model.NewId()
	tests := []struct{ name, query, constraint string }{
		{name: "role id", query: `INSERT INTO roles (id, created_at, updated_at, name, display_name) VALUES ('bad', ?, ?, 'bad-role', 'Bad')`, constraint: "roles_id_canonical_check"},
		{name: "binding id", query: `INSERT INTO role_bindings (id, created_at, updated_at, user_id, role_id, scope_type, scope_id, start_at) VALUES ('bad', ?, ?, ?, ?, 'institution', ?, ?)`, constraint: "role_bindings_id_canonical_check"},
		{name: "binding user", query: `INSERT INTO role_bindings (id, created_at, updated_at, user_id, role_id, scope_type, scope_id, start_at) VALUES (?, ?, ?, 'bad', ?, 'institution', ?, ?)`, constraint: "role_bindings_user_id_canonical_check"},
		{name: "binding role", query: `INSERT INTO role_bindings (id, created_at, updated_at, user_id, role_id, scope_type, scope_id, start_at) VALUES (?, ?, ?, ?, 'bad', 'institution', ?, ?)`, constraint: "role_bindings_role_id_canonical_check"},
		{name: "binding scope", query: `INSERT INTO role_bindings (id, created_at, updated_at, user_id, role_id, scope_type, scope_id, start_at) VALUES (?, ?, ?, ?, ?, 'institution', 'bad', ?)`, constraint: "role_bindings_scope_id_canonical_check"},
		{name: "audit id", query: `INSERT INTO audit_events (id, created_at, updated_at, action, resource_type, resource_id, scope_type, scope_id, status, node_id) VALUES ('bad', ?, ?, 'audit.view', 'institution', ?, 'institution', ?, 'success', 'node')`, constraint: "audit_events_id_canonical_check"},
		{name: "audit actor", query: `INSERT INTO audit_events (id, created_at, updated_at, actor_id, action, resource_type, resource_id, scope_type, scope_id, status, node_id) VALUES (?, ?, ?, 'bad', 'audit.view', 'institution', ?, 'institution', ?, 'success', 'node')`, constraint: "audit_events_actor_id_canonical_check"},
		{name: "audit session", query: `INSERT INTO audit_events (id, created_at, updated_at, session_id, action, resource_type, resource_id, scope_type, scope_id, status, node_id) VALUES (?, ?, ?, 'bad', 'audit.view', 'institution', ?, 'institution', ?, 'success', 'node')`, constraint: "audit_events_session_id_canonical_check"},
		{name: "audit resource", query: `INSERT INTO audit_events (id, created_at, updated_at, action, resource_type, resource_id, scope_type, scope_id, status, node_id) VALUES (?, ?, ?, 'audit.view', 'institution', 'bad', 'institution', ?, 'success', 'node')`, constraint: "audit_events_resource_id_canonical_check"},
		{name: "audit scope", query: `INSERT INTO audit_events (id, created_at, updated_at, action, resource_type, resource_id, scope_type, scope_id, status, node_id) VALUES (?, ?, ?, 'audit.view', 'institution', ?, 'institution', 'bad', 'success', 'node')`, constraint: "audit_events_scope_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := canonicalConstraintArgs(test.name, canonical, at)
			_, err := persistence.GetMaster().Exec(ctx, test.query, args...)
			var postgresErr *pq.Error
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want %s", err, test.constraint)
			}
		})
	}
}

func canonicalConstraintArgs(name, canonical string, at time.Time) []any {
	switch name {
	case "role id":
		return []any{at, at}
	case "binding id":
		return []any{at, at, canonical, canonical, canonical, at}
	case "binding user", "binding role", "binding scope":
		return []any{canonical, at, at, canonical, canonical, at}
	case "audit id":
		return []any{at, at, canonical, canonical}
	case "audit actor", "audit session":
		return []any{canonical, at, at, canonical, canonical}
	case "audit resource", "audit scope":
		return []any{canonical, at, at, canonical}
	default:
		panic("unknown canonical constraint fixture: " + name)
	}
}
