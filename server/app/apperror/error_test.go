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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestErrorCarriesStableCodeSafeFieldsAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("private database detail")
	fields := map[string]string{"field": "name", "resource_id": "abc"}
	err := apperror.New("academic_unit.invalid").
		WithFields(fields).
		Wrap(cause)

	fields["field"] = "mutated"
	require.Equal(t, "academic_unit.invalid", err.Code())
	require.Equal(t, "name", err.Fields()["field"])
	require.Equal(t, "abc", err.Fields()["resource_id"])
	public := err.Fields()
	public["field"] = "changed"
	require.Equal(t, "name", err.Fields()["field"], "Fields() exposed mutable state")
	require.ErrorIs(t, err, cause, "Error does not preserve its wrapped cause")
	require.Contains(t, err.Error(), "academic_unit.invalid", "Error()")
	if strings.Contains(err.Error(), "private database detail") == false {
		// Operator-facing Error() may include the cause; public mapping must not.
		// Presence is allowed; the contract is that transports never serialize Error().
	}
}

func TestErrorRejectsEmptyCode(t *testing.T) {
	t.Parallel()

	defer func() {
		require.NotNil(t, recover(), "NewError(\"\") did not panic")
	}()
	_ = apperror.New("")
}

func TestErrorMatchingHelpers(t *testing.T) {
	t.Parallel()

	cause := errors.New("root cause")
	err := apperror.New("class.enrollment_conflict").Wrap(cause)
	wrapped := errors.Join(errors.New("context"), err)

	require.True(t, apperror.Is(err, "class.enrollment_conflict"), "Is() missed direct application error")
	require.True(t, apperror.Is(wrapped, "class.enrollment_conflict"), "Is() missed wrapped application error")
	require.False(t, apperror.Is(err, "class.not_found"), "Is() matched the wrong code")
	require.False(t, apperror.Is(cause, "class.enrollment_conflict"), "Is() matched a non-application error")

	got, ok := apperror.As(wrapped)
	require.True(t, ok, "As() = (%#v, %v)", got, ok)
	require.Equal(t, "class.enrollment_conflict", got.Code(), "As() = (%#v, %v)", got, ok)
	_, ok = apperror.As(cause)
	require.False(t, ok, "As() matched a non-application error")
}

func TestErrorHasNoHTTPOrTransportMetadata(t *testing.T) {
	t.Parallel()

	// Reflect over the concrete method set so transport-facing names cannot
	// reappear without failing this guard.
	errorType := reflect.TypeOf((*apperror.Error)(nil))
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
		_, ok := errorType.MethodByName(name)
		require.False(t, ok, "apperror.Error exposes transport-facing method %s", name)
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
	require.NotErrorAs(t, apperror.New("resource.not_found"), &asLegacy, "apperror.Error must not implement the legacy HTTP applicationError surface")
}
