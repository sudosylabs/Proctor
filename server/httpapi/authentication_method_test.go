// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthenticationMethodRoutesRequireSessionAndStrongRecentMutations(t *testing.T) {
	resource := authenticationMethodResource(nil, browserCookies{})
	require.Len(t, resource.routes, 5, "routes")
	got := map[string]AuthRequirement{}
	for _, route := range resource.routes {
		path, _, err := route.path.compile(model.APIURLSuffix)
		require.NoError(t, err)
		got[route.method+" "+path] = route.auth
	}
	require.Equal(t, AuthSessionRequired, got[http.MethodGet+" /api/v1/authentication-methods"])
	strong := 0
	for key, requirement := range got {
		if key != http.MethodGet+" /api/v1/authentication-methods" {
			require.Equal(t, AuthStrongRecentSessionRequired, requirement, "%s auth", key)
			strong++
		}
	}
	require.Equal(t, 4, strong, "strong recent routes")
}
