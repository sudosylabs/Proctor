// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestErrorCatalogAgreesWithServerSource fails when production source mints
// an application error code the catalog does not list, when the transport
// mints an application-only code, when a listed code is never minted, or when
// the generated HTTP mapping is stale.
func TestErrorCatalogAgreesWithServerSource(t *testing.T) {
	module := filepath.Join("..", "..", "..")
	command := newErrorsCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{
		"check",
		"--module", module,
		"--catalog", filepath.Join(module, "httpapi", "error_catalog.yaml"),
		"--mapping", filepath.Join(module, "httpapi", "error_mapping.go"),
	})
	err := command.Execute()
	require.NoError(t, err, "errors check: %v\n%s", err, output.String())
	require.Contains(t, output.String(), "verified")
}

// TestStoreTableConstraintCheckRejectsUnproducedConstraints proves the check
// reports a StoreTable entry no Store literal or SQL object produces, and
// accepts entries the Store mints or the schema names.
func TestStoreTableConstraintCheckRejectsUnproducedConstraints(t *testing.T) {
	module := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(module, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	write("app/table.go", `package app

import "example/apperror"

var table = apperror.StoreTable{Constraints: map[string]apperror.Code{
	"minted_conflict": "a.conflict",
	"schema_unique":   "a.conflict",
	"dead_conflict":   "a.conflict",
}}
`)
	write("app/table_test.go", `package app

var _ = map[string]string{"test_only": ""}
`)
	write("store/sqlstore/store.go", `package sqlstore

var minted = "minted_conflict"
`)
	write("store/sqlstore/store_test.go", `package sqlstore

var dead = "dead_conflict"
`)
	write("migrations/postgres/000001_baseline.up.sql", "CREATE UNIQUE INDEX schema_unique ON t (c);\n")
	err := verifyStoreTableConstraints(module)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"dead_conflict"`)
	require.NotContains(t, err.Error(), "minted_conflict")
	require.NotContains(t, err.Error(), "schema_unique")
}
