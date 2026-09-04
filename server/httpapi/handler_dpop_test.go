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
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type dpopMiddlewareAuthenticator struct{}

func (dpopMiddlewareAuthenticator) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	return nil, nil
}

func (dpopMiddlewareAuthenticator) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	return nil, nil
}

func (dpopMiddlewareAuthenticator) AuthenticateDPoP(context.Context, string, string, string, string) (*model.Principal, error) {
	return nil, nil
}

func TestProtectedRouteManifestDeclaresDPoPAuthenticationErrors(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	api := &API{
		authenticator: dpopMiddlewareAuthenticator{}, logger: logger, cookies: cookies,
		recentAuthenticationTTL: time.Minute,
	}
	for _, requirement := range []AuthRequirement{AuthSessionRequired} {
		requirement := requirement
		t.Run(string(requirement), func(t *testing.T) {
			declared := routeErrorCodes(requirement, []string{"authentication.credentials.invalid"})
			operationPolicy := newRouteErrorPolicy(declared)
			handler := api.newHandlerWithErrorPolicy(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("protected handler ran after invalid DPoP proof")
			}), requirement, operationPolicy)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/protected", nil)
			request.Header.Set("Authorization", "DPoP "+model.NewCredentialToken())
			request.Header.Set("DPoP", "invalid proof")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("WWW-Authenticate"); got != `DPoP error="invalid_dpop_proof"` {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
			if _, declared := operationPolicy["authentication.dpop.invalid"]; !declared {
				t.Fatal("protected route manifest omitted DPoP authentication error")
			}
		})
	}
}
