// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthenticationMethodRoutesRequireSessionAndStrongRecentMutations(t *testing.T) {
	resource := authenticationMethodResource(nil, browserCookies{})
	if len(resource.routes) != 5 {
		t.Fatalf("routes = %d", len(resource.routes))
	}
	got := map[string]AuthRequirement{}
	for _, route := range resource.routes {
		path, _, err := route.path.compile(model.APIURLSuffix)
		if err != nil {
			t.Fatal(err)
		}
		got[route.method+" "+path] = route.auth
	}
	if got[http.MethodGet+" /api/v1/authentication-methods"] != AuthSessionRequired {
		t.Fatalf("list auth = %s", got[http.MethodGet+" /api/v1/authentication-methods"])
	}
	strong := 0
	for key, requirement := range got {
		if key != http.MethodGet+" /api/v1/authentication-methods" {
			if requirement != AuthStrongRecentSessionRequired {
				t.Fatalf("%s auth = %s", key, requirement)
			}
			strong++
		}
	}
	if strong != 4 {
		t.Fatalf("strong recent routes = %d", strong)
	}
}
