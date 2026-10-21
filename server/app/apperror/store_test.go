// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package apperror_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

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
			require.True(t, ok, "Translate(%v) = %v", test.err, got)
			require.Equal(t, test.code, got.Code(), "Translate(%v) = %v", test.err, got)
			require.Equal(t, test.fields, got.Fields(), "Translate(%v) = %v", test.err, got)
			if test.err != nil {
				require.ErrorIs(t, got, test.err, "Translate")
			}
		})
	}
}

func TestStoreTablePassesApplicationErrorsThrough(t *testing.T) {
	t.Parallel()

	original := apperror.New("authorization.denied").Wrap(store.NewErrNotFound("widget", "1"))
	got, ok := apperror.As(testTable.Translate(original))
	require.True(t, ok, "Translate(application error)")
	require.Same(t, original, got, "Translate(application error)")
}

func TestStoreTableWithChecksAddedRulesFirst(t *testing.T) {
	t.Parallel()

	table := testTable.With(apperror.StoreRule{Kind: apperror.StoreConflict, Entity: "authorization", Code: "widget.not_found"})
	require.True(t, apperror.Is(table.Translate(store.NewErrConflict("authorization", "denied", nil)), "widget.not_found"), "With rule did not take precedence")
	require.True(t, apperror.Is(testTable.Translate(store.NewErrConflict("authorization", "denied", nil)), "widget.conflict"), "With mutated the original table")
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
		require.True(t, ok, "Translate")
		require.Equal(t, want, got.Code(), "Translate")
		require.Equal(t, "class", got.Fields()["resource"], "Translate")
	}
}
