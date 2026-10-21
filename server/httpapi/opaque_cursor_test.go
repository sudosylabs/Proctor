// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type opaqueCursorFixture struct {
	Version int    `json:"version,omitempty"`
	Key     string `json:"key"`
}

func opaqueCursorFixtureSpec() opaqueCursorSpec[opaqueCursorFixture] {
	return opaqueCursorSpec[opaqueCursorFixture]{
		label: "fixture", maximumEncodedLength: 256, currentVersion: 1,
		members:        []string{"version", "key"},
		version:        func(cursor opaqueCursorFixture) int { return cursor.Version },
		setVersion:     func(cursor *opaqueCursorFixture, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate: func(cursor opaqueCursorFixture) error {
			if cursor.Key == "" {
				return errors.New("key is required")
			}
			return nil
		},
	}
}

func TestOpaqueCursorCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	want := opaqueCursorFixture{Key: "keyset"}
	first, err := encodeOpaqueCursor(want, spec)
	require.NoError(t, err)
	second, err := encodeOpaqueCursor(want, spec)
	require.NoError(t, err)
	require.Equal(t, second, first, "non-deterministic or non-raw-URL encoding")
	require.False(t, strings.ContainsAny(first, "+/="), "non-deterministic or non-raw-URL encoding")
	decoded, err := decodeOpaqueCursor(first, spec)
	require.NoError(t, err, "round trip")
	require.Equal(t, opaqueCursorFixture{Version: 1, Key: want.Key}, decoded, "round trip")
	document, err := base64.RawURLEncoding.Strict().DecodeString(first)
	require.NoError(t, err, "encoded document")
	require.Contains(t, string(document), `"version":1`, "encoded document")
}

func TestOpaqueCursorRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	encodeDocument := func(document string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(document))
	}
	valid := `{"version":1,"key":"keyset"}`
	tests := map[string]string{
		"empty":              "",
		"over bound":         strings.Repeat("a", spec.maximumEncodedLength+1),
		"at bound invalid":   strings.Repeat("a", spec.maximumEncodedLength),
		"malformed alphabet": "%%%",
		"padded base64":      encodeDocument(valid) + "=",
		"trailing bits":      "Zh",
		"invalid UTF-8":      base64.RawURLEncoding.EncodeToString([]byte{0xff}),
		"array":              encodeDocument(`[]`),
		"scalar":             encodeDocument(`1`),
		"null":               encodeDocument(`null`),
		"duplicate version":  encodeDocument(`{"version":1,"version":1,"key":"keyset"}`),
		"case alias version": encodeDocument(`{"version":2,"Version":1,"key":"keyset"}`),
		"case alias key":     encodeDocument(`{"version":1,"Key":"keyset"}`),
		"duplicate key":      encodeDocument(`{"version":1,"key":"a","key":"b"}`),
		"unknown member":     encodeDocument(`{"version":1,"key":"keyset","unknown":true}`),
		"missing key":        encodeDocument(`{"version":1}`),
		"concatenated":       encodeDocument(valid + `{}`),
		"trailing token":     encodeDocument(valid + ` true`),
		"trailing garbage":   encodeDocument(valid + ` garbage`),
		"unsupported":        encodeDocument(`{"version":2,"key":"keyset"}`),
	}
	for name, raw := range tests {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeOpaqueCursor(raw, spec)
			require.Error(t, err, "decode(%q) error", raw)
			require.True(t, strings.HasPrefix(err.Error(), "invalid fixture cursor"), "decode(%q) error", raw)
		})
	}
}

func TestOpaqueCursorRejectsEncoderAndSpecFailures(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	_, err := encodeOpaqueCursor(opaqueCursorFixture{}, spec)
	require.Error(t, err, "resource-invalid cursor encoded")

	type marshalFailureCursor struct {
		Version int `json:"version"`
		Value   any `json:"value"`
	}
	marshalSpec := opaqueCursorSpec[marshalFailureCursor]{
		label: "marshal fixture", maximumEncodedLength: 256, currentVersion: 1,
		members:        []string{"version", "value"},
		version:        func(cursor marshalFailureCursor) int { return cursor.Version },
		setVersion:     func(cursor *marshalFailureCursor, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate:       func(marshalFailureCursor) error { return nil },
	}
	_, err = encodeOpaqueCursor(marshalFailureCursor{Value: make(chan struct{})}, marshalSpec)
	require.Error(t, err, "JSON marshal failure was ignored")

	invalidSpec := spec
	invalidSpec.validate = nil
	_, err = encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, invalidSpec)
	require.Error(t, err, "invalid encoder specification accepted")
	_, err = decodeOpaqueCursor("anything", invalidSpec)
	require.Error(t, err, "invalid decoder specification accepted")
	duplicateMembers := spec
	duplicateMembers.members = []string{"version", "version"}
	_, err = encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, duplicateMembers)
	require.Error(t, err, "duplicate specification members accepted")
}

func FuzzOpaqueCursorDecode(f *testing.F) {
	spec := opaqueCursorFixtureSpec()
	valid, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "seed"}, spec)
	require.NoError(f, err)
	for _, seed := range []string{"", valid, "%%%", "Zh", base64.RawURLEncoding.EncodeToString([]byte(`null`))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = decodeOpaqueCursor(raw, spec)
	})
}

func TestOpaqueCursorDocumentIsJSON(t *testing.T) {
	// Keep json imported in this focused test so the fixture also proves the
	// emitted document is a normal object consumable by standard decoders.
	raw, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, opaqueCursorFixtureSpec())
	require.NoError(t, err)
	document, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err, "document")
	require.True(t, json.Valid(document), "document")
}
