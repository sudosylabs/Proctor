// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAPICommandBuildsAndChecksArtifact(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := filepath.Join(directory, "openapi")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "fragments", "system"), 0o755))
	writeOpenAPITestFile(t, filepath.Join(source, "base.yaml"), `openapi: 3.1.0
info:
  title: Proctor API
  version: 1.0.0
tags:
  - name: System
    description: Runtime health.
`)
	writeOpenAPITestFile(t, filepath.Join(source, "fragments", "system", "health.yaml"), `paths:
  /health/live:
    get:
      operationId: getHealthLive
      summary: Check liveness
      tags: [System]
      security: []
      x-proctor-auth: public
      x-proctor-error-codes: []
      x-proctor-idempotency: none
      responses:
        "204":
          description: The process is live.
`)
	output := filepath.Join(directory, "openapi.json")
	var commandOutput bytes.Buffer
	arguments := []string{"openapi", "--source", source, "--output", output}
	commandArguments := func(action string) []string {
		return append(append([]string(nil), arguments...), action)
	}
	require.NoError(t, Execute(context.Background(), commandArguments("build"), &commandOutput, &commandOutput))
	require.Contains(t, commandOutput.String(), "wrote")
	commandOutput.Reset()
	require.NoError(t, Execute(context.Background(), commandArguments("check"), &commandOutput, &commandOutput))
	require.Contains(t, commandOutput.String(), "validated")

	require.NoError(t, os.WriteFile(output, []byte("{}\n"), 0o600))
	commandOutput.Reset()
	err := Execute(context.Background(), commandArguments("check"), &commandOutput, &commandOutput)
	require.Error(t, err)
	require.Contains(t, err.Error(), "is stale")
}

func writeOpenAPITestFile(t *testing.T, name, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(contents), 0o600))
}
