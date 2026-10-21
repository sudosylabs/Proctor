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

	"github.com/stretchr/testify/require"
)

func TestGeneratedEntityIDsAreFresh(t *testing.T) {
	t.Parallel()

	generatedPath := filepath.Join(t.TempDir(), "id_gen.go")
	command := exec.Command("go", "run", "./internal/idgen", "-source", "id.go", "-output", generatedPath)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "regenerate typed IDs: %v\n%s", err, output)
	want, err := os.ReadFile("id_gen.go")
	require.NoError(t, err)
	got, err := os.ReadFile(generatedPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "id_gen.go is stale; run go generate ./model")
}
