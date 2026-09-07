// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type recoveryMFAHTTPFake struct{ MFA }

func (recoveryMFAHTTPFake) GetMFAStatus(_ context.Context, invocation application.Invocation, _ application.GetMFAStatusQuery) (*application.MFAStatus, error) {
	return &application.MFAStatus{MFARecoveryRequired: invocation.Principal().MFARecoveryRequired, AuthenticationMethod: "oidc", AuthenticationProviderID: "campus", AuthenticationStrength: model.AuthenticationSingleFactor, RecentlyAuthenticated: true}, nil
}
func (recoveryMFAHTTPFake) SetupMFA(context.Context, application.Invocation, application.SetupMFACommand) (*application.MFASetup, error) {
	return &application.MFASetup{Secret: "once-only", ProvisioningURI: "otpauth://totp/Proctor", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func TestMFARestrictedSessionOnlyReachesExplicitRecoveryRoutes(t *testing.T) {
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "oidc", AuthenticationProviderID: "campus", ExternalIdentityID: model.NewExternalIdentityID(), AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(), AuthenticationGeneration: 1, MFARecoveryRequired: true}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, mfaResource(recoveryMFAHTTPFake{}))
	for _, test := range []struct {
		path, method string
		status       int
	}{
		{"/api/v1/users/me/mfa", http.MethodGet, 200}, {"/api/v1/users/me/mfa/setup", http.MethodPost, 201},
		{"/api/v1/users/me/mfa/challenge", http.MethodPost, 401}, {"/api/v1/users/me/mfa/recovery-codes/regenerate", http.MethodPost, 401}, {"/api/v1/users/me/mfa/disable", http.MethodPost, 401},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"code":"123456"}`))
		request.Header.Set("Authorization", "Bearer recovery")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s = %d: %s", test.path, response.Code, response.Body.String())
		}
		if response.Code < 300 && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("recovery response is cacheable")
		}
	}
}
