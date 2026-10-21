// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRunWritesDeterministicRepresentativePreview(t *testing.T) {
	t.Parallel()

	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, output := range []string{first, second} {
		var stderr bytes.Buffer
		err := run([]string{"-output", output, "-catalogs", "../../i18n", "-templates", "../../templates"}, &stderr)
		require.NoError(t, err, "run(%q): %v (%s)", output, err, stderr.String())
	}

	firstIndex, err := os.ReadFile(filepath.Join(first, "index.html"))
	require.NoError(t, err)
	secondIndex, err := os.ReadFile(filepath.Join(second, "index.html"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(firstIndex, secondIndex), "preview index is not deterministic")
	require.False(t, bytes.Contains(firstIndex, []byte("@")), "preview index appears to contain a production-like email address")
	wantImage, err := os.ReadFile("../../templates/proctor-lockup-25d-v1.png")
	require.NoError(t, err)
	for _, output := range []string{first, second} {
		image, err := os.ReadFile(filepath.Join(output, "proctor-lockup-25d-v1.png"))
		require.NoError(t, err)
		require.True(t, bytes.Equal(image, wantImage))
	}
	keys := model.AllMailTemplateKeys()
	require.Len(t, keys, 46, "preview catalog keys")
	for _, catalogKey := range keys {
		key := string(catalogKey)
		body, err := os.ReadFile(filepath.Join(first, key+".html"))
		require.NoError(t, err, "%s HTML preview", key)
		require.Contains(t, string(body), `src="proctor-lockup-25d-v1.png"`, "%s preview image does not resolve locally", key)
		require.NotContains(t, string(body), `src="cid:`, "%s preview image does not resolve locally", key)
		_, err = os.Stat(filepath.Join(first, key+".txt"))
		require.NoError(t, err, "%s text preview", key)
		require.Equal(t, 1, bytes.Count(firstIndex, []byte(key+".html")), "%s preview links are incomplete or duplicated", key)
		require.Equal(t, 1, bytes.Count(firstIndex, []byte(key+".txt")), "%s preview links are incomplete or duplicated", key)
	}
}
