// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package errorcatalog compiles the human-authored public error code catalog,
// the single source of truth for every code Proctor can present with its HTTP
// status and meaning. It validates the code grammar and the status of each
// code, renders the HTTP transport's generated mapping, and compares the
// catalog with the codes the server mints.
//
// The package performs no process filesystem or toolchain access; callers
// supply catalog bytes and minted-code inventories.
package errorcatalog

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is one public error code.
type Entry struct {
	// Code is the stable machine-readable code.
	Code string `yaml:"code"`
	// Status is the HTTP status presented for the code.
	Status int `yaml:"status"`
	// Description states, for API consumers, the condition the code reports.
	Description string `yaml:"description"`
	// Transport marks a kernel code the HTTP or WebSocket transport may mint.
	// Every other code is minted only by the application.
	Transport bool `yaml:"transport,omitempty"`
	// Problem marks a routing or health code the HTTP kernel writes directly
	// as Problem Details rather than through an application error.
	Problem bool `yaml:"problem,omitempty"`
}

// Catalog is the compiled public error code catalog in code order.
type Catalog struct {
	Entries []Entry
}

type document struct {
	Codes []Entry `yaml:"codes"`
}

var (
	segment     = `[a-z][a-z0-9]*(?:_[a-z0-9]+)*`
	codeGrammar = regexp.MustCompile(`^` + segment + `(?:\.` + segment + `)+$`)
	problemCode = regexp.MustCompile(`^` + segment + `$`)
)

// Parse decodes and validates catalog source.
func Parse(source []byte) (*Catalog, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	var parsed document
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode error catalog: %w", err)
	}
	if len(parsed.Codes) == 0 {
		return nil, errors.New("error catalog declares no codes")
	}
	var problems []string
	seen := make(map[string]struct{}, len(parsed.Codes))
	for index, entry := range parsed.Codes {
		if index > 0 && parsed.Codes[index-1].Code >= entry.Code {
			problems = append(problems, fmt.Sprintf("%s: codes must be unique and sorted", entry.Code))
		}
		if _, exists := seen[entry.Code]; exists {
			continue
		}
		seen[entry.Code] = struct{}{}
		problems = append(problems, validateEntry(entry)...)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid error catalog:\n  %s", strings.Join(problems, "\n  "))
	}
	return &Catalog{Entries: parsed.Codes}, nil
}

func validateEntry(entry Entry) []string {
	var problems []string
	switch {
	case entry.Problem && !problemCode.MatchString(entry.Code) && !codeGrammar.MatchString(entry.Code):
		problems = append(problems, fmt.Sprintf("%s: code is not a lowercase snake_case name", entry.Code))
	case !entry.Problem && !codeGrammar.MatchString(entry.Code):
		problems = append(problems, fmt.Sprintf("%s: code is not <domain>[.<subdomain>].<condition> with snake_case segments", entry.Code))
	}
	if entry.Status < 400 || entry.Status > 599 || http.StatusText(entry.Status) == "" {
		problems = append(problems, fmt.Sprintf("%s: status %d is not an HTTP error status", entry.Code, entry.Status))
	}
	if strings.TrimSpace(entry.Description) == "" {
		problems = append(problems, fmt.Sprintf("%s: description is required", entry.Code))
	}
	if entry.Problem && entry.Transport {
		problems = append(problems, fmt.Sprintf("%s: a kernel problem code is not also an application error", entry.Code))
	}
	return problems
}

// Lookup returns the entry for code.
func (c *Catalog) Lookup(code string) (Entry, bool) {
	index := sort.Search(len(c.Entries), func(i int) bool { return c.Entries[i].Code >= code })
	if index < len(c.Entries) && c.Entries[index].Code == code {
		return c.Entries[index], true
	}
	return Entry{}, false
}

// Mint is one constant application error code found in production source.
type Mint struct {
	Code string
	// Package is the import path of the minting package.
	Package string
	// Position is the source position, for diagnostics.
	Position string
	// Transport reports whether the minting package is an HTTP or WebSocket
	// transport.
	Transport bool
}

// Verify compares the catalog with the codes minted by production source. It
// reports every minted code missing from the catalog, every transport mint of
// an application-only code, and every application error code no source
// mints.
func (c *Catalog) Verify(mints []Mint) error {
	var problems []string
	minted := make(map[string]struct{}, len(mints))
	for _, mint := range mints {
		minted[mint.Code] = struct{}{}
		entry, ok := c.Lookup(mint.Code)
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %s mints a code missing from the catalog", mint.Position, mint.Code))
		case entry.Problem:
			problems = append(problems, fmt.Sprintf("%s: %s is a kernel problem code, not an application error", mint.Position, mint.Code))
		case mint.Transport && !entry.Transport:
			problems = append(problems, fmt.Sprintf("%s: transport mints application code %s", mint.Position, mint.Code))
		}
	}
	for _, entry := range c.Entries {
		if _, ok := minted[entry.Code]; !ok && !entry.Problem {
			problems = append(problems, fmt.Sprintf("%s: catalog code is never minted", entry.Code))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("error catalog disagrees with source:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// RenderHTTPMapping renders the HTTP transport's generated Go mapping.
func (c *Catalog) RenderHTTPMapping() []byte {
	formatted, err := format.Source(c.renderHTTPMapping())
	if err != nil {
		panic("errorcatalog: rendered mapping is not valid Go: " + err.Error())
	}
	return formatted
}

func (c *Catalog) renderHTTPMapping() []byte {
	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("package httpapi\n\nimport \"net/http\"\n\n")
	out.WriteString("// applicationErrorMappings is the exhaustive HTTP status table for public\n")
	out.WriteString("// application and kernel error codes, generated from error_catalog.yaml.\n")
	out.WriteString("var applicationErrorMappings = map[string]applicationErrorMapping{\n")
	for _, entry := range c.Entries {
		if !entry.Problem {
			fmt.Fprintf(&out, "\t%q: {status: %s},\n", entry.Code, statusConstant(entry.Status))
		}
	}
	out.WriteString("}\n\n")
	out.WriteString("// transportProblemCodes are routing and health codes the kernel writes\n")
	out.WriteString("// directly as Problem Details, generated from error_catalog.yaml.\n")
	out.WriteString("var transportProblemCodes = map[string]int{\n")
	for _, entry := range c.Entries {
		if entry.Problem {
			fmt.Fprintf(&out, "\t%q: %s,\n", entry.Code, statusConstant(entry.Status))
		}
	}
	out.WriteString("}\n")
	return out.Bytes()
}

const header = `// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Code generated by "go run ./cmd/ptool errors generate" from error_catalog.yaml; DO NOT EDIT.

`

var statusConstants = map[int]string{
	http.StatusBadRequest:            "http.StatusBadRequest",
	http.StatusUnauthorized:          "http.StatusUnauthorized",
	http.StatusForbidden:             "http.StatusForbidden",
	http.StatusNotFound:              "http.StatusNotFound",
	http.StatusMethodNotAllowed:      "http.StatusMethodNotAllowed",
	http.StatusConflict:              "http.StatusConflict",
	http.StatusGone:                  "http.StatusGone",
	http.StatusRequestEntityTooLarge: "http.StatusRequestEntityTooLarge",
	http.StatusTooManyRequests:       "http.StatusTooManyRequests",
	http.StatusInternalServerError:   "http.StatusInternalServerError",
	http.StatusNotImplemented:        "http.StatusNotImplemented",
	http.StatusBadGateway:            "http.StatusBadGateway",
	http.StatusServiceUnavailable:    "http.StatusServiceUnavailable",
}

func statusConstant(status int) string {
	if name, ok := statusConstants[status]; ok {
		return name
	}
	return fmt.Sprint(status)
}
