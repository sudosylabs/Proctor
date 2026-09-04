// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedEntityIDsAreFresh(t *testing.T) {
	t.Parallel()

	generatedPath := filepath.Join(t.TempDir(), "id_gen.go")
	command := exec.Command("go", "run", "./internal/idgen", "-source", "id.go", "-output", generatedPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("regenerate typed IDs: %v\n%s", err, output)
	}
	want, err := os.ReadFile("id_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("id_gen.go is stale; run go generate ./model")
	}
}
