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

const validCatalog = `codes:
  - code: internal
    status: 500
    description: "Server fault."
    problem: true
  - code: request.invalid
    status: 400
    description: "Invalid request."
    transport: true
  - code: widget.not_found
    status: 404
    description: "Missing widget."
`

func TestParseAcceptsAValidCatalog(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := catalog.Lookup("widget.not_found"); !ok || entry.Status != 404 {
		t.Fatalf("Lookup(widget.not_found) = %#v, %v", entry, ok)
	}
	if _, ok := catalog.Lookup("widget.missing"); ok {
		t.Fatal("Lookup found an unlisted code")
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unsorted":            "codes:\n  - {code: b.invalid, status: 400, description: x}\n  - {code: a.invalid, status: 400, description: x}\n",
		"duplicate":           "codes:\n  - {code: a.invalid, status: 400, description: x}\n  - {code: a.invalid, status: 400, description: x}\n",
		"undotted":            "codes:\n  - {code: invalid, status: 400, description: x}\n",
		"uppercase":           "codes:\n  - {code: Widget.invalid, status: 400, description: x}\n",
		"hyphenated":          "codes:\n  - {code: widget.not-found, status: 404, description: x}\n",
		"success status":      "codes:\n  - {code: widget.invalid, status: 200, description: x}\n",
		"missing description": "codes:\n  - {code: widget.invalid, status: 400}\n",
		"unknown field":       "codes:\n  - {code: widget.invalid, status: 400, description: x, owner: app}\n",
		"problem transport":   "codes:\n  - {code: internal, status: 500, description: x, problem: true, transport: true}\n",
		"empty":               "codes: []\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := errorcatalog.Parse([]byte(source)); err == nil {
				t.Fatalf("Parse accepted %s catalog", name)
			}
		})
	}
}

func TestVerifyReportsUnmappedTransportAndDeadCodes(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if err = catalog.Verify([]errorcatalog.Mint{
		{Code: "request.invalid", Position: "httpapi/a.go:1", Transport: true},
		{Code: "widget.not_found", Position: "app/a.go:1"},
	}); err != nil {
		t.Fatalf("Verify(complete) = %v", err)
	}
	err = catalog.Verify([]errorcatalog.Mint{
		{Code: "request.invalid", Position: "app/a.go:1"},
		{Code: "widget.not_found", Position: "httpapi/b.go:2", Transport: true},
		{Code: "widget.unlisted", Position: "app/c.go:3"},
		{Code: "internal", Position: "app/d.go:4"},
	})
	if err == nil {
		t.Fatal("Verify accepted disagreeing source")
	}
	for _, want := range []string{
		"httpapi/b.go:2: transport mints application code widget.not_found",
		"app/c.go:3: widget.unlisted mints a code missing from the catalog",
		"app/d.go:4: internal is a kernel problem code",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Verify error lacks %q:\n%v", want, err)
		}
	}
	if err := catalog.Verify([]errorcatalog.Mint{{Code: "request.invalid", Position: "app/a.go:1"}}); err == nil ||
		!strings.Contains(err.Error(), "widget.not_found: catalog code is never minted") {
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
		`"request.invalid":  {status: http.StatusBadRequest}`,
		`"widget.not_found": {status: http.StatusNotFound}`,
		`"internal": http.StatusInternalServerError`,
		"DO NOT EDIT",
	} {
		if !bytes.Contains(rendered, []byte(want)) {
			t.Errorf("rendered mapping lacks %q:\n%s", want, rendered)
		}
	}
	if bytes.Contains(rendered, []byte(`"internal":  {status`)) {
		t.Error("kernel problem code rendered as an application error mapping")
	}
}
