// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"go/ast"
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

	if got := methodIdentifier("GetAcademicUnitId"); got != "GetAcademicUnitID" {
		t.Fatalf("methodIdentifier(Id) = %q", got)
	}
	if got := methodIdentifier("GetByIds"); got != "GetByIDs" {
		t.Fatalf("methodIdentifier(Ids) = %q", got)
	}
}
