// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package errorcatalog_test

import (
	"bytes"
	"go/format"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/internal/errorcatalog"
)

const vocabulary = `conditions:
  invalid: 400
  not_found: 404
  conflict: 409
  rate_limited: 429
  internal: 500
  unavailable: 503
default_status: 409
`

const validCatalog = vocabulary + `codes:
  - code: request.invalid
    description: "Invalid request."
    transport: true
  - code: server.internal
    description: "Server fault."
    problem: true
  - code: widget.archived
    description: "Archived widget."
  - code: widget.not_found
    description: "Missing widget."
  - code: widget.revision_conflict
    description: "Stale widget revision."
  - code: widget.token_invalid
    status: 401
    reason: "A failed credential is an authentication failure."
    description: "Bad widget token."
`

func TestParseDerivesStatusesFromTheCondition(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]int{
		"request.invalid":          400,
		"server.internal":          500,
		"widget.archived":          409,
		"widget.not_found":         404,
		"widget.revision_conflict": 409,
		"widget.token_invalid":     401,
	} {
		if entry, ok := catalog.Lookup(code); !ok || entry.Status != want {
			t.Errorf("Lookup(%s) = %#v, %v; want status %d", code, entry, ok, want)
		}
	}
	if _, ok := catalog.Lookup("widget.missing"); ok {
		t.Fatal("Lookup found an unlisted code")
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unsorted":                 "codes:\n  - {code: b.invalid, description: x}\n  - {code: a.invalid, description: x}\n",
		"duplicate":                "codes:\n  - {code: a.invalid, description: x}\n  - {code: a.invalid, description: x}\n",
		"undotted":                 "codes:\n  - {code: invalid, description: x}\n",
		"uppercase":                "codes:\n  - {code: Widget.invalid, description: x}\n",
		"hyphenated":               "codes:\n  - {code: widget.not-found, description: x}\n",
		"redundant status":         "codes:\n  - {code: widget.invalid, status: 400, description: x}\n",
		"exception without reason": "codes:\n  - {code: widget.invalid, status: 401, description: x}\n",
		"reason without status":    "codes:\n  - {code: widget.invalid, reason: because, description: x}\n",
		"success status":           "codes:\n  - {code: widget.invalid, status: 200, reason: because, description: x}\n",
		"missing description":      "codes:\n  - {code: widget.invalid}\n",
		"unknown field":            "codes:\n  - {code: widget.invalid, description: x, owner: app}\n",
		"problem transport":        "codes:\n  - {code: server.internal, description: x, problem: true, transport: true}\n",
		"empty":                    "codes: []\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := errorcatalog.Parse([]byte(vocabulary + source)); err == nil {
				t.Fatalf("Parse accepted %s catalog", name)
			}
		})
	}
	if _, err := errorcatalog.Parse([]byte("conditions:\n  Invalid: 400\ndefault_status: 409\ncodes:\n  - {code: a.b, description: x}\n")); err == nil {
		t.Fatal("Parse accepted a non-snake_case condition")
	}
	if _, err := errorcatalog.Parse([]byte("conditions:\n  invalid: 400\ndefault_status: 200\ncodes:\n  - {code: a.b, description: x}\n")); err == nil {
		t.Fatal("Parse accepted a success default status")
	}
}

func TestVerifyReportsUnmappedTransportAndDeadCodes(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	complete := []errorcatalog.Mint{
		{Code: "request.invalid", Position: "httpapi/a.go:1", Transport: true},
		{Code: "widget.archived", Position: "app/a.go:1"},
		{Code: "widget.not_found", Position: "app/a.go:2"},
		{Code: "widget.revision_conflict", Position: "app/a.go:3"},
		{Code: "widget.token_invalid", Position: "app/a.go:4"},
	}
	if err = catalog.Verify(complete); err != nil {
		t.Fatalf("Verify(complete) = %v", err)
	}
	err = catalog.Verify(append(complete,
		errorcatalog.Mint{Code: "widget.not_found", Position: "httpapi/b.go:2", Transport: true},
		errorcatalog.Mint{Code: "widget.unlisted", Position: "app/c.go:3"},
		errorcatalog.Mint{Code: "server.internal", Position: "app/d.go:4"},
	))
	if err == nil {
		t.Fatal("Verify accepted disagreeing source")
	}
	for _, want := range []string{
		"httpapi/b.go:2: transport mints application code widget.not_found",
		"app/c.go:3: widget.unlisted mints a code missing from the catalog",
		"app/d.go:4: server.internal is a kernel problem code",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Verify error lacks %q:\n%v", want, err)
		}
	}
	if err := catalog.Verify(complete[:4]); err == nil ||
		!strings.Contains(err.Error(), "widget.token_invalid: catalog code is never minted") {
		t.Fatalf("Verify(dead) = %v", err)
	}
}

func TestRenderHTTPMappingIsDeterministicFormattedGo(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	rendered := catalog.RenderHTTPMapping()
	formatted, err := format.Source(rendered)
	if err != nil || !bytes.Equal(formatted, rendered) {
		t.Fatalf("rendered mapping is not gofmt output: %v", err)
	}
	if !bytes.Equal(rendered, catalog.RenderHTTPMapping()) {
		t.Fatal("rendering is not deterministic")
	}
	for _, want := range []string{
		`"request.invalid":          {status: http.StatusBadRequest}`,
		`"widget.token_invalid":     {status: http.StatusUnauthorized}`,
		`"server.internal": http.StatusInternalServerError`,
		"DO NOT EDIT",
	} {
		if !bytes.Contains(rendered, []byte(want)) {
			t.Errorf("rendered mapping lacks %q:\n%s", want, rendered)
		}
	}
	if bytes.Contains(rendered, []byte(`"server.internal":          {status`)) {
		t.Error("kernel problem code rendered as an application error mapping")
	}
}
