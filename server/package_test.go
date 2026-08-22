// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPackageTargetCreatesOnlyBinaryAndConfigurationExamples(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "proctor-release")
	command := exec.Command("make", "package", "PACKAGE_DIR="+output)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("make package: %v\n%s", err, result)
	}
	for _, path := range []string{
		filepath.Join(output, "proctor"),
		filepath.Join(output, "config", "config.example.json"),
		filepath.Join(output, "config", "examples", "execution-host.json"),
		filepath.Join(output, "config", "examples", "cas-provider.json"),
		filepath.Join(output, "config", "examples", "oidc-provider.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("package artifact %q: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "config", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("package contains active configuration: %v", err)
	}
}

func TestPackageTargetRejectsAnExistingOutputDirectory(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "existing-release")
	active := filepath.Join(output, "config", "config.json")
	if err := os.MkdirAll(filepath.Dir(active), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte(`{"Secret":"operator-owned"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "package", "PACKAGE_DIR="+output)
	if result, err := command.CombinedOutput(); err == nil {
		t.Fatalf("make package reused an existing output directory:\n%s", result)
	}
	data, err := os.ReadFile(active)
	if err != nil {
		t.Fatalf("active configuration was removed: %v", err)
	}
	if string(data) != `{"Secret":"operator-owned"}` {
		t.Fatalf("active configuration was changed: %s", data)
	}
}
