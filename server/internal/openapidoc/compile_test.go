// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package openapidoc

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

const validBase = `openapi: 3.1.0
info:
  title: Proctor API
  version: 1.0.0
tags:
  - name: System
    description: Runtime health and service information.
`

const validFragment = `paths:
  /health/live:
    get:
      operationId: getHealthLive
      summary: Check process liveness
      tags: [System]
      security: []
      x-proctor-auth: public
      x-proctor-error-codes: []
      x-proctor-idempotency: none
      responses:
        "200":
          description: The process is live.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/HealthResponse"
components:
  schemas:
    HealthResponse:
      type: object
      additionalProperties: false
      required: [status]
      properties:
        status:
          type: string
`

func TestCompileDiscoversMergesAndValidatesFragments(t *testing.T) {
	t.Parallel()

	compiled, err := Compile(fstest.MapFS{
		"base.yaml":                         {Data: []byte(validBase)},
		"fragments/system/health.yaml":      {Data: []byte(validFragment)},
		"fragments/system/ignored.markdown": {Data: []byte("not OpenAPI")},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(compiled)
	for _, expected := range []string{`"/health/live"`, `"HealthResponse"`, `"getHealthLive"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("compiled document does not contain %s", expected)
		}
	}
	compiledAgain, err := Compile(fstest.MapFS{
		"base.yaml":                    {Data: []byte(validBase)},
		"fragments/system/health.yaml": {Data: []byte(validFragment)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(compiled) != string(compiledAgain) {
		t.Fatal("compilation is not deterministic")
	}
}

func TestCompileRejectsInvalidSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		files     fstest.MapFS
		wantError string
	}{
		{
			name: "missing fragments",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments": {Mode: 0o755 | fs.ModeDir},
			},
			wantError: "contains no fragments",
		},
		{
			name: "base owns a path",
			files: fstest.MapFS{
				"base.yaml":                    {Data: []byte(validBase + "paths: {}\n")},
				"fragments/system/health.yaml": {Data: []byte(validFragment)},
			},
			wantError: `top-level key "paths" belongs in a fragment`,
		},
		{
			name: "fragment owns metadata",
			files: fstest.MapFS{
				"base.yaml":                    {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte("info: {}\n")},
			},
			wantError: `unsupported top-level key "info"`,
		},
		{
			name: "multiple YAML documents",
			files: fstest.MapFS{
				"base.yaml":                    {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(validFragment + "---\npaths: {}\n")},
			},
			wantError: "multiple YAML documents",
		},
		{
			name: "duplicate path",
			files: fstest.MapFS{
				"base.yaml":                    {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(validFragment)},
				"fragments/system/more.yaml":   {Data: []byte("paths:\n  /health/live: {}\n")},
			},
			wantError: `path "/health/live" is already declared`,
		},
		{
			name: "duplicate component",
			files: fstest.MapFS{
				"base.yaml":                    {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(validFragment)},
				"fragments/system/more.yaml":   {Data: []byte("components:\n  schemas:\n    HealthResponse:\n      type: object\n")},
			},
			wantError: "component schemas.HealthResponse is already declared",
		},
		{
			name: "missing operation metadata",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"      summary: Check process liveness\n",
					"",
					1,
				))},
			},
			wantError: "summary is required",
		},
		{
			name: "missing operation ID",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"      operationId: getHealthLive\n",
					"",
					1,
				))},
			},
			wantError: "operationId is required",
		},
		{
			name: "unknown tag",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"      tags: [System]\n",
					"      tags: [Transport internals]\n",
					1,
				))},
			},
			wantError: `tag "Transport internals" is not declared`,
		},
		{
			name: "missing idempotency declaration",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"      x-proctor-idempotency: none\n",
					"",
					1,
				))},
			},
			wantError: "x-proctor-idempotency must be none, optional, or required",
		},
		{
			name: "unresolved component reference",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					`$ref: "#/components/schemas/HealthResponse"`,
					`$ref: "#/components/schemas/Missing"`,
					1,
				))},
			},
			wantError: "Missing",
		},
		{
			name: "undeclared path parameter",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"  /health/live:\n",
					"  /health/{probe}:\n",
					1,
				))},
			},
			wantError: "probe",
		},
		{
			name: "malformed required properties",
			files: fstest.MapFS{
				"base.yaml": {Data: []byte(validBase)},
				"fragments/system/health.yaml": {Data: []byte(strings.Replace(
					validFragment,
					"      required: [status]\n",
					"      required: status\n",
					1,
				))},
			},
			wantError: "required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Compile(test.files)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Compile() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}
