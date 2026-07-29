// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "net/http"

func (a *API) InitSystem() error {
	if err := a.Register(
		Route{Method: http.MethodGet, Path: "/health/live", Auth: AuthPublic},
		http.HandlerFunc(a.getLiveness),
	); err != nil {
		return err
	}
	if err := a.Register(
		Route{Method: http.MethodGet, Path: "/health/ready", Auth: AuthPublic},
		http.HandlerFunc(a.getReadiness),
	); err != nil {
		return err
	}
	return a.Register(
		Route{Method: http.MethodGet, Path: "/api/v1/system/version", Auth: AuthPublic},
		http.HandlerFunc(a.getVersion),
	)
}

func (a *API) getLiveness(writer http.ResponseWriter, request *http.Request) {
	if !a.health.Live() {
		WriteProblem(writer, Problem{
			Type:      "https://proctor.sudosylabs.com/problems/not-live",
			Title:     "Service unavailable",
			Status:    http.StatusServiceUnavailable,
			Detail:    "The process is not healthy.",
			Instance:  request.URL.Path,
			Code:      "not_live",
			RequestID: RequestID(request.Context()),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) getReadiness(writer http.ResponseWriter, request *http.Request) {
	if !a.health.Ready() {
		WriteProblem(writer, Problem{
			Type:      "https://proctor.sudosylabs.com/problems/not-ready",
			Title:     "Service unavailable",
			Status:    http.StatusServiceUnavailable,
			Detail:    "The service is not ready to accept requests.",
			Instance:  request.URL.Path,
			Code:      "not_ready",
			RequestID: RequestID(request.Context()),
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) getVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, a.buildInfo)
}
