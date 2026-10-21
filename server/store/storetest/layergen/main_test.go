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
)

func TestRenderTypeRejectsUnsupportedContractSyntax(t *testing.T) {
	t.Parallel()

	_, err := renderType(&ast.ChanType{Value: ast.NewIdent("string")}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported store contract type") {
		t.Fatalf("renderType() error = %v", err)
	}
}

func TestMethodIdentifierPreservesIDInitialism(t *testing.T) {
	t.Parallel()

	if got := methodIdentifier("GetAcademicUnitID"); got != "GetAcademicUnitID" {
		t.Fatalf("methodIdentifier(Id) = %q", got)
	}
	if got := methodIdentifier("GetByIDs"); got != "GetByIDs" {
		t.Fatalf("methodIdentifier(Ids) = %q", got)
	}
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
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	interfaces, _, err := parseInterfaces(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, spec := range interfaces {
		names = append(names, spec.name)
	}
	if got := strings.Join(names, ","); got != "Store,ZebraStore,AppleStore" {
		t.Fatalf("interface order = %s", got)
	}
	var methods []string
	for _, method := range interfaces[0].methods {
		methods = append(methods, method.name)
	}
	if got := strings.Join(methods, ","); got != "Zebra,Apple,Close" {
		t.Fatalf("Store methods = %s", got)
	}
}
