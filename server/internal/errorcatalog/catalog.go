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
	// Status is the HTTP status presented for the code. Source states it only
	// for a documented exception; Parse otherwise derives it from the
	// condition.
	Status int `yaml:"status,omitempty"`
	// Reason documents why an exception's status differs from the status its
	// condition implies.
	Reason string `yaml:"reason,omitempty"`
	// Description states, for API consumers, the condition the code reports.
	Description string `yaml:"description"`
	// Transport marks a kernel code the HTTP or WebSocket transport may mint.
	// Every other code is minted only by the application.
	Transport bool `yaml:"transport,omitempty"`
	// WebSocketFallback marks an application code the WebSocket transport
	// presents itself: the fallback for a failure that carries no application
	// error, or a concealing re-mapping. The HTTP transport never mints it.
	WebSocketFallback bool `yaml:"websocket_fallback,omitempty"`
	// Problem marks a routing or health code the HTTP kernel writes directly
	// as Problem Details rather than through an application error.
	Problem bool `yaml:"problem,omitempty"`
}

// Catalog is the compiled public error code catalog in code order.
type Catalog struct {
	Entries []Entry
}

type document struct {
	Conditions    map[string]int `yaml:"conditions"`
	DefaultStatus int            `yaml:"default_status"`
	Codes         []Entry        `yaml:"codes"`
}

var (
	segment          = `[a-z][a-z0-9]*(?:_[a-z0-9]+)*`
	segmentGrammar   = regexp.MustCompile(`^` + segment + `$`)
	codeGrammar      = regexp.MustCompile(`^` + segment + `(?:\.` + segment + `)+$`)
	errorStatusRange = func(status int) bool { return status >= 400 && status <= 599 && http.StatusText(status) != "" }
)

// conditionStatuses derives a code's HTTP status from its condition, the last
// code segment. A condition equal to a vocabulary word, or ending in
// _<word>, takes the word's status; the longest matching word wins. Any other
// condition names a resource state that rejects the request.
type conditionStatuses struct {
	words         []string
	statuses      map[string]int
	defaultStatus int
}

func (c conditionStatuses) derive(code string) int {
	condition := code[strings.LastIndex(code, ".")+1:]
	for _, word := range c.words {
		if condition == word || strings.HasSuffix(condition, "_"+word) {
			return c.statuses[word]
		}
	}
	return c.defaultStatus
}

// Parse decodes and validates catalog source and derives every status that
// source does not state as an exception.
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
	vocabulary := conditionStatuses{statuses: parsed.Conditions, defaultStatus: parsed.DefaultStatus}
	for word, status := range parsed.Conditions {
		if !segmentGrammar.MatchString(word) || !errorStatusRange(status) {
			problems = append(problems, fmt.Sprintf("condition %s: %d is not a snake_case condition with an HTTP error status", word, status))
		}
		vocabulary.words = append(vocabulary.words, word)
	}
	sort.Slice(vocabulary.words, func(i, j int) bool {
		left, right := vocabulary.words[i], vocabulary.words[j]
		return len(left) > len(right) || len(left) == len(right) && left < right
	})
	if !errorStatusRange(parsed.DefaultStatus) {
		problems = append(problems, fmt.Sprintf("default_status %d is not an HTTP error status", parsed.DefaultStatus))
	}
	seen := make(map[string]struct{}, len(parsed.Codes))
	for index := range parsed.Codes {
		entry := &parsed.Codes[index]
		if index > 0 && parsed.Codes[index-1].Code >= entry.Code {
			problems = append(problems, fmt.Sprintf("%s: codes must be unique and sorted", entry.Code))
		}
		if _, exists := seen[entry.Code]; exists {
			continue
		}
		seen[entry.Code] = struct{}{}
		problems = append(problems, validateEntry(entry, vocabulary.derive(entry.Code))...)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid error catalog:\n  %s", strings.Join(problems, "\n  "))
	}
	return &Catalog{Entries: parsed.Codes}, nil
}

func validateEntry(entry *Entry, derived int) []string {
	var problems []string
	if !codeGrammar.MatchString(entry.Code) {
		problems = append(problems, fmt.Sprintf("%s: code is not <domain>[.<subdomain>].<condition> with snake_case segments", entry.Code))
	}
	switch {
	case entry.Status == 0 && entry.Reason != "":
		problems = append(problems, fmt.Sprintf("%s: reason documents no status exception", entry.Code))
	case entry.Status == 0:
		entry.Status = derived
	case entry.Status == derived:
		problems = append(problems, fmt.Sprintf("%s: status %d is already implied by the condition", entry.Code, entry.Status))
	case !errorStatusRange(entry.Status):
		problems = append(problems, fmt.Sprintf("%s: status %d is not an HTTP error status", entry.Code, entry.Status))
	case strings.TrimSpace(entry.Reason) == "":
		problems = append(problems, fmt.Sprintf("%s: status %d differs from the condition's %d without a reason", entry.Code, entry.Status, derived))
	}
	if strings.TrimSpace(entry.Description) == "" {
		problems = append(problems, fmt.Sprintf("%s: description is required", entry.Code))
	}
	if entry.Problem && entry.Transport {
		problems = append(problems, fmt.Sprintf("%s: a kernel problem code is not also an application error", entry.Code))
	}
	if entry.WebSocketFallback && (entry.Transport || entry.Problem) {
		problems = append(problems, fmt.Sprintf("%s: a WebSocket fallback is an application code, not a kernel code", entry.Code))
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
	// WebSocket reports whether the minting package is the WebSocket
	// transport.
	WebSocket bool
}

// Verify compares the catalog with the codes minted by production source. It
// reports every minted code missing from the catalog, every transport mint of
// an application-only code (the WebSocket transport may mint the codes marked
// as its fallbacks), and every application error code no source mints.
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
		case mint.Transport && !entry.Transport && !(mint.WebSocket && entry.WebSocketFallback):
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
