// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/model"
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
			var problem api.Problem
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
	var problem api.Problem
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

func TestWriteErrorPreservesLegacyModelAppErrorBridge(t *testing.T) {
	t.Parallel()

	// Characterization of the temporary compatibility path: unmigrated
	// capabilities still return *model.AppError and must keep the same
	// Problem Details shape until ticket 39 removes the bridge.
	appErr := model.NewAppError(
		"TestWriteErrorPreservesLegacyModelAppErrorBridge",
		"legacy.bridge",
		nil,
		"database detail",
		http.StatusConflict,
	).WithSafeFields(map[string]string{"resource_id": "r1"}).Wrap(
		errors.New("sensitive internal cause"),
	)
	appErr.Translate(func(string, ...any) string {
		return "The resource conflicts with current state."
	})

	response := writeErrorResponse(t, appErr)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "sensitive") ||
		strings.Contains(response.Body.String(), "database detail") {
		t.Fatalf("legacy bridge leaked operator detail: %s", response.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "legacy.bridge" || problem.Fields["resource_id"] != "r1" {
		t.Fatalf("problem = %#v", problem)
	}
	if problem.Detail != "The resource conflicts with current state." {
		t.Fatalf("detail = %q", problem.Detail)
	}
}

func TestApplicationErrorMappingCoversEveryRegisteredCode(t *testing.T) {
	t.Parallel()

	for code, status := range api.ApplicationErrorStatuses() {
		if status < 400 || status > 599 {
			t.Fatalf("code %q maps to non-error status %d", code, status)
		}
		if code == "" || code == "internal" {
			t.Fatalf("registry contains reserved or empty code %q", code)
		}
		if key := api.LocalizationKey(code); key != code {
			t.Fatalf("LocalizationKey(%q) = %q, want code default", code, key)
		}
		response := writeErrorResponse(t, app.NewError(code))
		if response.Code != status {
			t.Fatalf("code %q status = %d, want %d", code, response.Code, status)
		}
		var problem api.Problem
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
		api.WriteError(writer, request, err)
	})
	request := httptest.NewRequest(http.MethodGet, "/academic-units/unit123", nil)
	request.Header.Set(api.RequestIDHeader, "req-error-mapping-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
