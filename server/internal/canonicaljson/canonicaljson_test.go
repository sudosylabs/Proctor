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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
)

func TestCanonicalizeAgreement(t *testing.T) {
	t.Parallel()
	document, err := os.ReadFile("testdata/agreement.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name      string `json:"name"`
		Input     []byte `json:"input_base64"`
		Canonical []byte `json:"canonical_base64"`
		SHA256    string `json:"sha256"`
		Reject    bool   `json:"reject"`
	}
	if err = json.Unmarshal(document, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			canonical, err := canonicaljson.Canonicalize(fixture.Input, 256<<10)
			if fixture.Reject {
				if err == nil || canonical != nil {
					t.Fatal("invalid agreement input was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, fixture.Canonical) {
				t.Fatalf("canonical bytes = %s; want %s",
					base64.StdEncoding.EncodeToString(canonical), base64.StdEncoding.EncodeToString(fixture.Canonical))
			}
			digest := sha256.Sum256(canonical)
			if got := "sha256:" + hex.EncodeToString(digest[:]); got != fixture.SHA256 {
				t.Fatalf("digest = %s; want %s", got, fixture.SHA256)
			}
			second, err := canonicaljson.Canonicalize(canonical, len(canonical))
			if err != nil || !bytes.Equal(second, canonical) {
				t.Fatal("canonicalization is not idempotent")
			}
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
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && !errors.Is(err, canonicaljson.ErrLimit) {
				t.Fatalf("limit error = %v", err)
			}
		})
	}
}

func TestCanonicalizeErrorsDoNotExposeInput(t *testing.T) {
	t.Parallel()
	_, err := canonicaljson.Canonicalize([]byte(`{"private_exam_answer":1,"private_exam_answer":2}`), 1024)
	if !errors.Is(err, canonicaljson.ErrInvalid) || strings.Contains(err.Error(), "private_exam_answer") {
		t.Fatalf("unsafe validation error: %v", err)
	}
}

func FuzzCanonicalize(f *testing.F) {
	for _, seed := range []string{`{}`, `[1,-0,null]`, `{"text":"\ud83d\ude00"}`, `{"a":1,"a":2}`, `"<>&  "`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		canonical, err := canonicaljson.Canonicalize(input, 256<<10)
		if err != nil {
			if canonical != nil {
				t.Fatal("failed canonicalization returned bytes")
			}
			return
		}
		if !json.Valid(canonical) {
			t.Fatal("canonical output is not JSON")
		}
		second, err := canonicaljson.Canonicalize(canonical, 256<<10)
		if err != nil || !bytes.Equal(second, canonical) {
			t.Fatal("canonicalization is not idempotent")
		}
	})
}
