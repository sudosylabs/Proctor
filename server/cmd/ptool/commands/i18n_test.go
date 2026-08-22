// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestI18nCheckValidatesRepositoryCatalog(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"i18n", "--catalogs", "../../../i18n", "check"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	definitions, err := localizationDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("validated %d messages across 1 locale(s)", len(definitions))
	if got := output.String(); !strings.Contains(got, want) {
		t.Fatalf("output = %q", got)
	}
}

func TestI18nFormatIsExplicitAndStable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	name := filepath.Join(directory, "en.json")
	input := `[{"id":"z","translation":"last"},{"id":"a","translation":"first"}]`
	if err := os.WriteFile(name, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Execute(context.Background(), []string{"i18n", "--catalogs", directory, "format", "--check"}, &output, &output); err == nil {
		t.Fatal("format check accepted drift")
	}
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != input {
		t.Fatal("format --check changed the catalog")
	}
	output.Reset()
	if err := Execute(context.Background(), []string{"i18n", "--catalogs", directory, "format"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), "[\n  {\n    \"id\": \"a\"") {
		t.Fatalf("formatted catalog = %s", contents)
	}
}
