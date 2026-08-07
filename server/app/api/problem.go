// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

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
	return Problem{
		Type:      "https://proctor.sudosylabs.com/problems/internal",
		Title:     "Internal server error",
		Status:    http.StatusInternalServerError,
		Detail:    "An unexpected error occurred.",
		Instance:  request.URL.Path,
		Code:      "internal",
		RequestID: RequestID(request.Context()),
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
