// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
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
			err:        app.NewError("academic_unit_member.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_unit_member.invalid",
		},
		{
			name:       "academic unit member conflict",
			err:        app.NewError("academic_unit_member.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "academic_unit_member.conflict",
		},
		{
			name:       "affiliation invalid",
			err:        app.NewError("affiliation.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "affiliation.invalid",
		},
		{
			name:       "affiliation conflict",
			err:        app.NewError("affiliation.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "affiliation.conflict",
		},
		{
			name:       "student affiliation with active enrollment",
			err:        app.NewError("affiliation.student_has_active_enrollment"),
			wantStatus: http.StatusConflict,
			wantCode:   "affiliation.student_has_active_enrollment",
		},
		{
			name: "not found preserves safe fields",
			err: app.NewError("academic_unit.not_found").
				WithField("academic_unit_id", "unit123").
				Wrap(errors.New("sql: no rows")),
			wantStatus: http.StatusNotFound,
			wantCode:   "academic_unit.not_found",
			wantField:  "academic_unit_id",
			wantValue:  "unit123",
		},
		{
			name:       "conflict",
			err:        app.NewError("class.enrollment_conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "class.enrollment_conflict",
		},
		{
			name:       "invalid request",
			err:        app.NewError("academic_unit.invalid").WithField("field", "name"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_unit.invalid",
			wantField:  "field",
			wantValue:  "name",
		},
		{
			name:       "authentication required",
			err:        app.NewError("authentication.required"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "authentication.required",
		},
		{
			name:       "authorization denied",
			err:        app.NewError("authorization.denied"),
			wantStatus: http.StatusForbidden,
			wantCode:   "authorization.denied",
		},
		{
			name:       "invalid authentication token",
			err:        app.NewError("authentication.invalid_token"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "authentication.invalid_token",
		},
		{
			name:       "invalid authorization request",
			err:        app.NewError("authorization.request.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "authorization.request.invalid",
		},
		{
			name:       "fail closed audit unavailable",
			err:        app.NewError("audit.unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "audit.unavailable",
		},
		{
			name:       "institution conflict",
			err:        app.NewError("institution.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "institution.conflict",
		},
		{
			name:       "institution invalid",
			err:        app.NewError("institution.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "institution.invalid",
		},
		{
			name:       "programme conflict",
			err:        app.NewError("programme.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "programme.conflict",
		},
		{
			name:       "programme invalid",
			err:        app.NewError("programme.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "programme.invalid",
		},
		{
			name:       "programme level conflict",
			err:        app.NewError("programme_level.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "programme_level.conflict",
		},
		{
			name:       "programme level invalid",
			err:        app.NewError("programme_level.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "programme_level.invalid",
		},
		{
			name:       "academic period conflict",
			err:        app.NewError("academic_period.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "academic_period.conflict",
		},
		{
			name:       "academic period invalid",
			err:        app.NewError("academic_period.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "academic_period.invalid",
		},
		{
			name:       "class conflict",
			err:        app.NewError("class.conflict"),
			wantStatus: http.StatusConflict,
			wantCode:   "class.conflict",
		},
		{
			name:       "class invalid",
			err:        app.NewError("class.invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "class.invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := writeErrorResponse(t, tt.err)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "sql:") ||
				strings.Contains(response.Body.String(), "no rows") {
				t.Fatalf("wrapped cause leaked: %s", response.Body.String())
			}
			var problem httpapi.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", problem.Code, tt.wantCode)
			}
			if problem.Type != "https://proctor.sudosylabs.com/problems/"+tt.wantCode {
				t.Fatalf("type = %q", problem.Type)
			}
			if problem.Status != tt.wantStatus {
				t.Fatalf("problem status = %d", problem.Status)
			}
			if problem.Instance != "/academic-units/unit123" {
				t.Fatalf("instance = %q", problem.Instance)
			}
			if tt.wantField != "" && problem.Fields[tt.wantField] != tt.wantValue {
				t.Fatalf("fields = %#v", problem.Fields)
			}
		})
	}
}

func TestWriteErrorFailsSafeForUnmappedApplicationCodes(t *testing.T) {
	t.Parallel()

	response := writeErrorResponse(t, app.NewError("future.capability.unknown_code").
		WithField("secret", "should-not-matter").
		Wrap(errors.New("sensitive operator detail")))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "sensitive") ||
		strings.Contains(response.Body.String(), "future.capability") {
		t.Fatalf("unmapped error leaked details: %s", response.Body.String())
	}
	var problem httpapi.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "internal" || problem.Status != http.StatusInternalServerError {
		t.Fatalf("problem = %#v", problem)
	}
	if len(problem.Fields) != 0 {
		t.Fatalf("unmapped error exposed fields = %#v", problem.Fields)
	}
}

func TestWriteErrorMapsConflictWithoutLeakingCauses(t *testing.T) {
	t.Parallel()

	response := writeErrorResponse(t, app.NewError("installation.already_initialized").
		WithField("resource_id", "r1").
		Wrap(errors.New("sensitive internal cause")))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatalf("leaked operator detail: %s", response.Body.String())
	}
	var problem httpapi.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "installation.already_initialized" || problem.Fields["resource_id"] != "r1" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestApplicationErrorMappingCoversEveryRegisteredCode(t *testing.T) {
	t.Parallel()

	for code, status := range httpapi.ApplicationErrorStatuses() {
		if status < 400 || status > 599 {
			t.Fatalf("code %q maps to non-error status %d", code, status)
		}
		if code == "" || code == "internal" {
			t.Fatalf("registry contains reserved or empty code %q", code)
		}
		if key := httpapi.LocalizationKey(code); key != code {
			t.Fatalf("LocalizationKey(%q) = %q, want code default", code, key)
		}
		response := writeErrorResponse(t, app.NewError(code))
		if response.Code != status {
			t.Fatalf("code %q status = %d, want %d", code, response.Code, status)
		}
		var problem httpapi.Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem.Code != code || problem.Status != status {
			t.Fatalf("code %q problem = %#v", code, problem)
		}
		if problem.Type != "https://proctor.sudosylabs.com/problems/"+code {
			t.Fatalf("code %q type = %q", code, problem.Type)
		}
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
