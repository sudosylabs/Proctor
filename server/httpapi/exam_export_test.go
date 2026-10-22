// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type exportsHTTPApp struct {
	calls   int
	create  exam.CreateExportCommand
	query   exam.ExportQuery
	result  *model.ExamExport
	failure error
}

func (a *exportsHTTPApp) CreateExamExport(_ context.Context, _ invocation.Invocation, c exam.CreateExportCommand) (*model.ExamExport, error) {
	a.calls++
	a.create = c
	return a.result, a.failure
}
func (a *exportsHTTPApp) GetExamExport(_ context.Context, _ invocation.Invocation, q exam.ExportQuery) (*model.ExamExport, error) {
	a.calls++
	a.query = q
	return a.result, a.failure
}
func (a *exportsHTTPApp) OpenExamExport(_ context.Context, _ invocation.Invocation, q exam.ExportQuery) (*exam.ExportDownload, error) {
	a.calls++
	a.query = q
	if a.failure != nil {
		return nil, a.failure
	}
	return &exam.ExportDownload{Export: a.result, Body: io.NopCloser(strings.NewReader("archive"))}, nil
}

func newHTTPExport(t *testing.T, p model.Principal, scope model.RetentionHoldScope) *model.ExamExport {
	t.Helper()
	at := model.TimeUTC(time.Now())
	e := &model.ExamExport{ID: model.NewExamExportID(), Scope: scope, RequesterUserID: p.UserID, Categories: []model.RetentionCategory{model.RetentionCategoryWork}, State: model.ExamExportQueued, PolicyRevision: 1, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), SourceExpiresAt: at.Add(24 * time.Hour), SubmissionCount: 1}
	require.NoError(t, e.Validate())
	return e
}

func TestExamExportsHTTPExplicitCategoriesAndExactScope(t *testing.T) {
	for _, submission := range []bool{false, true} {
		t.Run(map[bool]string{false: "Sitting", true: "Submission"}[submission], func(t *testing.T) {
			p := recordsHTTPPrincipal()
			scope := model.RetentionHoldScope{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID()}
			path := "/api/v1/exams/" + scope.ExamID.String() + "/sittings/" + scope.SittingID.String()
			if submission {
				scope.SubmissionID = model.NewSubmissionID()
				path += "/submissions/" + scope.SubmissionID.String()
			}
			path += "/exports"
			fake := &exportsHTTPApp{result: newHTTPExport(t, p, scope)}
			logger, _ := newTestLogger(t)
			api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: p}, examExportsResource(fake))
			send := func(body, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer session")
				r.Header.Set("Content-Type", "application/json")
				if key != "" {
					r.Header.Set("Idempotency-Key", key)
				}
				w := httptest.NewRecorder()
				api.ServeHTTP(w, r)
				return w
			}
			for _, body := range []string{`{}`, `{"categories":null}`, `{"categories":[]}`, `{"categories":["work","work"]}`, `{"categories":["all"]}`, `{"categories":["work"],"categories":["integrity"]}`, `{"categories":["work"],"expires_at":"never"}`} {
				w := send(body, "export-key")
				require.Equal(t, http.StatusBadRequest, w.Code, "invalid %s: %d %s", body, w.Code, w.Body.String())
				require.Equal(t, 0, fake.calls, "invalid %s: %d %s", body, w.Code, w.Body.String())
			}
			w := send(`{"categories":["work"]}`, "")
			require.Equal(t, http.StatusBadRequest, w.Code, "missing key: %d %s", w.Code, w.Body.String())
			require.Equal(t, 0, fake.calls, "missing key: %d %s", w.Code, w.Body.String())
			w = send(`{"categories":["work"]}`, "export-key")
			require.Equal(t, http.StatusAccepted, w.Code, "create: %d %s command=%#v", w.Code, w.Body.String(), fake.create)
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"), "create: %d %s command=%#v", w.Code, w.Body.String(), fake.create)
			require.Equal(t, scope, fake.create.Scope, "create: %d %s command=%#v", w.Code, w.Body.String(), fake.create)
			require.Equal(t, "export-key", fake.create.IdempotencyKey, "create: %d %s command=%#v", w.Code, w.Body.String(), fake.create)
			var result map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.NotNil(t, result["expires_at"])
			require.NotNil(t, result["construction_deadline"])
			require.Nil(t, result["download_url"])
			require.Nil(t, result["artifact"])
		})
	}
}

func TestExamExportHTTPDownloadHasFixedPrivateAttachment(t *testing.T) {
	p := recordsHTTPPrincipal()
	scope := model.RetentionHoldScope{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), SubmissionID: model.NewSubmissionID()}
	e := newHTTPExport(t, p, scope)
	e.State = model.ExamExportReady
	e.ReadyAt = model.OptionalTimeFrom(e.CreatedAt)
	e.ArchiveSHA256 = strings.Repeat("a", 64)
	e.ArchiveSizeBytes = 7
	fake := &exportsHTTPApp{result: e}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: p}, examExportsResource(fake))
	path := "/api/v1/exams/" + scope.ExamID.String() + "/sittings/" + scope.SittingID.String() + "/submissions/" + scope.SubmissionID.String() + "/exports/" + e.ID.String() + "/content"
	send := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer session")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	w := send()
	require.Equal(t, 200, w.Code, "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, "archive", w.Body.String(), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, "application/zip", w.Header().Get("Content-Type"), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, `attachment; filename="exam-export-`+e.ID.String()+`.zip"`, w.Header().Get("Content-Disposition"), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, `"`+e.ArchiveSHA256+`"`, w.Header().Get("ETag"), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"), "download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	require.Equal(t, scope, fake.query.Scope)
	require.Equal(t, e.ID, fake.query.ExportID)
	fake.failure = apperror.New("exam.export.expired")
	w = send()
	require.Equal(t, http.StatusGone, w.Code, "expired")
	require.NotContains(t, w.Body.String(), "archive", "expired")
}

func TestExamExportsHTTPForbidsPATBeforeUseCase(t *testing.T) {
	p := recordsHTTPPrincipal()
	p.CredentialType = model.CredentialPersonalAccessToken
	p.SessionID = ""
	fake := &exportsHTTPApp{}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: p}, examExportsResource(fake))
	path := "/api/v1/exams/" + model.NewExamID().String() + "/sittings/" + model.NewExamSittingID().String() + "/exports/" + model.NewExamExportID().String()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer pat")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	require.NotEqual(t, http.StatusOK, w.Code, "PAT reached export: %d %s", w.Code, w.Body.String())
	require.Equal(t, 0, fake.calls, "PAT reached export: %d %s", w.Code, w.Body.String())
}
