// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuditRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	valid := auditRow{ID: model.NewAuditEventID().String(), CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(11, 0).UTC(), Action: string(model.ActionAuditView), ResourceType: model.ResourceInstitution, ResourceID: model.NewInstitutionID().String(), ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), Status: model.AuditStatusSuccess, NodeID: "node"}
	tests := []struct {
		name, field string
		row         auditRow
	}{
		{name: "id", field: "id", row: replaceAuditRow(valid, func(row *auditRow) { row.ID = "bad" })},
		{name: "actor", field: "actor_id", row: replaceAuditRow(valid, func(row *auditRow) { row.ActorID = sql.NullString{String: "bad", Valid: true} })},
		{name: "session", field: "session_id", row: replaceAuditRow(valid, func(row *auditRow) { row.SessionID = sql.NullString{String: "bad", Valid: true} })},
		{name: "resource", field: "resource_id", row: replaceAuditRow(valid, func(row *auditRow) { row.ResourceID = "bad" })},
		{name: "scope", field: "scope_id", row: replaceAuditRow(valid, func(row *auditRow) { row.ScopeID = "bad" })},
		{name: "domain", field: "status", row: replaceAuditRow(valid, func(row *auditRow) { row.Status = "unknown" })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			var persisted *persistedStateError
			require.ErrorAs(t, err, &persisted, "model() error")
			require.Equal(t, "audit_event", persisted.Entity, "model() error")
			require.Equal(t, test.field, persisted.Field, "model() error")
		})
	}
}

func TestAuditRowRehydrationAllowsAbsentOptionalActorAndSession(t *testing.T) {
	row := auditRow{ID: model.NewAuditEventID().String(), CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(11, 0).UTC(), Action: string(model.ActionAuditView), ResourceType: model.ResourceInstitution, ResourceID: model.NewInstitutionID().String(), ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), Status: model.AuditStatusAttempt, NodeID: "node"}
	got, err := row.model()
	require.NoError(t, err)
	require.True(t, got.ActorID.IsZero(), "optional IDs = %q, %q, want absent", got.ActorID, got.SessionID)
	require.True(t, got.SessionID.IsZero(), "optional IDs = %q, %q, want absent", got.ActorID, got.SessionID)
}

func replaceAuditRow(row auditRow, replace func(*auditRow)) auditRow { replace(&row); return row }
