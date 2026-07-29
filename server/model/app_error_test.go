// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAppErrorTranslationWrappingAndSafeFields(t *testing.T) {
	t.Parallel()

	cause := errors.New("private database detail")
	params := map[string]any{"Field": "name"}
	fields := map[string]string{"field": "name"}
	appErr := NewAppError(
		"Institution.IsValid",
		"model.institution.is_valid.name.app_error",
		params,
		"id=private",
		http.StatusBadRequest,
	).WithSafeFields(fields).Wrap(cause)

	params["Field"] = "changed"
	fields["field"] = "changed"
	appErr.Translate(func(id string, args ...any) string {
		if id != "model.institution.is_valid.name.app_error" {
			t.Fatalf("translation id = %q", id)
		}
		values := args[0].(map[string]any)
		if values["Field"] != "name" {
			t.Fatalf("translation params = %#v", values)
		}
		return "The institution name is invalid."
	})

	if appErr.ClientMessage() != "The institution name is invalid." {
		t.Fatalf("client message = %q", appErr.ClientMessage())
	}
	if appErr.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("status = %d", appErr.HTTPStatus())
	}
	if !errors.Is(appErr, cause) {
		t.Fatal("AppError does not preserve its wrapped cause")
	}
	if appErr.SafeFields()["field"] != "name" {
		t.Fatalf("safe fields = %#v", appErr.SafeFields())
	}
	publicFields := appErr.SafeFields()
	publicFields["field"] = "mutated"
	if appErr.SafeFields()["field"] != "name" {
		t.Fatal("SafeFields exposed mutable state")
	}

	appErr.WipeDetailed()
	if errors.Is(appErr, cause) || strings.Contains(appErr.Error(), "private") {
		t.Fatalf("WipeDetailed left private detail in %q", appErr.Error())
	}
}

func TestAppErrorBoundsRenderedInternalError(t *testing.T) {
	t.Parallel()

	appErr := NewAppError("Test", "test.error", nil, strings.Repeat("x", 2048), http.StatusInternalServerError)
	if length := len(appErr.Error()); length != maxErrorLength+3 {
		t.Fatalf("rendered length = %d, want %d", length, maxErrorLength+3)
	}
}
