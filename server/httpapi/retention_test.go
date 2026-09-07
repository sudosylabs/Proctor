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
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type retentionHTTPFake struct {
	RetentionApplication
	control    application.ChangeRetentionControlCommand
	preview    application.CreateRetentionPreviewCommand
	noticePage *application.RetentionNoticePage
	recordPage *application.RetentionRecordPage
	after      model.RetentionRetirementID
	calls      int
}

func (f *retentionHTTPFake) ChangeRetentionControl(_ context.Context, _ application.Invocation, c application.ChangeRetentionControlCommand) (*model.RetentionControl, error) {
	f.calls++
	f.control = c
	return &model.RetentionControl{Revision: 2, State: c.State, ApprovedPolicyRevision: c.ExpectedPolicyRevision, ApprovedPreviewID: c.PreviewID, UpdatedAt: model.NowUTC()}, nil
}
func (f *retentionHTTPFake) CreateRetentionPreview(_ context.Context, _ application.Invocation, c application.CreateRetentionPreviewCommand) (*model.RetentionPreview, error) {
	f.calls++
	f.preview = c
	return &model.RetentionPreview{ID: model.NewRetentionPreviewID(), PolicyRevision: c.ExpectedPolicyRevision, CreatedAt: model.NowUTC(), ExpiresAt: model.NowUTC().Add(time.Hour)}, nil
}
func (f *retentionHTTPFake) ListRetentionNotices(_ context.Context, _ application.Invocation, after model.RetentionRetirementID, _ int) (*application.RetentionNoticePage, error) {
	f.calls++
	f.after = after
	return f.noticePage, nil
}
func (f *retentionHTTPFake) ListRetentionRecords(context.Context, application.Invocation, model.SubmissionID, int) (*application.RetentionRecordPage, error) {
	f.calls++
	return f.recordPage, nil
}
func retentionHTTPPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor, AuthenticatedAt: model.NowUTC(), MFACompletedAt: model.OptionalTimeFrom(model.NowUTC()), ClientType: model.SessionClientWeb}
}
func serveRetentionHTTP(api http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
func TestRetentionHTTPControlRequiresInteractiveRecentProofAndIdempotency(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := retentionHTTPPrincipal()
	previewID := model.NewRetentionPreviewID()
	body := `{"expected_revision":1,"expected_policy_revision":3,"state":"enabled","preview_id":"` + previewID.String() + `"}`
	for _, test := range []struct {
		name  string
		alter func(*model.Principal)
		key   string
		want  int
	}{
		{"valid", func(*model.Principal) {}, "approve-retention", 200},
		{"missing key", func(*model.Principal) {}, "", 400},
		{"single factor", func(p *model.Principal) {
			p.AuthenticationStrength = model.AuthenticationSingleFactor
			p.MFACompletedAt = model.OptionalTime{}
		}, "approve-retention", 403},
		{"stale proof", func(p *model.Principal) {
			p.AuthenticatedAt = model.NowUTC().Add(-24 * time.Hour)
			p.MFACompletedAt = model.OptionalTimeFrom(p.AuthenticatedAt)
		}, "approve-retention", 403},
		{"PAT", func(p *model.Principal) { p.CredentialType = model.CredentialPersonalAccessToken; p.SessionID = "" }, "approve-retention", 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := principal
			test.alter(&p)
			app := &retentionHTTPFake{}
			api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: p}, retentionResource(app))
			r := serveRetentionHTTP(api, "PUT", "/api/v1/retention/control", body, test.key)
			if r.Code != test.want {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}
			if test.want != 200 && app.calls != 0 {
				t.Fatal("blocked control reached application")
			}
			if test.want == 200 && (app.control.PreviewID != previewID || app.control.ExpectedPolicyRevision != 3 || app.control.IdempotencyKey != test.key || r.Header().Get("Cache-Control") != "private, no-store") {
				t.Fatal("approval contract lost")
			}
		})
	}
}
func TestRetentionHTTPRejectsUnknownFieldsAndCrossPurposeCursors(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	app := &retentionHTTPFake{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: retentionHTTPPrincipal()}, retentionResource(app))
	r := serveRetentionHTTP(api, "POST", "/api/v1/retention/previews", `{"expected_policy_revision":1,"delete_now":true}`, "preview")
	if r.Code != 400 || app.calls != 0 {
		t.Fatal("unknown destructive parameter reached application")
	}
	cursor, err := encodeOpaqueCursor(retentionCursor{After: model.NewSubmissionID().String()}, retentionCursorSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?cursor=" + cursor, "?limit=1&limit=2", "?user_id=" + model.NewUserID().String(), "?limit=201"} {
		r = serveRetentionHTTP(api, "GET", "/api/v1/users/me/retention-notices"+query, "", "")
		if r.Code != 400 || app.calls != 0 {
			t.Fatalf("bad query admitted: %s %d", query, r.Code)
		}
	}
}
func TestRetentionHTTPNoticeProjectionIsRecipientOnlyAndBounded(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	p := retentionHTTPPrincipal()
	notice := model.RetentionNotice{RetirementID: model.NewRetentionRetirementID(), RecipientUserID: p.UserID, Scope: model.RetentionHoldScope{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), SubmissionID: model.NewSubmissionID()}, Category: model.RetentionCategoryWork, State: model.RetentionRetirementGrace, CreatedAt: model.NowUTC(), RetireAfter: model.NowUTC().Add(7 * 24 * time.Hour), DeliveryState: "failed", DeliveryErrorCode: "mail.preparation_unavailable"}
	app := &retentionHTTPFake{noticePage: &application.RetentionNoticePage{Items: []model.RetentionNotice{notice}, HasMore: true}}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: p}, retentionResource(app))
	r := serveRetentionHTTP(api, "GET", "/api/v1/users/me/retention-notices?limit=1", "", "")
	var page retentionNoticesResponse
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.NextCursor == "" || r.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("notice=%d %s", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "recipient_user_id") {
		t.Fatal("notice leaked recipient selection")
	}
	app.noticePage = &application.RetentionNoticePage{Items: []model.RetentionNotice{}}
	r = serveRetentionHTTP(api, "GET", "/api/v1/users/me/retention-notices?limit=1&cursor="+page.NextCursor, "", "")
	if r.Code != 200 || app.after != notice.RetirementID {
		t.Fatal("cursor did not roundtrip")
	}
	notice.RecipientUserID = model.NewUserID()
	app.noticePage = &application.RetentionNoticePage{Items: []model.RetentionNotice{notice}}
	r = serveRetentionHTTP(api, "GET", "/api/v1/users/me/retention-notices", "", "")
	if r.Code != 503 {
		t.Fatal("wrong recipient response exposed")
	}
}
