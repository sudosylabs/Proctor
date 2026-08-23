// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWriteDeterministicArchive(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(filepath.Join(source, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "proctor"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config", "config.example.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	epoch := time.Unix(1_700_000_000, 0).UTC()
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	if err := writeDeterministicArchive(source, first, "proctor-test", epoch); err != nil {
		t.Fatal(err)
	}
	if err := writeDeterministicArchive(source, second, "proctor-test", epoch); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("archives created from identical input differ")
	}

	entries := readArchiveEntries(t, firstBytes)
	want := []archiveEntry{
		{name: "proctor-test/", mode: 0o755, modifiedAt: epoch.Unix()},
		{name: "proctor-test/config/", mode: 0o755, modifiedAt: epoch.Unix()},
		{name: "proctor-test/config/config.example.json", mode: 0o644, modifiedAt: epoch.Unix()},
		{name: "proctor-test/proctor", mode: 0o755, modifiedAt: epoch.Unix()},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("archive entries = %#v, want %#v", entries, want)
	}
}

type archiveEntry struct {
	name       string
	mode       int64
	modifiedAt int64
}

func readArchiveEntries(t *testing.T, compressed []byte) []archiveEntry {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	var entries []archiveEntry
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archiveEntry{name: header.Name, mode: header.Mode, modifiedAt: header.ModTime.Unix()})
	}
}

func TestWriteDeterministicArchiveRejectsSymlinks(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	err := writeDeterministicArchive(source, filepath.Join(t.TempDir(), "output.tar.gz"), "proctor-test", time.Unix(0, 0))
	if err == nil {
		t.Fatal("expected symbolic link rejection")
	}
}
