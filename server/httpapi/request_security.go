// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/sudosylabs/proctor/server/internal/weborigin"
)

// browserRequestPolicy protects unsafe requests, including public operations
// that create cookies before a Session or double-submit CSRF proof exists.
// Its authority is the configured public origin, never Host or proxy headers.
type browserRequestPolicy struct {
	origin string
}

func newBrowserRequestPolicy(publicURL string) (browserRequestPolicy, error) {
	origin, err := weborigin.FromPublicURL(publicURL)
	if err != nil {
		return browserRequestPolicy{}, err
	}
	return browserRequestPolicy{origin: origin}, nil
}

func (p browserRequestPolicy) allows(request *http.Request) bool {
	// Existing provider callbacks use GET and retain their own one-use state and
	// browser binding. A future cross-site POST protocol needs an explicit design.
	if !requiresCSRF(request.Method) {
		return true
	}
	if origins, present := request.Header["Origin"]; present {
		if len(origins) != 1 {
			return false
		}
		origin, ok := weborigin.FromHeader(origins[0])
		if !ok || p.origin == "" || origin != p.origin {
			return false
		}
	}
	if sites, present := request.Header["Sec-Fetch-Site"]; present {
		return len(sites) == 1 && (sites[0] == "same-origin" || sites[0] == "none")
	}
	// Native clients omit browser headers. JSON decoding separately rejects
	// simple form/text media types, including requests with no Content-Type.
	return true
}

func requireJSONMediaType(request *http.Request) error {
	values := request.Header.Values("Content-Type")
	if len(values) != 1 {
		return errors.New("one application/json Content-Type is required")
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return errors.New("application/json Content-Type is required")
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return errors.New("JSON supports only an optional UTF-8 charset")
		}
	}
	return nil
}
