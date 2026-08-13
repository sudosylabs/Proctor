// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderIsDeterministicAndComplete(t *testing.T) {
	t.Parallel()
	if len(entityIDs) != 26 {
		t.Fatalf("typed-ID catalog length = %d, want 26", len(entityIDs))
	}

	first, err := render(entityIDs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := render(entityIDs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("render produced different output for the same catalog")
	}
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
			if strings.Count(string(first), declaration) != 1 {
				t.Fatalf("generated declaration %q count = %d, want 1", declaration, strings.Count(string(first), declaration))
			}
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
			if err := validateCatalog(test.specs); err == nil {
				t.Fatal("validateCatalog() accepted an ambiguous catalog")
			}
		})
	}
}

func TestValidateDeclarationsRequiresExactCatalogContraction(t *testing.T) {
	t.Parallel()

	specs := []idSpec{
		{typeName: "UserID", fieldName: "user_id", constructorSubject: "user"},
		{typeName: "ClassID", fieldName: "class_id", constructorSubject: "class"},
	}
	if err := validateDeclarations([]string{"UserID", "ClassID"}, specs); err != nil {
		t.Fatalf("validateDeclarations() = %v", err)
	}
	for _, declared := range [][]string{
		{"UserID"},
		{"ClassID", "UserID"},
		{"UserID", "SessionID"},
	} {
		if err := validateDeclarations(declared, specs); err == nil {
			t.Fatalf("validateDeclarations(%v) accepted declaration drift", declared)
		}
	}
}
