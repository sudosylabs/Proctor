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
	"net/url"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicUnitMemberHTTPPagingPreservesArrayAndContinuation(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	scopeID := model.NewAcademicUnitID()
	member := &model.AcademicUnitMember{ID: model.NewAcademicUnitMemberID(), UserID: model.NewUserID(), AcademicUnitID: scopeID}
	service := &academicUnitMemberHTTPApplication{pageResult: &application.AcademicUnitMemberPage{Members: []*model.AcademicUnitMember{member}, HasMore: true, ActiveAt: 1000}}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, academicUnitMemberResource(service))
	path := "/api/v1/academic-units/" + scopeID.String() + "/members"
	call := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer credential")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	response := call(path + "?limit=1")
	if response.Code != http.StatusOK {
		t.Fatalf("page status = %d: %s", response.Code, response.Body.String())
	}
	var result []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result) != 1 {
		t.Fatalf("page did not retain bare array: %s, %v", response.Body.String(), err)
	}
	link := response.Header().Get("Link")
	if !strings.HasSuffix(link, `>; rel="next"`) {
		t.Fatalf("next Link = %s", link)
	}
	target := strings.TrimSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`)
	next, err := url.Parse(target)
	if err != nil || next.Host != "" || next.Path != path || next.Query().Get("active_at") != "1000" || next.Query().Get("limit") != "1" {
		t.Fatalf("next URL = %#v, %v", next, err)
	}
	service.pageResult = &application.AcademicUnitMemberPage{ActiveAt: 1000}
	response = call(target)
	if response.Code != http.StatusOK || response.Header().Get("Link") != "" || service.pageQuery.AfterID != member.ID || service.pageQuery.AfterUserID != member.UserID {
		t.Fatalf("final page/query = %d %#v", response.Code, service.pageQuery)
	}
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=1&limit=2", "?cursor=", "?limit=1&history=true&active_at=1000", "?limit=1&history=invalid"} {
		if response := call(path + suffix); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid page query %s = %d", suffix, response.Code)
		}
	}
	service.pageResult = &application.AcademicUnitMemberPage{HasMore: true, Members: []*model.AcademicUnitMember{member}}
	response = call(path + "?limit=1&history=true")
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Link"), "history=true") || strings.Contains(response.Header().Get("Link"), "active_at=") {
		t.Fatalf("history continuation = %s", response.Header().Get("Link"))
	}
}

func TestClassMemberHTTPPagingPreservesArrayAndContinuation(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	scopeID := model.NewClassID()
	member := &model.ClassMember{ID: model.NewClassMemberID(), UserID: model.NewUserID(), ClassID: scopeID}
	service := &classMemberHTTPApplication{pageResult: &application.ClassMemberPage{Members: []*model.ClassMember{member}, HasMore: true, ActiveAt: 1000}}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, classMemberResource(service))
	path := "/api/v1/classes/" + scopeID.String() + "/members"
	call := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer credential")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	response := call(path + "?limit=1")
	if response.Code != http.StatusOK {
		t.Fatalf("page status = %d: %s", response.Code, response.Body.String())
	}
	var result []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result) != 1 {
		t.Fatalf("page did not retain bare array: %s, %v", response.Body.String(), err)
	}
	link := response.Header().Get("Link")
	if !strings.HasSuffix(link, `>; rel="next"`) {
		t.Fatalf("next Link = %s", link)
	}
	target := strings.TrimSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`)
	next, err := url.Parse(target)
	if err != nil || next.Host != "" || next.Path != path || next.Query().Get("active_at") != "1000" || next.Query().Get("limit") != "1" {
		t.Fatalf("next URL = %#v, %v", next, err)
	}
	service.pageResult = &application.ClassMemberPage{ActiveAt: 1000}
	response = call(target)
	if response.Code != http.StatusOK || response.Header().Get("Link") != "" || service.pageQuery.AfterID != member.ID || service.pageQuery.AfterUserID != member.UserID {
		t.Fatalf("final page/query = %d %#v", response.Code, service.pageQuery)
	}
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=1&limit=2", "?cursor=", "?limit=1&history=true&active_at=1000", "?limit=1&history=invalid"} {
		if response := call(path + suffix); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid page query %s = %d", suffix, response.Code)
		}
	}
	service.pageResult = &application.ClassMemberPage{HasMore: true, Members: []*model.ClassMember{member}}
	response = call(path + "?limit=1&history=true")
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Link"), "history=true") || strings.Contains(response.Header().Get("Link"), "active_at=") {
		t.Fatalf("history continuation = %s", response.Header().Get("Link"))
	}
}
