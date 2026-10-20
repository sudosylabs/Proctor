// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
	if err := command.Execute(); err != nil {
		t.Fatalf("errors check: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "verified") {
		t.Fatalf("errors check output = %q", output.String())
	}
}
