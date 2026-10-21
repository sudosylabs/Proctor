// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type healthResponse struct {
	Status string `json:"status"`
}

type systemResourceModule struct {
	health               Health
	buildInfo            BuildInfo
	desktopCompatibility DesktopCompatibilityApplication
	now                  func() time.Time
}

func systemResource(health Health, buildInfo BuildInfo) resource {
	return systemResourceWithApplication(
		health,
		buildInfo,
		unavailableDesktopCompatibilityApplication{},
		time.Now,
	)
}

func systemResourceWithApplication(
	health Health,
	buildInfo BuildInfo,
	desktopCompatibility DesktopCompatibilityApplication,
	now func() time.Time,
) resource {
	module := systemResourceModule{
		health:               health,
		buildInfo:            buildInfo,
		desktopCompatibility: desktopCompatibility,
		now:                  now,
	}
	return newResource(
		"system",
		publicRoute(http.MethodGet, rootPath(literal("health"), literal("live")), []string{healthNotLiveCode}, module.liveness),
		publicRoute(http.MethodGet, rootPath(literal("health"), literal("ready")), []string{healthNotReadyCode}, module.readiness),
		publicRoute(http.MethodGet, apiPath(literal("system"), literal("ping")), []string{"request.invalid"}, module.ping),
		publicRoute(http.MethodGet, apiPath(literal("system"), literal("version")), nil, module.version),
	)
}

type systemPingResponse struct {
	SchemaVersion           int    `json:"schema_version"`
	ServerTime              string `json:"server_time"`
	Availability            string `json:"availability"`
	Reason                  string `json:"reason"`
	AdministratorMessage    string `json:"administrator_message,omitempty"`
	RetryAt                 string `json:"retry_at,omitempty"`
	Compatibility           string `json:"compatibility"`
	CompatibilityReason     string `json:"compatibility_reason"`
	MinimumDesktopRelease   string `json:"minimum_desktop_release,omitempty"`
	MaximumDesktopRelease   string `json:"maximum_desktop_release,omitempty"`
	MinimumRealtimeProtocol int    `json:"minimum_realtime_protocol,omitempty"`
	MaximumRealtimeProtocol int    `json:"maximum_realtime_protocol,omitempty"`
}

func (m systemResourceModule) liveness(request operationRequest) (operationResult, error) {
	if !m.health.Live() {
		title, detail := localizedNamedProblemPresentation(request.request, "not_live")
		return problemResult(Problem{
			Type:      problemType(healthNotLiveCode),
			Title:     title,
			Status:    http.StatusServiceUnavailable,
			Detail:    detail,
			Instance:  request.request.URL.Path,
			Code:      healthNotLiveCode,
			RequestID: RequestID(request.context),
		}), nil
	}
	return jsonResult(http.StatusOK, healthResponse{Status: "ok"}), nil
}

func (m systemResourceModule) readiness(request operationRequest) (operationResult, error) {
	if !m.health.Ready() {
		title, detail := localizedNamedProblemPresentation(request.request, "not_ready")
		return problemResult(Problem{
			Type:      problemType(healthNotReadyCode),
			Title:     title,
			Status:    http.StatusServiceUnavailable,
			Detail:    detail,
			Instance:  request.request.URL.Path,
			Code:      healthNotReadyCode,
			RequestID: RequestID(request.context),
		}), nil
	}
	return jsonResult(http.StatusOK, healthResponse{Status: "ok"}), nil
}

func (m systemResourceModule) version(operationRequest) (operationResult, error) {
	return jsonResult(http.StatusOK, m.buildInfo), nil
}

func (m systemResourceModule) ping(request operationRequest) (operationResult, error) {
	query, err := desktopCompatibilityQuery(request.request)
	if err != nil {
		return operationResult{}, err
	}
	result, evaluationErr := m.desktopCompatibility.EvaluateDesktopCompatibility(
		request.context,
		query,
	)
	if apperror.Is(evaluationErr, "request.invalid") {
		return operationResult{}, evaluationErr
	}
	responseTime := model.TimeUTC(m.now())
	ready := m.health.Ready() && evaluationErr == nil
	response := systemPingResponse{
		SchemaVersion:       1,
		ServerTime:          model.FormatRFC3339(responseTime),
		Availability:        "temporarily_unavailable",
		Reason:              "temporarily_unavailable",
		Compatibility:       string(app.DesktopCompatibilityServerIncompatible),
		CompatibilityReason: "compatibility_unavailable",
	}
	if evaluationErr == nil {
		response.AdministratorMessage = result.AdministratorMessage
		response.Compatibility = string(result.Compatibility)
		response.CompatibilityReason = result.Reason
		response.MinimumDesktopRelease = result.MinimumDesktopRelease
		response.MaximumDesktopRelease = result.MaximumDesktopRelease
		response.MinimumRealtimeProtocol = result.MinimumRealtimeProtocol
		response.MaximumRealtimeProtocol = result.MaximumRealtimeProtocol
		if result.RetryAt.Valid && result.RetryAt.Time.After(responseTime) {
			response.RetryAt = model.FormatRFC3339(result.RetryAt.Time)
		}
	}
	if ready {
		response.Availability = string(result.Availability)
		response.Reason = string(result.Availability)
	}
	return jsonResult(http.StatusOK, response).withHeaders(http.Header{
		"Cache-Control": {"no-store"},
	}), nil
}

func desktopCompatibilityQuery(request *http.Request) (app.DesktopCompatibilityQuery, error) {
	values := request.URL.Query()
	allowed := map[string]int{
		"desktop_release":   64,
		"desktop_build_id":  128,
		"platform":          32,
		"architecture":      32,
		"realtime_protocol": 16,
	}
	for key, candidates := range values {
		maximum, known := allowed[key]
		if !known || len(candidates) != 1 || candidates[0] == "" || len(candidates[0]) > maximum {
			return app.DesktopCompatibilityQuery{}, apperror.New("request.invalid").WithField("field", key)
		}
	}
	selector := func(key string) (string, error) {
		candidates := values[key]
		if len(candidates) != 1 || candidates[0] == "" || len(candidates[0]) > allowed[key] {
			return "", apperror.New("request.invalid").WithField("field", key)
		}
		return candidates[0], nil
	}
	desktopRelease, err := selector("desktop_release")
	if err != nil {
		return app.DesktopCompatibilityQuery{}, err
	}
	desktopBuildID, err := selector("desktop_build_id")
	if err != nil {
		return app.DesktopCompatibilityQuery{}, err
	}
	platform, err := selector("platform")
	if err != nil {
		return app.DesktopCompatibilityQuery{}, err
	}
	architecture, err := selector("architecture")
	if err != nil {
		return app.DesktopCompatibilityQuery{}, err
	}
	realtimeText, err := selector("realtime_protocol")
	if err != nil {
		return app.DesktopCompatibilityQuery{}, err
	}
	realtimeProtocol, parseErr := strconv.Atoi(realtimeText)
	if parseErr != nil || realtimeProtocol < 1 {
		return app.DesktopCompatibilityQuery{}, apperror.New("request.invalid").WithField(
			"field",
			"realtime_protocol",
		)
	}
	return app.DesktopCompatibilityQuery{
		DesktopRelease:   desktopRelease,
		DesktopBuildID:   desktopBuildID,
		Platform:         platform,
		Architecture:     architecture,
		RealtimeProtocol: realtimeProtocol,
	}, nil
}
