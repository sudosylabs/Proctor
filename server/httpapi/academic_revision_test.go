// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicRevisionHTTPContract(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"academic_unit", "programme", "programme_level", "academic_period", "class"} {
		t.Run(kind, func(t *testing.T) {
			logger, _ := newTestLogger(t)
			principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
			module, path, revision := academicRevisionHTTPFixture(kind, principal)
			api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, module)
			call := func(method, suffix, body string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer credential")
				if body != "" {
					request.Header.Set("Content-Type", "application/json")
				}
				response := httptest.NewRecorder()
				api.ServeHTTP(response, request)
				return response
			}
			response := call(http.MethodPatch, "", `{"expected_revision":7}`)
			require.Equal(t, http.StatusOK, response.Code, "patch status = %d: %s", response.Code, response.Body.String())
			var result map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, float64(7), result["revision"], "revision response/command = %#v", result)
			require.NotNil(t, revision(http.MethodPatch), "revision response/command = %#v", result)
			require.Equal(t, int64(7), *revision(http.MethodPatch), "revision response/command = %#v", result)
			for _, body := range []string{`{"expected_revision":null}`, `{"expected_revision":0}`, `{"expected_revision":-1}`, `{"expected_revision":1.5}`} {
				require.Equal(t, http.StatusBadRequest, call(http.MethodPatch, "", body).Code, "invalid patch %s", body)
			}
			require.Equal(t, http.StatusOK, call(http.MethodPatch, "", `{}`).Code, "legacy patch failed")
			require.Nil(t, revision(http.MethodPatch), "legacy patch failed")
			require.Equal(t, http.StatusNoContent, call(http.MethodDelete, "", `{"expected_revision":7}`).Code, "archive precondition failed")
			require.NotNil(t, revision(http.MethodDelete), "archive precondition failed")
			require.Equal(t, int64(7), *revision(http.MethodDelete), "archive precondition failed")
			for _, body := range []string{`{"expected_revision":null}`, `{"expected_revision":0}`, `{"expected_revision":-1}`, `{"expected_revision":"7"}`, `{"revision":7}`} {
				require.Equal(t, http.StatusBadRequest, call(http.MethodDelete, "", body).Code, "invalid archive %s", body)
			}
			require.Equal(t, http.StatusNoContent, call(http.MethodDelete, "", "").Code, "legacy archive failed")
			require.Nil(t, revision(http.MethodDelete), "legacy archive failed")
		})
	}
}

func academicRevisionHTTPFixture(kind string, principal model.Principal) (resource, string, func(string) *int64) {
	switch kind {
	case "academic_unit":
		value := &model.AcademicUnit{ID: model.NewAcademicUnitID(), Revision: 7}
		application := &academicUnitHTTPApplication{unit: value, principal: principal}
		return academicUnitResource(application), "/api/v1/academic-units/" + value.ID.String(), func(method string) *int64 {
			if method == http.MethodDelete {
				return application.archiveCommand.ExpectedRevision
			}
			return application.updateCommand.ExpectedRevision
		}
	case "programme":
		value := &model.Programme{ID: model.NewProgrammeID(), Revision: 7}
		application := &programmeHTTPApplication{result: value}
		return programmeResource(application), "/api/v1/programmes/" + value.ID.String(), func(method string) *int64 {
			if method == http.MethodDelete {
				return application.archiveCommand.ExpectedRevision
			}
			return application.updateCommand.ExpectedRevision
		}
	case "programme_level":
		value := &model.ProgrammeLevel{ID: model.NewProgrammeLevelID(), Revision: 7}
		application := &programmeLevelHTTPApplication{result: value}
		return programmeLevelResource(application), "/api/v1/programme-levels/" + value.ID.String(), func(method string) *int64 {
			if method == http.MethodDelete {
				return application.archiveCommand.ExpectedRevision
			}
			return application.updateCommand.ExpectedRevision
		}
	case "academic_period":
		value := &model.AcademicPeriod{ID: model.NewAcademicPeriodID(), Revision: 7}
		application := &academicPeriodHTTPApplication{result: value}
		return academicPeriodResource(application), "/api/v1/academic-periods/" + value.ID.String(), func(method string) *int64 {
			if method == http.MethodDelete {
				return application.archiveCommand.ExpectedRevision
			}
			return application.updateCommand.ExpectedRevision
		}
	case "class":
		value := &model.Class{ID: model.NewClassID(), Revision: 7}
		application := &classHTTPApplication{result: value}
		return classResource(application), "/api/v1/classes/" + value.ID.String(), func(method string) *int64 {
			if method == http.MethodDelete {
				return application.archiveCommand.ExpectedRevision
			}
			return application.updateCommand.ExpectedRevision
		}
	default:
		panic("unknown academic fixture")
	}
}
