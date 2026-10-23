// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type unavailableDesktopCompatibilityApplication struct{}

func (unavailableDesktopCompatibilityApplication) EvaluateDesktopCompatibility(
	context.Context,
	invocation.Invocation,
	app.DesktopCompatibilityQuery,
) (app.DesktopCompatibilityResult, error) {
	return app.DesktopCompatibilityResult{}, errUnpresentableResult
}

func (unavailableDesktopCompatibilityApplication) GetDesktopCompatibilityPolicy(
	context.Context,
	invocation.Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	return nil, errUnpresentableResult
}

func (unavailableDesktopCompatibilityApplication) ReplaceDesktopCompatibilityPolicy(
	context.Context,
	invocation.Invocation,
	app.ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	return nil, errUnpresentableResult
}

type desktopCompatibilityPolicyRequest struct {
	ExpectedRevision       int64              `json:"expected_revision"`
	MinimumDesktopRelease  Optional[string]   `json:"minimum_desktop_release"`
	RevokedDesktopBuildIDs Optional[[]string] `json:"revoked_desktop_build_ids"`
	AdministratorMessage   Optional[string]   `json:"administrator_message"`
	Availability           Optional[string]   `json:"availability"`
	RetryAt                Optional[string]   `json:"retry_at"`
}

type desktopCompatibilityPolicyResponse struct {
	Revision               int64    `json:"revision"`
	MinimumDesktopRelease  string   `json:"minimum_desktop_release"`
	RevokedDesktopBuildIDs []string `json:"revoked_desktop_build_ids"`
	AdministratorMessage   string   `json:"administrator_message"`
	Availability           string   `json:"availability"`
	RetryAt                *string  `json:"retry_at,omitempty"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              string   `json:"updated_at"`
}

type desktopCompatibilityPolicyResourceModule struct {
	application DesktopCompatibilityApplication
}

func desktopCompatibilityPolicyResource(application DesktopCompatibilityApplication) apiResource {
	module := desktopCompatibilityPolicyResourceModule{application: application}
	replace := strongRecentSessionRoute(
		http.MethodPut,
		apiPath(literal("desktop-compatibility-policy")),
		[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "request.invalid", "desktop_compatibility_policy.invalid", "desktop_compatibility_policy.revision_conflict", "desktop_compatibility_policy.unavailable", "resource.not_found"},
		module.replace,
	)
	replace.idempotency = IdempotencyRequired
	return newResource(
		"desktop-compatibility-policy",
		principalRoute(
			http.MethodGet,
			apiPath(literal("desktop-compatibility-policy")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "desktop_compatibility_policy.unavailable", "desktop_compatibility_policy.revision_conflict", "resource.not_found"},
			module.get,
		),
		replace,
	)
}

func (m desktopCompatibilityPolicyResourceModule) get(
	request operationRequest,
) (operationResult, error) {
	policy, err := m.application.GetDesktopCompatibilityPolicy(
		request.context,
		request.invocation(),
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(
		http.StatusOK,
		desktopCompatibilityPolicyResponseFromModel(policy),
	).withHeaders(privateNoStoreHeaders()), nil
}

func (m desktopCompatibilityPolicyResourceModule) replace(
	request operationRequest,
) (operationResult, error) {
	var body desktopCompatibilityPolicyRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	settings, err := requiredDesktopCompatibilityPolicySettings(body)
	if err != nil {
		return operationResult{}, err
	}
	policy, err := m.application.ReplaceDesktopCompatibilityPolicy(
		request.context,
		request.invocation(),
		app.ReplaceDesktopCompatibilityPolicyCommand{
			ExpectedRevision: body.ExpectedRevision,
			Settings:         settings,
			IdempotencyKey:   request.idempotencyKey,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(
		http.StatusOK,
		desktopCompatibilityPolicyResponseFromModel(policy),
	).withHeaders(privateNoStoreHeaders()), nil
}

func requiredDesktopCompatibilityPolicySettings(
	body desktopCompatibilityPolicyRequest,
) (model.DesktopCompatibilityPolicySettings, error) {
	minimumRelease := body.MinimumDesktopRelease.ValuePointer()
	if minimumRelease == nil {
		return model.DesktopCompatibilityPolicySettings{}, apperror.New(
			"request.invalid",
		).WithField("field", "minimum_desktop_release")
	}
	revokedBuildIDs := body.RevokedDesktopBuildIDs.ValuePointer()
	if revokedBuildIDs == nil {
		return model.DesktopCompatibilityPolicySettings{}, apperror.New(
			"request.invalid",
		).WithField("field", "revoked_desktop_build_ids")
	}
	message := body.AdministratorMessage.ValuePointer()
	if message == nil {
		return model.DesktopCompatibilityPolicySettings{}, apperror.New(
			"request.invalid",
		).WithField("field", "administrator_message")
	}
	availability := body.Availability.ValuePointer()
	if availability == nil {
		return model.DesktopCompatibilityPolicySettings{}, apperror.New(
			"request.invalid",
		).WithField("field", "availability")
	}
	if body.RetryAt.IsNull() {
		return model.DesktopCompatibilityPolicySettings{}, apperror.New(
			"request.invalid",
		).WithField("field", "retry_at")
	}
	var retryAt model.OptionalTime
	if value := body.RetryAt.ValuePointer(); value != nil {
		parsed, parseErr := model.ParseRFC3339(*value)
		if parseErr != nil {
			return model.DesktopCompatibilityPolicySettings{}, apperror.New(
				"request.invalid",
			).WithField("field", "retry_at").Wrap(parseErr)
		}
		retryAt = model.OptionalTimeFrom(parsed)
	}
	canonicalBuildIDs := slices.Clone(*revokedBuildIDs)
	slices.Sort(canonicalBuildIDs)
	return model.DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  *minimumRelease,
		RevokedDesktopBuildIDs: canonicalBuildIDs,
		AdministratorMessage:   strings.TrimSpace(*message),
		Availability:           model.DesktopAvailability(*availability),
		RetryAt:                retryAt,
	}, nil
}

func desktopCompatibilityPolicyResponseFromModel(
	policy *model.DesktopCompatibilityPolicy,
) desktopCompatibilityPolicyResponse {
	response := desktopCompatibilityPolicyResponse{
		RevokedDesktopBuildIDs: []string{},
	}
	if policy == nil {
		return response
	}
	response.Revision = policy.Revision
	response.MinimumDesktopRelease = policy.MinimumDesktopRelease
	response.RevokedDesktopBuildIDs = slices.Clone(policy.RevokedDesktopBuildIDs)
	response.AdministratorMessage = policy.AdministratorMessage
	response.Availability = string(policy.Availability)
	if policy.RetryAt.Valid {
		value := model.FormatRFC3339(policy.RetryAt.Time)
		response.RetryAt = &value
	}
	response.CreatedAt = model.FormatRFC3339(policy.CreatedAt)
	response.UpdatedAt = model.FormatRFC3339(policy.UpdatedAt)
	return response
}
