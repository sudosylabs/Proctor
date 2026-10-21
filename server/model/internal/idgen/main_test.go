// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderIsDeterministicAndComplete(t *testing.T) {
	t.Parallel()
	first, err := render(entityIDs)
	require.NoError(t, err)
	second, err := render(entityIDs)
	require.NoError(t, err)
	require.True(t, bytes.Equal(first, second), "render produced different output for the same catalog")
	for _, spec := range entityIDs {
		for _, declaration := range []string{
			"func New" + spec.typeName + "()",
			"func Parse" + spec.typeName + "(",
			"func (id " + spec.typeName + ") IsZero()",
			"func (id " + spec.typeName + ") IsValid()",
			"func (id " + spec.typeName + ") String()",
			"func (id " + spec.typeName + ") MarshalText()",
			"func (id *" + spec.typeName + ") UnmarshalText(",
			"func (id " + spec.typeName + ") MarshalJSON()",
			"func (id *" + spec.typeName + ") UnmarshalJSON(",
		} {
			require.Equal(t, 1, strings.Count(string(first), declaration), "generated declaration %q count", declaration)
		}
	}
}

func TestValidateCatalogRejectsAmbiguity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		specs []idSpec
	}{
		{name: "empty"},
		{name: "invalid type", specs: []idSpec{{typeName: "userID", fieldName: "user_id", constructorSubject: "user"}}},
		{name: "invalid field", specs: []idSpec{{typeName: "UserID", fieldName: "UserID", constructorSubject: "user"}}},
		{name: "empty subject", specs: []idSpec{{typeName: "UserID", fieldName: "user_id"}}},
		{name: "duplicate type", specs: []idSpec{{typeName: "UserID", fieldName: "user_id", constructorSubject: "user"}, {typeName: "UserID", fieldName: "other_id", constructorSubject: "other"}}},
		{name: "duplicate field", specs: []idSpec{{typeName: "UserID", fieldName: "user_id", constructorSubject: "user"}, {typeName: "OtherID", fieldName: "user_id", constructorSubject: "other"}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, validateCatalog(test.specs), "validateCatalog() accepted an ambiguous catalog")
		})
	}
}

func TestValidateDeclarationsRequiresExactCatalogContraction(t *testing.T) {
	t.Parallel()

	specs := []idSpec{
		{typeName: "UserID", fieldName: "user_id", constructorSubject: "user"},
		{typeName: "ClassID", fieldName: "class_id", constructorSubject: "class"},
	}
	require.NoError(t, validateDeclarations([]string{"UserID", "ClassID"}, specs))
	for _, declared := range [][]string{
		{"UserID"},
		{"ClassID", "UserID"},
		{"UserID", "SessionID"},
	} {
		require.Error(t, validateDeclarations(declared, specs), "validateDeclarations(%v) accepted declaration drift", declared)
	}
}
