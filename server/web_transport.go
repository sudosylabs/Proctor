// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"errors"
	"net/http"

	"github.com/sudosylabs/proctor/server/webui"
)

// rootHTTPTransport owns the API transport lifecycle while dispatching the
// installation's API and browser modules through one listener. The API and
// webapp handlers remain siblings; neither can turn an unknown route into a
// fallback owned by the other.
type rootHTTPTransport struct {
	owned  runtimeTransport
	api    http.Handler
	webapp http.Handler
}

func newRootHTTPTransport(
	owned runtimeTransport,
	api http.Handler,
	webapp http.Handler,
) (runtimeTransport, error) {
	if owned == nil || api == nil || webapp == nil {
		return nil, errors.New("API lifecycle, API handler, and webapp handler are required")
	}
	return &rootHTTPTransport{owned: owned, api: api, webapp: webapp}, nil
}

func (t *rootHTTPTransport) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if webui.HandlesPath(request.URL.Path) {
		t.webapp.ServeHTTP(writer, request)
		return
	}
	t.api.ServeHTTP(writer, request)
}

func (t *rootHTTPTransport) Close() error {
	return t.owned.Close()
}
