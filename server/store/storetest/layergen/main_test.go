// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderTypeRejectsUnsupportedContractSyntax(t *testing.T) {
	t.Parallel()

	_, err := renderType(&ast.ChanType{Value: ast.NewIdent("string")}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported store contract type")
}

func TestMethodIdentifierPreservesIDInitialism(t *testing.T) {
	t.Parallel()

	require.Equal(t, "GetAcademicUnitID", methodIdentifier("GetAcademicUnitID"))
	require.Equal(t, "GetByIDs", methodIdentifier("GetByIDs"))
}

func TestParseInterfacesExpandsCatalogAndOrdersByAccessor(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	files := map[string]string{
		"store.go": `package store

type Catalog interface {
	Zebra() ZebraStore
	Apple() AppleStore
}

type Store interface {
	Catalog

	Close() error
}
`,
		"apple.go": "package store\n\ntype AppleStore interface{ Get() error }\n",
		"zebra.go": "package store\n\ntype ZebraStore interface{ Get() error }\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600))
	}
	interfaces, _, err := parseInterfaces(directory)
	require.NoError(t, err)
	var names []string
	for _, spec := range interfaces {
		names = append(names, spec.name)
	}
	require.Equal(t, "Store,ZebraStore,AppleStore", strings.Join(names, ","))
	var methods []string
	for _, method := range interfaces[0].methods {
		methods = append(methods, method.name)
	}
	require.Equal(t, "Zebra,Apple,Close", strings.Join(methods, ","))
}
