// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package canonicaljson_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
)

func TestCanonicalizeAgreement(t *testing.T) {
	t.Parallel()
	document, err := os.ReadFile("testdata/agreement.json")
	require.NoError(t, err)
	var fixtures []struct {
		Name      string `json:"name"`
		Input     []byte `json:"input_base64"`
		Canonical []byte `json:"canonical_base64"`
		SHA256    string `json:"sha256"`
		Reject    bool   `json:"reject"`
	}
	err = json.Unmarshal(document, &fixtures)
	require.NoError(t, err)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			canonical, err := canonicaljson.Canonicalize(fixture.Input, 256<<10)
			if fixture.Reject {
				require.Error(t, err, "invalid agreement input was accepted")
				require.Nil(t, canonical, "invalid agreement input was accepted")
				return
			}
			require.NoError(t, err)
			require.True(t, bytes.Equal(canonical, fixture.Canonical), "canonical bytes")
			digest := sha256.Sum256(canonical)
			require.Equal(t, fixture.SHA256, ("sha256:" + hex.EncodeToString(digest[:])), "digest")
			second, err := canonicaljson.Canonicalize(canonical, len(canonical))
			require.NoError(t, err, "canonicalization is not idempotent")
			require.True(t, bytes.Equal(second, canonical), "canonicalization is not idempotent")
		})
	}
}

func TestCanonicalizeBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		limit int
		valid bool
	}{
		{name: "exact bytes", input: `{"a":1}`, limit: 7, valid: true},
		{name: "input byte overflow", input: ` {"a":1}`, limit: 7},
		{name: "zero limit", input: `0`},
		{name: "negative limit", input: `0`, limit: -1},
		{name: "maximum depth", input: strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64), limit: 1024, valid: true},
		{name: "depth overflow", input: strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), limit: 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := canonicaljson.Canonicalize([]byte(test.input), test.limit)
			if test.valid {
				require.NoError(t, err)
			}
			if !test.valid {
				require.ErrorIs(t, err, canonicaljson.ErrLimit)
			}
		})
	}
}

func TestCanonicalizeErrorsDoNotExposeInput(t *testing.T) {
	t.Parallel()
	_, err := canonicaljson.Canonicalize([]byte(`{"private_exam_answer":1,"private_exam_answer":2}`), 1024)
	require.ErrorIs(t, err, canonicaljson.ErrInvalid)
	require.NotContains(t, err.Error(), "private_exam_answer")
}

func FuzzCanonicalize(f *testing.F) {
	for _, seed := range []string{`{}`, `[1,-0,null]`, `{"text":"\ud83d\ude00"}`, `{"a":1,"a":2}`, `"<>&  "`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		canonical, err := canonicaljson.Canonicalize(input, 256<<10)
		if err != nil {
			require.Nil(t, canonical, "failed canonicalization returned bytes")
			return
		}
		require.True(t, json.Valid(canonical), "canonical output is not JSON")
		second, err := canonicaljson.Canonicalize(canonical, 256<<10)
		require.NoError(t, err, "canonicalization is not idempotent")
		require.True(t, bytes.Equal(second, canonical), "canonicalization is not idempotent")
	})
}
