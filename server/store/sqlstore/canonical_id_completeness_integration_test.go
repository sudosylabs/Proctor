//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type canonicalIDColumn struct {
	Table  string `db:"table_name"`
	Column string `db:"column_name"`
}

type canonicalIDConstraint struct {
	Name       string `db:"constraint_name"`
	Definition string `db:"definition"`
}

func TestCanonicalIDConstraintCompleteness(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	var columns []canonicalIDColumn
	if err := persistence.GetMaster().Select(ctx, &columns, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND data_type = 'character varying'
		   AND character_maximum_length = 26
		 ORDER BY table_name, column_name`); err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 {
		t.Fatal("canonical-ID inventory found no varchar(26) columns")
	}

	for _, column := range columns {
		qualified := column.Table + "." + column.Column
		constraintName := fmt.Sprintf("%s_%s_canonical_check", column.Table, column.Column)
		var constraint canonicalIDConstraint
		if err := persistence.GetMaster().Get(ctx, &constraint, `
			SELECT con.conname AS constraint_name,
			       pg_get_constraintdef(con.oid) AS definition
			  FROM pg_constraint con
			  JOIN pg_class relation ON relation.oid = con.conrelid
			  JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			 WHERE namespace.nspname = 'public'
			   AND relation.relname = ?
			   AND con.conname = ?
			   AND con.contype = 'c'`, column.Table, constraintName); err != nil {
			t.Errorf("%s lacks named canonical-ID constraint %s: %v", qualified, constraintName, err)
			continue
		}
		if !strings.Contains(constraint.Definition, "ybndrfg8ejkmcpqxot1uwisza345h769") ||
			!strings.Contains(constraint.Definition, "{26}") {
			t.Errorf("%s constraint %s does not enforce the canonical identifier predicate: %s", qualified, constraint.Name, constraint.Definition)
		}
	}
}
