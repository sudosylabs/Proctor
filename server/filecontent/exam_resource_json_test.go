// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestExamResourceJSONGrammar(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"null", "null", true},
		{"true", "true", true},
		{"false", "false", true},
		{"zero", "0", true},
		{"negative zero", "-0", true},
		{"fraction", "-12.345", true},
		{"exponent", "12.345e-000001", true},
		{"unbounded number precision", "1E+9999999999999999999999999999999999999999", true},
		{"whitespace", " \t\n\r[null]\t\r\n ", true},
		{"empty string", `""`, true},
		{"empty array", "[]", true},
		{"empty object", "{}", true},
		{"mixed containers", `{"a":[true,false,null,-1,{},[]],"b":{"c":"d"}}`, true},
		{"duplicate object names", `{"a":1,"a":2}`, true},
		{"escaped duplicate names", `{"a":1,"\u0061":2}`, true},
		{"unicode", `{"é":"日本語😀"}`, true},
		{"escapes", `"\"\\\/\b\f\n\r\t\u0041"`, true},
		{"escaped NUL", `"\u0000"`, true},
		{"escaped surrogate pair", `"\ud800\udc00"`, true},
		{"unpaired high surrogate", `"\ud800"`, true},
		{"unpaired low surrogate", `"\udc00"`, true},
		{"literal replacement character", `"�"`, true},
		{"empty", "", false},
		{"only whitespace", " \t\n", false},
		{"multiple scalars", "null true", false},
		{"multiple containers", "{}[]", false},
		{"trailing text", "truefalse", false},
		{"missing array close", "[0", false},
		{"missing object close", `{"a":0`, false},
		{"mismatched close", "[}", false},
		{"unexpected close", "]", false},
		{"missing key", "{:0}", false},
		{"non-string key", "{0:0}", false},
		{"missing colon", `{"a" 0}`, false},
		{"missing value", `{"a":}`, false},
		{"missing comma", "[0 1]", false},
		{"array trailing comma", "[0,]", false},
		{"object trailing comma", `{"a":0,}`, false},
		{"double comma", "[0,,1]", false},
		{"leading comma", "[,0]", false},
		{"leading plus", "+1", false},
		{"leading zero", "01", false},
		{"negative leading zero", "-01", false},
		{"incomplete sign", "-", false},
		{"missing integer", ".1", false},
		{"missing fraction", "1.", false},
		{"missing exponent", "1e", false},
		{"missing signed exponent", "1e+", false},
		{"double exponent sign", "1e--1", false},
		{"hexadecimal", "0x1", false},
		{"infinity", "Infinity", false},
		{"NaN", "NaN", false},
		{"capitalized literal", "True", false},
		{"incomplete literal", "nul", false},
		{"missing string close", `"a`, false},
		{"unfinished escape", `"a\`, false},
		{"unknown escape", `"\x20"`, false},
		{"unfinished unicode escape", `"\u010"`, false},
		{"invalid unicode escape", `"\u010g"`, false},
		{"raw newline", "\"a\nb\"", false},
		{"raw NUL", "\"a\x00b\"", false},
		{"invalid UTF-8 string", "\"\xff\"", false},
		{"invalid UTF-8 key", "{\"\xff\":0}", false},
		{"UTF-8 BOM", "\ufeff{}", false},
		{"non-JSON whitespace", "\u00a0{}", false},
		{"comment", "/* notes */{}", false},
		{"single quotes", "{'a':0}", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateExamResourceJSON(strings.NewReader(test.body))
			if (err == nil) != test.valid {
				t.Fatalf("validation error=%v, want valid=%v", err, test.valid)
			}
			if !test.valid && !errors.Is(err, ErrInvalidExamResourceContent) {
				t.Fatalf("unsafe validation error: %v", err)
			}
		})
	}
}

func TestExamResourceJSONPreservesDecoderNestingLimit(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{9999, 10000, 10001} {
		for _, container := range []struct{ open, close string }{{"[", "]"}, {`{"a":`, "}"}} {
			body := strings.Repeat(container.open, depth) + "0" + strings.Repeat(container.close, depth)
			got := validateExamResourceJSON(strings.NewReader(body)) == nil
			if want := referenceExamResourceJSON(body); got != want || got != (depth <= 10000) {
				t.Fatalf("depth=%d open=%q: accepted=%v, decoder=%v", depth, container.open, got, want)
			}
		}
	}
}

func TestExamResourceJSONAcrossReadBoundaries(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`"` + strings.Repeat("a", examResourceCopyBuffer-3) + `\u00ab"`,
		`"` + strings.Repeat("a", examResourceCopyBuffer-3) + `\""`,
		`"` + strings.Repeat("a", examResourceCopyBuffer-2) + `é"`,
		`[` + strings.Repeat("0,", examResourceCopyBuffer-3) + `1e+99999999]`,
		strings.Repeat(" ", examResourceCopyBuffer-2) + "null" + strings.Repeat(" ", examResourceCopyBuffer*2),
		`"` + strings.Repeat("a", examResourceCopyBuffer-3) + `\u00z1"`,
		`"` + strings.Repeat("😀", examResourceCopyBuffer/4) + `"`,
	} {
		for _, chunkSize := range []int{1, 2, 3, 7, examResourceCopyBuffer - 1, examResourceCopyBuffer} {
			reader := &chunkedJSONReader{ReadSeeker: strings.NewReader(body), chunkSize: chunkSize}
			if got, want := validateExamResourceJSON(reader) == nil, referenceExamResourceJSON(body); got != want {
				t.Fatalf("chunk=%d: accepted=%v, decoder=%v", chunkSize, got, want)
			}
		}
	}
}

func TestExamResourceJSONRejectsTransientNumberReadErrors(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"0", "1", "12", "1.25", "1e2", "1e+2", "1e-2", "-1.25e+2"} {
		for _, withFinalBytes := range []bool{false, true} {
			reader := &failedJSONReader{Reader: strings.NewReader(body), withFinalBytes: withFinalBytes}
			if err := validateExamResourceJSONSyntax(reader); !errors.Is(err, ErrInvalidExamResourceContent) {
				t.Fatalf("number=%q with final bytes=%v: error=%v, want safe content failure", body, withFinalBytes, err)
			}
		}
	}
}

func TestExamResourceJSONKeepsConstantAllocations(t *testing.T) {
	allocations := func(members int) float64 {
		body := "[" + strings.Repeat(`{"a":1},`, members) + "null]"
		return testing.AllocsPerRun(3, func() {
			if err := validateExamResourceJSON(strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := allocations(8), allocations(4096)
	if large > small+1 {
		t.Fatalf("JSON allocations grew with member count: small=%g large=%g", small, large)
	}
}

func FuzzExamResourceJSONGrammar(f *testing.F) {
	for _, seed := range []string{
		`{"a":[1,true,null,"é","\uD800"],"a":2}`,
		`[[[[[1]]]]]`, `"\u0000"`, "1e999999", `"\"\\\/\b\f\n\r\t"`, `{"a":true`, "01", "\"\xff\"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		// The reference intentionally constructs a tree; bound each comparison
		// so fuzzing malformed syntax cannot become a memory stress workload.
		if len(body) > 64<<10 {
			t.Skip()
		}
		if got, want := validateExamResourceJSON(strings.NewReader(body)) == nil, referenceExamResourceJSON(body); got != want {
			t.Fatalf("accepted=%v, decoder=%v for %q", got, want, body)
		}
	})
}

// The prior UseNumber decoder is the compatibility oracle. JSON Resources are
// syntax-checked authored bytes; duplicate keys and large numbers are valid.
func referenceExamResourceJSON(body string) bool {
	if err := validateExamResourceUTF8(strings.NewReader(body)); err != nil {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	return errors.Is(decoder.Decode(&value), io.EOF)
}

type chunkedJSONReader struct {
	io.ReadSeeker
	chunkSize int
}

func (r *chunkedJSONReader) Read(body []byte) (int, error) {
	return r.ReadSeeker.Read(body[:min(len(body), r.chunkSize)])
}

type failedJSONReader struct {
	*strings.Reader
	withFinalBytes bool
	failed         bool
}

func (r *failedJSONReader) Read(body []byte) (int, error) {
	n, err := r.Reader.Read(body)
	if !r.failed && (err == io.EOF || r.withFinalBytes && r.Len() == 0) {
		r.failed = true
		return n, errors.New("private underlying read failure")
	}
	return n, err
}
