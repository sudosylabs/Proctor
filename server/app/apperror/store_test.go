// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package apperror_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/store"
)

var errSentinel = errors.New("sentinel")

var testTable = apperror.StoreTable{
	Rules: append([]apperror.StoreRule{
		{Kind: apperror.StoreSentinel, Sentinel: errSentinel, Code: "widget.sentinel"},
		{Kind: apperror.StoreNotFound, Entity: "gadget", Code: "gadget.not_found"},
	}, apperror.Conflicts("widget_export", map[string]apperror.Code{"limit": "widget.export.limit"})...),
	Constraints:  map[string]apperror.Code{"widget_revision": "widget.revision_conflict"},
	NotFound:     "widget.not_found",
	Conflict:     "widget.conflict",
	Reference:    "widget.reference_invalid",
	Invalid:      "widget.invalid",
	InvalidField: "value",
	Unavailable:  "widget.unavailable",
	Fields:       map[string]string{"resource": "widget"},
}

func TestStoreTableTranslatesByKindEntityAndConstraint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		code   string
		fields map[string]string
	}{
		{"nil means a missing required result", nil, "widget.unavailable", map[string]string{"resource": "widget"}},
		{"sentinel rule", errors.Join(errors.New("context"), errSentinel), "widget.sentinel", map[string]string{"resource": "widget"}},
		{"entity-specific absence", store.NewErrNotFound("gadget", "1"), "gadget.not_found", map[string]string{"resource": "widget"}},
		{"default absence", store.NewErrNotFound("widget", "1"), "widget.not_found", map[string]string{"resource": "widget"}},
		{"entity-specific constraint", store.NewErrConflict("widget_export", "limit", nil), "widget.export.limit", map[string]string{"resource": "widget"}},
		{"entity mismatch falls back", store.NewErrConflict("other", "limit", nil), "widget.conflict", map[string]string{"resource": "widget"}},
		{"named constraint", store.NewErrConflict("widget", "widget_revision", nil), "widget.revision_conflict", map[string]string{"resource": "widget"}},
		{"reference", store.NewErrReference("widget", "widgets_owner_fkey", nil), "widget.reference_invalid", map[string]string{"resource": "widget"}},
		{"invalid input names the value", store.NewErrInvalidInput("widget", "name", nil), "widget.invalid", map[string]string{"resource": "widget", "field": "value"}},
		{"idempotency conflict", &store.ErrIdempotencyConflict{}, "idempotency.conflict", nil},
		{"idempotency in progress", &store.ErrIdempotencyInProgress{}, "idempotency.in_progress", nil},
		{"unknown failure", errors.New("driver detail"), "widget.unavailable", map[string]string{"resource": "widget"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := apperror.As(testTable.Translate(test.err))
			if !ok || got.Code() != test.code || !reflect.DeepEqual(got.Fields(), test.fields) {
				t.Fatalf("Translate(%v) = %v %v, want %s %v", test.err, got, got.Fields(), test.code, test.fields)
			}
			if test.err != nil && !errors.Is(got, test.err) {
				t.Fatalf("Translate(%v) lost its cause", test.err)
			}
		})
	}
}

func TestStoreTablePassesApplicationErrorsThrough(t *testing.T) {
	t.Parallel()

	original := apperror.New("authorization.denied").Wrap(store.NewErrNotFound("widget", "1"))
	if got, ok := apperror.As(testTable.Translate(original)); !ok || got != original {
		t.Fatalf("Translate(application error) = %v, want it unchanged", got)
	}
}

func TestStoreTableWithChecksAddedRulesFirst(t *testing.T) {
	t.Parallel()

	table := testTable.With(apperror.StoreRule{Kind: apperror.StoreConflict, Entity: "authorization", Code: "widget.not_found"})
	if !apperror.Is(table.Translate(store.NewErrConflict("authorization", "denied", nil)), "widget.not_found") {
		t.Fatal("With rule did not take precedence")
	}
	if !apperror.Is(testTable.Translate(store.NewErrConflict("authorization", "denied", nil)), "widget.conflict") {
		t.Fatal("With mutated the original table")
	}
}

func TestAdministrationStoreTableNamesTheResource(t *testing.T) {
	t.Parallel()

	table := apperror.AdministrationStoreTable("class", "class.conflict", "class.invalid",
		map[string]apperror.Code{"class_member_end_time": "resource.not_found"})
	for err, want := range map[error]string{
		store.NewErrNotFound("class", "1"):                          "resource.not_found",
		store.NewErrConflict("class", "classes_name_key", nil):      "class.conflict",
		store.NewErrConflict("class", "class_member_end_time", nil): "resource.not_found",
		store.NewErrReference("class", "classes_period_fkey", nil):  "class.invalid",
		store.NewErrInvalidInput("class", "name", nil):              "class.invalid",
		errors.New("driver"):                                        "administration.unavailable",
	} {
		got, ok := apperror.As(table.Translate(err))
		if !ok || got.Code() != want || got.Fields()["resource"] != "class" {
			t.Fatalf("Translate(%v) = %v %v, want %s naming class", err, got, got.Fields(), want)
		}
	}
}
