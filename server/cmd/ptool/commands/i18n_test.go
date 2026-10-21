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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestI18nCheckValidatesRepositoryCatalog(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	require.NoError(t, Execute(context.Background(), []string{"i18n", "--catalogs", "../../../i18n", "check"}, &output, &output))
	definitions, err := localizationDefinitions()
	require.NoError(t, err)
	want := fmt.Sprintf("validated %d messages across 1 locale(s)", len(definitions))
	require.Contains(t, output.String(), want)
}

func TestI18nFormatIsExplicitAndStable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	name := filepath.Join(directory, "en.json")
	input := `[{"id":"z","translation":"last"},{"id":"a","translation":"first"}]`
	require.NoError(t, os.WriteFile(name, []byte(input), 0o600))
	var output bytes.Buffer
	require.Error(t, Execute(context.Background(), []string{"i18n", "--catalogs", directory, "format", "--check"}, &output, &output), "format check accepted drift")
	contents, err := os.ReadFile(name)
	require.NoError(t, err)
	require.Equal(t, input, string(contents), "format --check changed the catalog")
	output.Reset()
	require.NoError(t, Execute(context.Background(), []string{"i18n", "--catalogs", directory, "format"}, &output, &output))
	contents, err = os.ReadFile(name)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(contents), "[\n  {\n    \"id\": \"a\""), "formatted catalog = %s", contents)
}
