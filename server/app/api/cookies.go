// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/login.go,
// server/channels/app/authentication.go, and server/channels/web/handlers.go.
// Proctor separates its Electron/browser cookie transport from bearer clients
// and uses a rotating signed double-submit value for CSRF protection.

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	BrowserAccessCookieName        = "PROCTOR_ACCESS"
	BrowserRefreshCookieName       = "PROCTOR_REFRESH"
	BrowserCSRFBindingCookieName   = "PROCTOR_CSRF_BINDING"
	BrowserCSRFCookieName          = "PROCTOR_CSRF"
	BrowserExternalLoginCookieName = "PROCTOR_EXTERNAL_LOGIN"
	BrowserCSRFHeader              = "X-Proctor-CSRF-Token"
)

const csrfMessage = "proctor-browser-csrf-v1"

type browserCookies struct {
	secure            bool
	refreshPath       string
	externalLoginPath string
	now               func() time.Time
}

func newBrowserCookies(publicURL string) (browserCookies, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return browserCookies{}, errors.New("public URL must be an absolute HTTP or HTTPS URL")
	}
	return browserCookies{
		secure:            parsed.Scheme == "https",
		refreshPath:       model.APIURLSuffix + "/auth/refresh",
		externalLoginPath: model.APIURLSuffix + "/auth/providers/",
		now:               time.Now,
	}, nil
}

func (c browserCookies) attachExternalLoginBinding(
	writer http.ResponseWriter,
	binding string,
	expiresAt int64,
) {
	c.set(
		writer,
		BrowserExternalLoginCookieName,
		binding,
		c.externalLoginPath,
		expiresAt,
		true,
	)
}

func (c browserCookies) clearExternalLoginBinding(writer http.ResponseWriter) {
	c.expire(
		writer,
		BrowserExternalLoginCookieName,
		c.externalLoginPath,
		true,
	)
}

func (c browserCookies) attach(
	writer http.ResponseWriter,
	tokens *model.AuthenticationTokens,
) {
	if tokens == nil {
		return
	}
	binding := model.NewCredentialToken()
	csrf := browserCSRFToken(binding)
	c.set(
		writer,
		BrowserAccessCookieName,
		tokens.AccessToken,
		"/",
		tokens.AccessExpiresAt,
		true,
	)
	c.set(
		writer,
		BrowserRefreshCookieName,
		tokens.RefreshToken,
		c.refreshPath,
		tokens.RefreshExpiresAt,
		true,
	)
	c.set(
		writer,
		BrowserCSRFBindingCookieName,
		binding,
		"/",
		tokens.RefreshExpiresAt,
		true,
	)
	c.set(
		writer,
		BrowserCSRFCookieName,
		csrf,
		"/",
		tokens.RefreshExpiresAt,
		false,
	)
}

func (c browserCookies) clear(writer http.ResponseWriter) {
	c.expire(writer, BrowserAccessCookieName, "/", true)
	c.expire(writer, BrowserRefreshCookieName, c.refreshPath, true)
	c.expire(writer, BrowserCSRFBindingCookieName, "/", true)
	c.expire(writer, BrowserCSRFCookieName, "/", false)
}

func (c browserCookies) verifyCSRF(request *http.Request) *model.AppError {
	binding, appErr := singleCookieValue(request, BrowserCSRFBindingCookieName)
	if appErr != nil || binding == "" {
		return csrfError()
	}
	cookieToken, appErr := singleCookieValue(request, BrowserCSRFCookieName)
	if appErr != nil || cookieToken == "" {
		return csrfError()
	}
	headerToken := request.Header.Get(BrowserCSRFHeader)
	expected := browserCSRFToken(binding)
	if headerToken == "" ||
		subtle.ConstantTimeCompare([]byte(cookieToken), []byte(expected)) != 1 ||
		subtle.ConstantTimeCompare([]byte(headerToken), []byte(expected)) != 1 {
		return csrfError()
	}
	return nil
}

func (c browserCookies) set(
	writer http.ResponseWriter,
	name string,
	value string,
	path string,
	expiresAt int64,
	httpOnly bool,
) {
	expires := time.UnixMilli(expiresAt)
	maxAge := int(time.Until(expires).Seconds())
	if c.now != nil {
		maxAge = int(expires.Sub(c.now()).Seconds())
	}
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		Expires:  expires,
		HttpOnly: httpOnly,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c browserCookies) expire(
	writer http.ResponseWriter,
	name string,
	path string,
	httpOnly bool,
) {
	http.SetCookie(writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: httpOnly,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func browserCSRFToken(binding string) string {
	mac := hmac.New(sha256.New, []byte(binding))
	_, _ = mac.Write([]byte(csrfMessage))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func csrfError() *model.AppError {
	return model.NewAppError(
		"browserCookies.verifyCSRF",
		"authentication.csrf.invalid",
		nil,
		"",
		http.StatusForbidden,
	)
}
