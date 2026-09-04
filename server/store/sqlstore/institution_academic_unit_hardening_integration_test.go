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

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstitutionAcademicUnitCanonicalIDConstraints(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)

	assertCanonicalCheckViolation(t, "institutions_id_canonical_check", func() error {
		at := model.NowUTC()
		_, err := persistence.GetMaster().Exec(ctx, `
			INSERT INTO institutions (id, created_at, updated_at, name, display_name)
			VALUES ('bad', ?, ?, 'invalid-id', 'Invalid ID')`, at, at)
		return err
	})

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "canonical-ids", DisplayName: "Canonical IDs",
	})
	if err != nil {
		t.Fatal(err)
	}
	at := model.NowUTC()
	tests := []struct {
		name       string
		constraint string
		id         string
		ownerID    string
		parentID   any
	}{
		{
			name:       "academic unit id",
			constraint: "academic_units_id_canonical_check",
			id:         "bad",
			ownerID:    institution.ID.String(),
		},
		{
			name:       "institution reference",
			constraint: "academic_units_institution_id_canonical_check",
			id:         model.NewAcademicUnitID().String(),
			ownerID:    "bad",
		},
		{
			name:       "nullable parent reference",
			constraint: "academic_units_parent_id_canonical_check",
			id:         model.NewAcademicUnitID().String(),
			ownerID:    institution.ID.String(),
			parentID:   "bad",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCanonicalCheckViolation(t, test.constraint, func() error {
				_, err := persistence.GetMaster().Exec(ctx, `
					INSERT INTO academic_units (
						id, created_at, updated_at, institution_id, parent_id, name, display_name
					) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					test.id, at, at, test.ownerID, test.parentID, test.name, test.name,
				)
				return err
			})
		})
	}
}

func assertCanonicalCheckViolation(t *testing.T, constraint string, operation func() error) {
	t.Helper()
	err := operation()
	var postgresErr *pq.Error
	if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != constraint {
		t.Fatalf("constraint violation = %v, want PostgreSQL check %s", err, constraint)
	}
}
