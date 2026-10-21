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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type canonicalIDColumn struct {
	Table  string `db:"table_name"`
	Column string `db:"column_name"`
}

type canonicalIDConstraint struct {
	Name       string `db:"constraint_name"`
	Definition string `db:"definition"`
}

const postgresIdentifierMaxBytes = 63

func canonicalIDConstraintName(table, column string) string {
	name := fmt.Sprintf("%s_%s_canonical_check", table, column)
	if len(name) > postgresIdentifierMaxBytes {
		return name[:postgresIdentifierMaxBytes]
	}
	return name
}

func TestCanonicalIDConstraintCompleteness(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	var columns []canonicalIDColumn
	require.NoError(t, persistence.GetMaster().Select(ctx, &columns, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND data_type = 'character varying'
		   AND character_maximum_length = 26
		 ORDER BY table_name, column_name`))
	require.NotEmpty(t, columns, "canonical-ID inventory found no varchar(26) columns")

	for _, column := range columns {
		qualified := column.Table + "." + column.Column
		constraintName := canonicalIDConstraintName(column.Table, column.Column)
		var constraint canonicalIDConstraint
		if !assert.NoError(t, persistence.GetMaster().Get(ctx, &constraint, `
			SELECT con.conname AS constraint_name,
			       pg_get_constraintdef(con.oid) AS definition
			  FROM pg_constraint con
			  JOIN pg_class relation ON relation.oid = con.conrelid
			  JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			 WHERE namespace.nspname = 'public'
			   AND relation.relname = ?
			   AND con.conname::text = ?
			   AND con.contype = 'c'`, column.Table, constraintName), "%s lacks named canonical-ID constraint %s", qualified, constraintName) {
			continue
		}
		assert.Contains(t, constraint.Definition, "ybndrfg8ejkmcpqxot1uwisza345h769", "%s constraint %s does not enforce the canonical identifier predicate", qualified, constraint.Name)
		assert.Contains(t, constraint.Definition, "{26}", "%s constraint %s does not enforce the canonical identifier predicate", qualified, constraint.Name)
	}
}
