// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPICommandBuildsAndChecksArtifact(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := filepath.Join(directory, "openapi")
	if err := os.MkdirAll(filepath.Join(source, "fragments", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if err := Execute(context.Background(), commandArguments("build"), &commandOutput, &commandOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commandOutput.String(), "wrote") {
		t.Fatalf("build output = %q", commandOutput.String())
	}
	commandOutput.Reset()
	if err := Execute(context.Background(), commandArguments("check"), &commandOutput, &commandOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commandOutput.String(), "validated") {
		t.Fatalf("check output = %q", commandOutput.String())
	}

	if err := os.WriteFile(output, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commandOutput.Reset()
	err := Execute(context.Background(), commandArguments("check"), &commandOutput, &commandOutput)
	if err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("check error = %v", err)
	}
}

func writeOpenAPITestFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
