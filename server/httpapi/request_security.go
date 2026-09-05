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
	"net/url"
	"strings"
)

// browserRequestPolicy protects unsafe requests, including public operations
// that create cookies before a Session or double-submit CSRF proof exists.
// Its authority is the configured public origin, never Host or proxy headers.
type browserRequestPolicy struct {
	origin string
}

func newBrowserRequestPolicy(publicURL string) (browserRequestPolicy, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return browserRequestPolicy{}, errors.New("public URL must have an HTTP or HTTPS origin")
	}
	return browserRequestPolicy{origin: serializedOrigin(parsed)}, nil
}

func serializedOrigin(parsed *url.URL) string {
	host := strings.ToLower(parsed.Host)
	if parsed.Scheme == "https" && parsed.Port() == "443" || parsed.Scheme == "http" && parsed.Port() == "80" {
		host = strings.TrimSuffix(host, ":"+parsed.Port())
	}
	return parsed.Scheme + "://" + host
}

func (policy browserRequestPolicy) allows(request *http.Request) bool {
	// Existing provider callbacks use GET and retain their own one-use state and
	// browser binding. A future cross-site POST protocol needs an explicit design.
	if !requiresCSRF(request.Method) {
		return true
	}
	if origins, present := request.Header["Origin"]; present {
		if len(origins) != 1 || origins[0] == "" || strings.TrimSpace(origins[0]) != origins[0] {
			return false
		}
		origin, err := url.Parse(origins[0])
		if err != nil || origin.User != nil || origin.Opaque != "" || origin.Path != "" ||
			origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" ||
			policy.origin == "" || serializedOrigin(origin) != policy.origin {
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
