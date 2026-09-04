// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type localizationContextKey struct{}

type requestLocalization struct {
	localizer Localizer
	locale    string
}

func withRequestLocalization(ctx context.Context, localizer Localizer, locale string) context.Context {
	return context.WithValue(ctx, localizationContextKey{}, requestLocalization{localizer: localizer, locale: locale})
}

type Problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Instance  string            `json:"instance,omitempty"`
	Code      string            `json:"code"`
	RequestID string            `json:"request_id,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func WriteProblem(writer http.ResponseWriter, problem Problem) {
	if problem.Type == "" {
		problem.Type = "about:blank"
	}
	if problem.Status == 0 {
		problem.Status = http.StatusInternalServerError
	}
	if problem.Code == "" {
		problem.Code = "internal"
	}

	writer.Header().Set("Content-Type", "application/problem+json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(problem.Status)
	_ = json.NewEncoder(writer).Encode(problem)
}

// WriteError maps a failure to RFC 9457 Problem Details. Transport-neutral
// application failures (*app.Error) use the centralized code table through the
// applicationFailure interface. Unexpected errors become a generic correlated
// internal response with no internal detail.
func WriteError(writer http.ResponseWriter, request *http.Request, err error) {
	var failure applicationFailure
	if errors.As(err, &failure) {
		WriteProblem(writer, problemFromApplicationFailure(request, failure))
		return
	}
	WriteProblem(writer, internalProblem(request))
}

func internalProblem(request *http.Request) Problem {
	title, detail := localizedProblemPresentation(request, http.StatusInternalServerError)
	return Problem{
		Type:      "https://proctor.sudosylabs.com/problems/internal",
		Title:     title,
		Status:    http.StatusInternalServerError,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      "internal",
		RequestID: RequestID(request.Context()),
	}
}

func localizedProblemPresentation(request *http.Request, status int) (string, string) {
	return localizedNamedProblemPresentation(request, problemPresentationName(status))
}

func localizedNamedProblemPresentation(request *http.Request, name string) (string, string) {
	if request == nil {
		return "", ""
	}
	localization, ok := request.Context().Value(localizationContextKey{}).(requestLocalization)
	if !ok || localization.localizer == nil {
		return "", ""
	}
	title, detail := "", ""
	if translated, err := localization.localizer.Translate(localization.locale, "problem."+name+".title", nil); err == nil {
		title = translated
	}
	if translated, err := localization.localizer.Translate(localization.locale, "problem."+name+".detail", nil); err == nil {
		detail = translated
	}
	return title, detail
}

func problemPresentationName(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		if status >= 400 && status < 500 {
			return "client_error"
		}
		return "internal"
	}
}
