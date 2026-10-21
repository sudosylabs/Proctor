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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
  - code: widget.lease_expired
    description: "Expired widget lease."
    websocket_fallback: true
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
	require.NoError(t, err)
	for code, want := range map[string]int{
		"request.invalid":          400,
		"server.internal":          500,
		"widget.archived":          409,
		"widget.not_found":         404,
		"widget.revision_conflict": 409,
		"widget.token_invalid":     401,
	} {
		entry, ok := catalog.Lookup(code)
		assert.True(t, ok, "Lookup(%s)", code)
		assert.Equal(t, want, entry.Status, "Lookup(%s) status", code)
	}
	_, ok := catalog.Lookup("widget.missing")
	require.False(t, ok, "Lookup found an unlisted code")
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
		"transport fallback":       "codes:\n  - {code: request.invalid, description: x, transport: true, websocket_fallback: true}\n",
		"empty":                    "codes: []\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := errorcatalog.Parse([]byte(vocabulary + source))
			require.Error(t, err, "Parse accepted %s catalog", name)
		})
	}
	_, err := errorcatalog.Parse([]byte("conditions:\n  Invalid: 400\ndefault_status: 409\ncodes:\n  - {code: a.b, description: x}\n"))
	require.Error(t, err, "Parse accepted a non-snake_case condition")
	_, err = errorcatalog.Parse([]byte("conditions:\n  invalid: 400\ndefault_status: 200\ncodes:\n  - {code: a.b, description: x}\n"))
	require.Error(t, err, "Parse accepted a success default status")
}

func TestVerifyReportsUnmappedTransportAndDeadCodes(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	require.NoError(t, err)
	complete := []errorcatalog.Mint{
		{Code: "request.invalid", Position: "httpapi/a.go:1", Transport: true},
		{Code: "widget.archived", Position: "app/a.go:1"},
		{Code: "widget.lease_expired", Position: "app/a.go:5"},
		{Code: "widget.lease_expired", Position: "websocket/a.go:1", Transport: true, WebSocket: true},
		{Code: "widget.not_found", Position: "app/a.go:2"},
		{Code: "widget.revision_conflict", Position: "app/a.go:3"},
		{Code: "widget.token_invalid", Position: "app/a.go:4"},
	}
	err = catalog.Verify(complete)
	require.NoError(t, err)
	err = catalog.Verify(append(complete,
		errorcatalog.Mint{Code: "widget.not_found", Position: "httpapi/b.go:2", Transport: true},
		errorcatalog.Mint{Code: "widget.unlisted", Position: "app/c.go:3"},
		errorcatalog.Mint{Code: "server.internal", Position: "app/d.go:4"},
		errorcatalog.Mint{Code: "widget.lease_expired", Position: "httpapi/e.go:5", Transport: true},
		errorcatalog.Mint{Code: "widget.archived", Position: "websocket/f.go:6", Transport: true, WebSocket: true},
	))
	require.Error(t, err, "Verify accepted disagreeing source")
	for _, want := range []string{
		"httpapi/b.go:2: transport mints application code widget.not_found",
		"app/c.go:3: widget.unlisted mints a code missing from the catalog",
		"app/d.go:4: server.internal is a kernel problem code",
		"httpapi/e.go:5: transport mints application code widget.lease_expired",
		"websocket/f.go:6: transport mints application code widget.archived",
	} {
		assert.Contains(t, err.Error(), want, "Verify error lacks %q:\n%v", want, err)
	}
	err = catalog.Verify(complete[:6])
	require.Error(t, err)
	require.Contains(t, err.Error(), "widget.token_invalid: catalog code is never minted")
}

func TestRenderHTTPMappingIsDeterministicFormattedGo(t *testing.T) {
	t.Parallel()

	catalog, err := errorcatalog.Parse([]byte(validCatalog))
	require.NoError(t, err)
	rendered := catalog.RenderHTTPMapping()
	formatted, err := format.Source(rendered)
	require.NoError(t, err)
	require.True(t, bytes.Equal(formatted, rendered))
	require.True(t, bytes.Equal(rendered, catalog.RenderHTTPMapping()), "rendering is not deterministic")
	for _, want := range []string{
		`"request.invalid":          {status: http.StatusBadRequest}`,
		`"widget.token_invalid":     {status: http.StatusUnauthorized}`,
		`"server.internal": http.StatusInternalServerError`,
		"DO NOT EDIT",
	} {
		assert.True(t, bytes.Contains(rendered, []byte(want)), "rendered mapping lacks %q:\n%s", want, rendered)
	}
	assert.False(t, bytes.Contains(rendered, []byte(`"server.internal":          {status`)), "kernel problem code rendered as an application error mapping")
}
