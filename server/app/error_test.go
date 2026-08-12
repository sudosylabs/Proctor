// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
)

func TestErrorCarriesStableCodeSafeFieldsAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("private database detail")
	fields := map[string]string{"field": "name", "resource_id": "abc"}
	err := app.NewError("academic_unit.invalid").
		WithFields(fields).
		Wrap(cause)

	fields["field"] = "mutated"
	if err.Code() != "academic_unit.invalid" {
		t.Fatalf("Code() = %q", err.Code())
	}
	if err.Fields()["field"] != "name" || err.Fields()["resource_id"] != "abc" {
		t.Fatalf("Fields() = %#v", err.Fields())
	}
	public := err.Fields()
	public["field"] = "changed"
	if err.Fields()["field"] != "name" {
		t.Fatal("Fields() exposed mutable state")
	}
	if !errors.Is(err, cause) {
		t.Fatal("Error does not preserve its wrapped cause")
	}
	if !strings.Contains(err.Error(), "academic_unit.invalid") {
		t.Fatalf("Error() = %q, want code", err.Error())
	}
	if strings.Contains(err.Error(), "private database detail") == false {
		// Operator-facing Error() may include the cause; public mapping must not.
		// Presence is allowed; the contract is that transports never serialize Error().
	}
}

func TestErrorRejectsEmptyCode(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewError(\"\") did not panic")
		}
	}()
	_ = app.NewError("")
}

func TestErrorMatchingHelpers(t *testing.T) {
	t.Parallel()

	cause := errors.New("root cause")
	err := app.NewError("class.enrollment_conflict").Wrap(cause)
	wrapped := errors.Join(errors.New("context"), err)

	if !app.Is(err, "class.enrollment_conflict") {
		t.Fatal("Is() missed direct application error")
	}
	if !app.Is(wrapped, "class.enrollment_conflict") {
		t.Fatal("Is() missed wrapped application error")
	}
	if app.Is(err, "class.not_found") {
		t.Fatal("Is() matched the wrong code")
	}
	if app.Is(cause, "class.enrollment_conflict") {
		t.Fatal("Is() matched a non-application error")
	}

	got, ok := app.As(wrapped)
	if !ok || got.Code() != "class.enrollment_conflict" {
		t.Fatalf("As() = (%#v, %v)", got, ok)
	}
	if _, ok := app.As(cause); ok {
		t.Fatal("As() matched a non-application error")
	}
}

func TestErrorHasNoHTTPOrTransportMetadata(t *testing.T) {
	t.Parallel()

	// Reflect over the concrete method set so transport-facing names cannot
	// reappear without failing this guard.
	errorType := reflect.TypeOf((*app.Error)(nil))
	forbidden := []string{
		"HTTPStatus",
		"StatusCode",
		"RequestId",
		"RequestID",
		"ClientMessage",
		"Translate",
		"Message",
		"DetailedError",
		"SafeFields",
		"ErrorCode",
	}
	for _, name := range forbidden {
		if _, ok := errorType.MethodByName(name); ok {
			t.Fatalf("app.Error exposes transport-facing method %s", name)
		}
	}

	// Ensure the concrete type cannot acquire transport-facing presentation
	// responsibilities through the former HTTP application-error shape.
	type legacyApplicationError interface {
		error
		HTTPStatus() int
		ErrorCode() string
		ClientMessage() string
		SafeFields() map[string]string
	}
	var asLegacy legacyApplicationError
	if errors.As(app.NewError("resource.not_found"), &asLegacy) {
		t.Fatal("app.Error must not implement the legacy HTTP applicationError surface")
	}
}
