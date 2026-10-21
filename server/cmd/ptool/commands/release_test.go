// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteDeterministicArchive(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "package")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "proctor"), []byte("binary"), 0o755)) //nolint:gosec // The packaged binary fixture must be executable.
	require.NoError(t, os.WriteFile(filepath.Join(source, "config", "config.example.json"), []byte("{}\n"), 0o600))

	epoch := time.Unix(1_700_000_000, 0).UTC()
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	require.NoError(t, writeDeterministicArchive(source, first, "proctor-test", epoch))
	require.NoError(t, writeDeterministicArchive(source, second, "proctor-test", epoch))
	firstBytes, err := os.ReadFile(first)
	require.NoError(t, err)
	secondBytes, err := os.ReadFile(second)
	require.NoError(t, err)
	require.True(t, bytes.Equal(firstBytes, secondBytes), "archives created from identical input differ")

	entries := readArchiveEntries(t, firstBytes)
	want := []archiveEntry{
		{name: "proctor-test/", mode: 0o755, modifiedAt: epoch.Unix()},
		{name: "proctor-test/config/", mode: 0o755, modifiedAt: epoch.Unix()},
		{name: "proctor-test/config/config.example.json", mode: 0o644, modifiedAt: epoch.Unix()},
		{name: "proctor-test/proctor", mode: 0o755, modifiedAt: epoch.Unix()},
	}
	require.Equal(t, want, entries, "archive entries")
}

type archiveEntry struct {
	name       string
	mode       int64
	modifiedAt int64
}

func readArchiveEntries(t *testing.T, compressed []byte) []archiveEntry {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	var entries []archiveEntry
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		require.NoError(t, err)
		entries = append(entries, archiveEntry{name: header.Name, mode: header.Mode, modifiedAt: header.ModTime.Unix()})
	}
}

func TestWriteDeterministicArchiveRejectsSymlinks(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	require.NoError(t, os.Symlink("missing", filepath.Join(source, "link")))
	err := writeDeterministicArchive(source, filepath.Join(t.TempDir(), "output.tar.gz"), "proctor-test", time.Unix(0, 0))
	require.Error(t, err, "expected symbolic link rejection")
}
