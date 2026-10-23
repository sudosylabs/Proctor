// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackageTargetCreatesBinaryWebappAndConfigurationExamples(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "proctor-release")
	webapp := createPackageTestWebapp(t)
	command := exec.Command("make", "package", "PACKAGE_DIR="+output, "WEBAPP_DIST_DIR="+webapp)
	result, err := command.CombinedOutput()
	require.NoError(t, err, "make package: %v\n%s", err, result)
	for _, path := range []string{
		filepath.Join(output, "proctor"),
		filepath.Join(output, "config", "config.example.json"),
		filepath.Join(output, "config", "examples", "cas-provider.json"),
		filepath.Join(output, "config", "examples", "oidc-provider.json"),
		filepath.Join(output, "webapp", "dist", "index.html"),
		filepath.Join(output, "webapp", "dist", "webapp-build.json"),
		filepath.Join(output, "webapp", "dist", "assets", "index-Test123.js"),
	} {
		_, err = os.Stat(path)
		require.NoError(t, err, "package artifact %q", path)
	}
	_, err = os.Stat(filepath.Join(output, "config", "config.json"))
	require.True(t, os.IsNotExist(err), "package contains active configuration")
}

func createPackageTestWebapp(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	assets := filepath.Join(directory, "assets")
	require.NoError(t, os.MkdirAll(assets, 0o700))
	for name, data := range map[string]string{
		"index.html":              "<!doctype html><title>Proctor</title>",
		"webapp-build.json":       `{"version":"dev","commit":"unknown"}`,
		"assets/index-Test123.js": "export {};",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(data), 0o600))
	}
	return directory
}

func TestPackageTargetRejectsAnExistingOutputDirectory(t *testing.T) {
	t.Parallel()

	output := filepath.Join(t.TempDir(), "existing-release")
	active := filepath.Join(output, "config", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(active), 0o700))
	require.NoError(t, os.WriteFile(active, []byte(`{"Secret":"operator-owned"}`), 0o600))
	command := exec.Command("make", "package", "PACKAGE_DIR="+output)
	result, err := command.CombinedOutput()
	require.Error(t, err, "make package reused an existing output directory:\n%s", result)
	data, err := os.ReadFile(active)
	require.NoError(t, err)
	require.Equal(t, `{"Secret":"operator-owned"}`, string(data), "active configuration was changed")
}
