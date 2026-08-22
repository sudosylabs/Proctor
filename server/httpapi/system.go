// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
}

type systemResourceModule struct {
	health    Health
	buildInfo BuildInfo
}

func systemResource(health Health, buildInfo BuildInfo) resource {
	module := systemResourceModule{health: health, buildInfo: buildInfo}
	return newResource(
		"system",
		publicRoute(http.MethodGet, rootPath(literal("health"), literal("live")), []string{"not_live"}, module.liveness),
		publicRoute(http.MethodGet, rootPath(literal("health"), literal("ready")), []string{"not_ready"}, module.readiness),
		publicRoute(http.MethodGet, apiPath(literal("system"), literal("version")), nil, module.version),
	)
}

func (module systemResourceModule) liveness(request operationRequest) (operationResult, error) {
	if !module.health.Live() {
		title, detail := localizedNamedProblemPresentation(request.request, "not_live")
		return problemResult(Problem{
			Type:      "https://proctor.sudosylabs.com/problems/not-live",
			Title:     title,
			Status:    http.StatusServiceUnavailable,
			Detail:    detail,
			Instance:  request.request.URL.Path,
			Code:      "not_live",
			RequestID: RequestID(request.context),
		}), nil
	}
	return jsonResult(http.StatusOK, healthResponse{Status: "ok"}), nil
}

func (module systemResourceModule) readiness(request operationRequest) (operationResult, error) {
	if !module.health.Ready() {
		title, detail := localizedNamedProblemPresentation(request.request, "not_ready")
		return problemResult(Problem{
			Type:      "https://proctor.sudosylabs.com/problems/not-ready",
			Title:     title,
			Status:    http.StatusServiceUnavailable,
			Detail:    detail,
			Instance:  request.request.URL.Path,
			Code:      "not_ready",
			RequestID: RequestID(request.context),
		}), nil
	}
	return jsonResult(http.StatusOK, healthResponse{Status: "ok"}), nil
}

func (module systemResourceModule) version(operationRequest) (operationResult, error) {
	return jsonResult(http.StatusOK, module.buildInfo), nil
}
