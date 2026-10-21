// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/httpapi"
)

func TestWriteErrorMapsTransportNeutralApplicationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantField  string
		wantValue  string
	}{
		{
			name:       "academic unit member invalid",
			err:        apperror.New("academic_unit_member.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_unit_member.invalid",
		},
		{
			name:       "academic unit member conflict",
			err:        apperror.New("academic_unit_member.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "academic_unit_member.conflict",
		},
		{
			name:       "affiliation invalid",
			err:        apperror.New("affiliation.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "affiliation.invalid",
		},
		{
			name:       "affiliation conflict",
			err:        apperror.New("affiliation.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "affiliation.conflict",
		},
		{
			name:       "student affiliation with active enrollment",
			err:        apperror.New("affiliation.student_has_active_enrollment"),
			wantStatus: http.StatusConflict,
			wantCode:   "affiliation.student_has_active_enrollment",
		},
		{
			name: "not found preserves safe fields",
			err: apperror.New("resource.not_found").
				WithField("resource", "academic_unit").
				Wrap(errors.New("sql: no rows")),
			wantStatus: http.StatusNotFound,
			wantCode:   "resource.not_found",
			wantField:  "resource",
			wantValue:  "academic_unit",
		},
		{
			name:       "conflict",
			err:        apperror.New("class.enrollment_conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "class.enrollment_conflict",
		},
		{
			name:       "invalid request",
			err:        apperror.New("academic_unit.invalid").WithField("field", "name"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_unit.invalid",
			wantField:  "field",
			wantValue:  "name",
		},
		{
			name:       "authentication required",
			err:        apperror.New("authentication.required"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "authentication.required",
		},
		{
			name:       "authorization denied",
			err:        apperror.New("authorization.denied"),
			wantStatus: http.StatusForbidden,
			wantCode:   "authorization.denied",
		},
		{
			name:       "invalid authentication token",
			err:        apperror.New("authentication.token_invalid"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "authentication.token_invalid",
		},
		{
			name:       "invalid authorization request",
			err:        apperror.New("authorization.request.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "authorization.request.invalid",
		},
		{
			name:       "fail closed audit unavailable",
			err:        apperror.New("audit.unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "audit.unavailable",
		},
		{
			name:       "institution conflict",
			err:        apperror.New("institution.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "institution.conflict",
		},
		{
			name:       "institution invalid",
			err:        apperror.New("institution.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "institution.invalid",
		},
		{
			name:       "programme conflict",
			err:        apperror.New("programme.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "programme.conflict",
		},
		{
			name:       "programme invalid",
			err:        apperror.New("programme.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "programme.invalid",
		},
		{
			name:       "programme level conflict",
			err:        apperror.New("programme_level.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "programme_level.conflict",
		},
		{
			name:       "programme level invalid",
			err:        apperror.New("programme_level.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "programme_level.invalid",
		},
		{
			name:       "academic period conflict",
			err:        apperror.New("academic_period.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "academic_period.conflict",
		},
		{
			name:       "academic period invalid",
			err:        apperror.New("academic_period.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_period.invalid",
		},
		{
			name:       "class conflict",
			err:        apperror.New("class.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "class.conflict",
		},
		{
			name:       "class invalid",
			err:        apperror.New("class.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "class.invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := writeErrorResponse(t, tt.err)
			require.Equal(t, tt.wantStatus, response.Code, "status = %d, want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			require.NotContains(t, response.Body.String(), "sql:")
			require.NotContains(t, response.Body.String(), "no rows")
			var problem httpapi.Problem
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
			require.Equal(t, tt.wantCode, problem.Code, "code")
			require.Equal(t, "https://proctor.sudosylabs.com/problems/"+tt.wantCode, problem.Type)
			require.Equal(t, tt.wantStatus, problem.Status, "problem status")
			require.Equal(t, "/academic-units/unit123", problem.Instance)
			if tt.wantField != "" {
				require.Equal(t, tt.wantValue, problem.Fields[tt.wantField])
			}
		})
	}
}

func TestWriteErrorFailsSafeForUnmappedApplicationCodes(t *testing.T) {
	t.Parallel()

	response := writeErrorResponse(t, apperror.New("future.capability.unknown_code").
		WithField("secret", "should-not-matter").
		Wrap(errors.New("sensitive operator detail")))
	require.Equal(t, http.StatusInternalServerError, response.Code, "status")
	require.NotContains(t, response.Body.String(), "sensitive")
	require.NotContains(t, response.Body.String(), "future.capability")
	var problem httpapi.Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "server.internal", problem.Code)
	require.Equal(t, http.StatusInternalServerError, problem.Status)
	require.Empty(t, problem.Fields)
}

func TestWriteErrorMapsConflictWithoutLeakingCauses(t *testing.T) {
	t.Parallel()

	response := writeErrorResponse(t, apperror.New("installation.already_initialized").
		WithField("resource_id", "r1").
		Wrap(errors.New("sensitive internal cause")))
	require.Equal(t, http.StatusConflict, response.Code, "status")
	require.NotContains(t, response.Body.String(), "sensitive")
	var problem httpapi.Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "installation.already_initialized", problem.Code)
	require.Equal(t, "r1", problem.Fields["resource_id"])
}

func TestApplicationErrorMappingCoversEveryRegisteredCode(t *testing.T) {
	t.Parallel()

	for code, status := range httpapi.ApplicationErrorStatuses() {
		require.GreaterOrEqual(t, status, 400, "code %q maps to non-error status", code)
		require.LessOrEqual(t, status, 599, "code %q maps to non-error status", code)
		require.NotEmpty(t, code)
		require.NotEqual(t, "server.internal", code)
		response := writeErrorResponse(t, apperror.New(apperror.Code(code)))
		require.Equal(t, status, response.Code, "code %q status", code)
		var problem httpapi.Problem
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
		require.Equal(t, code, problem.Code, "code %q problem = %#v", code, problem)
		require.Equal(t, status, problem.Status, "code %q problem = %#v", code, problem)
		require.Equal(t, "https://proctor.sudosylabs.com/problems/"+code, problem.Type, "code %q type", code)
	}
}

func writeErrorResponse(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		httpapi.WriteError(writer, request, err)
	})
	request := httptest.NewRequest(http.MethodGet, "/academic-units/unit123", nil)
	request.Header.Set(httpapi.RequestIDHeader, "req-error-mapping-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
