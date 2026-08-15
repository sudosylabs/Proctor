// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const userSettingsReplacementBodyMaxBytes = model.UserSettingsSourceMaxBytes*6 + 4096

type userSettingsResponse struct {
	Source        string `json:"source"`
	FormatVersion int    `json:"format_version"`
	Revision      string `json:"revision"`
	Writable      bool   `json:"writable"`
	UpdatedAt     int64  `json:"updated_at"`
}

type userSettingsReplaceRequest struct {
	Source           string `json:"source"`
	FormatVersion    int    `json:"format_version"`
	ExpectedRevision string `json:"expected_revision"`
}

type userSettingsReplacementResponse struct {
	Revision      string `json:"revision"`
	FormatVersion int    `json:"format_version"`
	UpdatedAt     int64  `json:"updated_at"`
	Changed       bool   `json:"changed"`
}

type userSettingsResourceModule struct {
	settings UserSettingsApplication
}

func userSettingsResource(settings UserSettingsApplication) resource {
	module := userSettingsResourceModule{settings: settings}
	path := apiPath(literal("users"), literal("me"), literal("settings"))
	replace := sessionRoute(
		http.MethodPut,
		path,
		userSettingsSessionMutationCodes(
			"request.invalid", "user_settings.invalid", "user_settings.format_unsupported",
			"user_settings.revision_conflict", "user_settings.unavailable", "audit.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		),
		module.replace,
	)
	replace.idempotency = IdempotencyRequired
	replace.maxBodyBytes = userSettingsReplacementBodyMaxBytes
	return newResource(
		"user-settings",
		sessionRoute(http.MethodGet, path, userSettingsSessionCodes("user_settings.unavailable"), module.read),
		replace,
	)
}

func userSettingsSessionCodes(extra ...string) []string {
	return append([]string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
	}, extra...)
}

func userSettingsSessionMutationCodes(extra ...string) []string {
	return append(userSettingsSessionCodes("authentication.csrf.invalid"), extra...)
}

func (module userSettingsResourceModule) read(request operationRequest) (operationResult, error) {
	view, err := module.settings.ReadOwnUserSettings(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userSettingsResponse{
		Source:        view.Source,
		FormatVersion: view.FormatVersion,
		Revision:      view.Revision.String(),
		Writable:      view.Writable,
		UpdatedAt:     view.UpdatedAt.UnixMilli(),
	}).withHeaders(http.Header{"Cache-Control": []string{"private, no-store"}}), nil
}

func (module userSettingsResourceModule) replace(request operationRequest) (operationResult, error) {
	var input userSettingsReplaceRequest
	if err := request.decodeJSON(&input, "user_settings"); err != nil {
		return operationResult{}, err
	}
	expectedRevision, err := model.ParseUserSettingsRevision(input.ExpectedRevision)
	if err != nil {
		return operationResult{}, invalidRequestError("expected_revision", err)
	}
	result, err := module.settings.ReplaceOwnUserSettings(
		request.context,
		request.invocation(),
		application.ReplaceOwnUserSettingsCommand{
			Source: input.Source, FormatVersion: input.FormatVersion,
			ExpectedRevision: expectedRevision, IdempotencyKey: request.idempotencyKey,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userSettingsReplacementResponse{
		Revision: result.Revision.String(), FormatVersion: result.FormatVersion,
		UpdatedAt: result.UpdatedAt.UnixMilli(), Changed: result.Changed,
	}).withHeaders(http.Header{"Cache-Control": []string{"private, no-store"}}), nil
}
