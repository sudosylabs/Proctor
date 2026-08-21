// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type localizationContextKey struct{}

type requestLocalization struct {
	localizer Localizer
	locale    string
}

func withRequestLocalization(ctx context.Context, localizer Localizer, locale string) context.Context {
	return context.WithValue(ctx, localizationContextKey{}, requestLocalization{localizer: localizer, locale: locale})
}

func preferredLocale(request *http.Request, supported []string) string {
	if request == nil || len(supported) == 0 {
		return ""
	}
	type preference struct {
		locale  string
		quality float64
		order   int
	}
	preferences := make([]preference, 0, 8)
	for order, raw := range strings.Split(request.Header.Get("Accept-Language"), ",") {
		parts := strings.Split(strings.TrimSpace(raw), ";")
		locale := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(parts[0]), "_", "-"))
		if locale == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(name, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > 0 {
			preferences = append(preferences, preference{locale: locale, quality: quality, order: order})
		}
	}
	bestLocale, bestQuality, bestSpecificity, bestOrder := "", -1.0, -1, len(preferences)+1
	for _, candidate := range supported {
		normalized := strings.ToLower(strings.ReplaceAll(candidate, "_", "-"))
		for _, requested := range preferences {
			specificity := 0
			switch {
			case requested.locale == normalized:
				specificity = 3
			case strings.HasPrefix(requested.locale, normalized+"-") || strings.HasPrefix(normalized, requested.locale+"-"):
				specificity = 2
			case requested.locale == "*":
				specificity = 1
			}
			better := requested.quality > bestQuality ||
				requested.quality == bestQuality && specificity > bestSpecificity ||
				requested.quality == bestQuality && specificity == bestSpecificity && requested.order < bestOrder
			if specificity > 0 && better {
				bestLocale, bestQuality, bestSpecificity, bestOrder = candidate, requested.quality, specificity, requested.order
			}
		}
	}
	return bestLocale
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
	if problem.Title == "" {
		problem.Title = http.StatusText(problem.Status)
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
	title, detail := problemPresentation(status)
	if request == nil {
		return title, detail
	}
	localization, ok := request.Context().Value(localizationContextKey{}).(requestLocalization)
	if !ok || localization.localizer == nil {
		return title, detail
	}
	name := problemPresentationName(status)
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

func problemPresentation(status int) (string, string) {
	switch status {
	case http.StatusBadRequest:
		return "Invalid request", "The request is invalid."
	case http.StatusUnauthorized:
		return "Authentication required", "Authentication is required."
	case http.StatusForbidden:
		return "Access denied", "You are not allowed to perform this action."
	case http.StatusNotFound:
		return "Resource not found", "The requested resource was not found."
	case http.StatusConflict:
		return "Conflict", "The request conflicts with the current state."
	case http.StatusTooManyRequests:
		return "Too many requests", "Please retry later."
	case http.StatusServiceUnavailable:
		return "Service unavailable", "The service is temporarily unavailable."
	default:
		if status >= 400 && status < 500 {
			if title := http.StatusText(status); title != "" {
				return title, "The request could not be completed."
			}
		}
		return "Internal server error", "An unexpected error occurred."
	}
}
