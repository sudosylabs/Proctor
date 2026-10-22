// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const userSettingsReplacementBodyMaxBytes = model.UserSettingsSourceMaxBytes*6 + 4096

type userSettingsResponse struct {
	Source        string `json:"source"`
	FormatVersion int    `json:"format_version"`
	Revision      string `json:"revision"`
	Writable      bool   `json:"writable"`
	UpdatedAt     string `json:"updated_at"`
}

type userSettingsReplaceRequest struct {
	Source           string `json:"source"`
	FormatVersion    int    `json:"format_version"`
	ExpectedRevision string `json:"expected_revision"`
}

type userSettingsReplacementResponse struct {
	Revision      string `json:"revision"`
	FormatVersion int    `json:"format_version"`
	UpdatedAt     string `json:"updated_at"`
	Changed       bool   `json:"changed"`
}

type userSettingsResourceModule struct {
	settings UserSettingsApplication
}

func userSettingsResource(settings UserSettingsApplication) apiResource {
	module := userSettingsResourceModule{settings: settings}
	path := apiPath(literal("users"), literal("me"), literal("settings"))
	replace := sessionRoute(
		http.MethodPut,
		path,
		userSettingsSessionMutationCodes(
			"request.invalid", "user_settings.invalid", "user_settings.format_unsupported",
			"user_settings.revision_conflict", "user_settings.unavailable", "audit.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
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
	return append([]string(nil), extra...)
}

func userSettingsSessionMutationCodes(extra ...string) []string {
	return userSettingsSessionCodes(extra...)
}

func (m userSettingsResourceModule) read(request operationRequest) (operationResult, error) {
	view, err := m.settings.ReadOwnUserSettings(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userSettingsResponse{
		Source:        view.Source,
		FormatVersion: view.FormatVersion,
		Revision:      view.Revision.String(),
		Writable:      view.Writable,
		UpdatedAt:     model.FormatRFC3339(view.UpdatedAt),
	}).withHeaders(http.Header{"Cache-Control": []string{"private, no-store"}}), nil
}

func (m userSettingsResourceModule) replace(request operationRequest) (operationResult, error) {
	var input userSettingsReplaceRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	expectedRevision, err := model.ParseUserSettingsRevision(input.ExpectedRevision)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	result, err := m.settings.ReplaceOwnUserSettings(
		request.context,
		request.invocation(),
		app.ReplaceOwnUserSettingsCommand{
			Source: input.Source, FormatVersion: input.FormatVersion,
			ExpectedRevision: expectedRevision, IdempotencyKey: request.idempotencyKey,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userSettingsReplacementResponse{
		Revision: result.Revision.String(), FormatVersion: result.FormatVersion,
		UpdatedAt: model.FormatRFC3339(result.UpdatedAt), Changed: result.Changed,
	}).withHeaders(http.Header{"Cache-Control": []string{"private, no-store"}}), nil
}
