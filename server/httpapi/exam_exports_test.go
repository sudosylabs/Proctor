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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type exportsHTTPApp struct {
	calls   int
	create  application.CreateExamExportCommand
	query   application.ExamExportQuery
	result  *model.ExamExport
	failure error
}

func (a *exportsHTTPApp) CreateExamExport(_ context.Context, _ application.Invocation, c application.CreateExamExportCommand) (*model.ExamExport, error) {
	a.calls++
	a.create = c
	return a.result, a.failure
}
func (a *exportsHTTPApp) GetExamExport(_ context.Context, _ application.Invocation, q application.ExamExportQuery) (*model.ExamExport, error) {
	a.calls++
	a.query = q
	return a.result, a.failure
}
func (a *exportsHTTPApp) OpenExamExport(_ context.Context, _ application.Invocation, q application.ExamExportQuery) (*application.ExamExportDownload, error) {
	a.calls++
	a.query = q
	if a.failure != nil {
		return nil, a.failure
	}
	return &application.ExamExportDownload{Export: a.result, Body: io.NopCloser(strings.NewReader("archive"))}, nil
}

func newHTTPExport(t *testing.T, p model.Principal, scope model.RetentionHoldScope) *model.ExamExport {
	t.Helper()
	at := model.TimeUTC(time.Now())
	e := &model.ExamExport{ID: model.NewExamExportID(), Scope: scope, RequesterUserID: p.UserID, Categories: []model.RetentionCategory{model.RetentionCategoryWork}, State: model.ExamExportQueued, PolicyRevision: 1, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), SourceExpiresAt: at.Add(24 * time.Hour), SubmissionCount: 1}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
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
				if w.Code != http.StatusBadRequest || fake.calls != 0 {
					t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body.String())
				}
			}
			w := send(`{"categories":["work"]}`, "")
			if w.Code != http.StatusBadRequest || fake.calls != 0 {
				t.Fatalf("missing key: %d %s", w.Code, w.Body.String())
			}
			w = send(`{"categories":["work"]}`, "export-key")
			if w.Code != http.StatusAccepted || w.Header().Get("Cache-Control") != "private, no-store" || fake.create.Scope != scope || fake.create.IdempotencyKey != "export-key" {
				t.Fatalf("create: %d %s command=%#v", w.Code, w.Body.String(), fake.create)
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["expires_at"] == nil || result["construction_deadline"] == nil || result["download_url"] != nil || result["artifact"] != nil {
				t.Fatalf("unsafe metadata=%#v", result)
			}
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
	if w.Code != 200 || w.Body.String() != "archive" || w.Header().Get("Content-Type") != "application/zip" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Disposition") != `attachment; filename="exam-export-`+e.ID.String()+`.zip"` || w.Header().Get("ETag") != `"`+e.ArchiveSHA256+`"` || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download=%d %s headers=%#v", w.Code, w.Body.String(), w.Header())
	}
	if fake.query.Scope != scope || fake.query.ExportID != e.ID {
		t.Fatalf("download scope=%#v", fake.query)
	}
	fake.failure = application.NewError("exam.export.expired")
	w = send()
	if w.Code != http.StatusGone || strings.Contains(w.Body.String(), "archive") {
		t.Fatalf("expired=%d %s", w.Code, w.Body.String())
	}
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
	if w.Code == http.StatusOK || fake.calls != 0 {
		t.Fatalf("PAT reached export: %d %s", w.Code, w.Body.String())
	}
}
