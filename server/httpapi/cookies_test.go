// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestBrowserCookiesAreBoundedSecureAndCSRFSigned(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	if err != nil {
		t.Fatal(err)
	}
	cookies.now = func() time.Time { return now }
	tokens := &model.AuthenticationTokens{
		AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccessExpiresAt:  now.Add(15 * time.Minute),
		RefreshExpiresAt: now.Add(24 * time.Hour),
	}
	response := httptest.NewRecorder()
	cookies.attach(response, tokens)
	attached := response.Result().Cookies()
	if len(attached) != 4 {
		t.Fatalf("cookies = %#v", attached)
	}
	byName := make(map[string]*http.Cookie, len(attached))
	for _, cookie := range attached {
		byName[cookie.Name] = cookie
		if !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode ||
			cookie.MaxAge <= 0 {
			t.Fatalf("unsafe browser cookie = %#v", cookie)
		}
	}
	if !byName[BrowserAccessCookieName].HttpOnly ||
		byName[BrowserAccessCookieName].Path != "/" ||
		!byName[BrowserRefreshCookieName].HttpOnly ||
		byName[BrowserRefreshCookieName].Path != model.APIURLSuffix+"/auth/refresh" ||
		!byName[BrowserCSRFBindingCookieName].HttpOnly ||
		byName[BrowserCSRFCookieName].HttpOnly {
		t.Fatalf("cookie contracts = %#v", byName)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/roles", nil)
	for _, cookie := range attached {
		request.AddCookie(cookie)
	}
	request.Header.Set(BrowserCSRFHeader, byName[BrowserCSRFCookieName].Value)
	if appErr := cookies.verifyCSRF(request); appErr != nil {
		t.Fatal(appErr)
	}
	request.Header.Set(BrowserCSRFHeader, "tampered")
	if appErr := cookies.verifyCSRF(request); applicationErrorCode(appErr) != "authentication.csrf.invalid" {
		t.Fatalf("tampered CSRF error = %v", appErr)
	}

	clearedResponse := httptest.NewRecorder()
	cookies.clear(clearedResponse)
	for _, cookie := range clearedResponse.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			t.Fatalf("cookie was not expired = %#v", cookie)
		}
	}
}

func TestRequestCredentialRejectsAmbiguousSourcesAndDuplicateCookies(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	request.Header.Set("Authorization", "Bearer header-token")
	request.AddCookie(&http.Cookie{Name: BrowserAccessCookieName, Value: "cookie-token"})
	if _, appErr := requestAccessCredential(request); applicationErrorCode(appErr) != "authentication.credential_ambiguous" {
		t.Fatalf("header and cookie error = %v", appErr)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	request.AddCookie(&http.Cookie{Name: BrowserAccessCookieName, Value: "first"})
	request.AddCookie(&http.Cookie{Name: BrowserAccessCookieName, Value: "second"})
	if _, appErr := requestAccessCredential(request); applicationErrorCode(appErr) != "authentication.credential_ambiguous" {
		t.Fatalf("duplicate cookie error = %v", appErr)
	}
}
