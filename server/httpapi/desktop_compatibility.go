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
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type unavailableDesktopCompatibilityApplication struct{}

func (unavailableDesktopCompatibilityApplication) EvaluateDesktopCompatibility(
	context.Context,
	application.DesktopCompatibilityQuery,
) (application.DesktopCompatibilityResult, error) {
	return application.DesktopCompatibilityResult{}, application.NewError(
		"desktop_compatibility_policy.unavailable",
	)
}

func (unavailableDesktopCompatibilityApplication) GetDesktopCompatibilityPolicy(
	context.Context,
	application.Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	return nil, application.NewError("desktop_compatibility_policy.unavailable")
}

func (unavailableDesktopCompatibilityApplication) ReplaceDesktopCompatibilityPolicy(
	context.Context,
	application.Invocation,
	application.ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	return nil, application.NewError("desktop_compatibility_policy.unavailable")
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
	RetryAt                *string  `json:"retry_at"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              string   `json:"updated_at"`
}

type desktopCompatibilityPolicyResourceModule struct {
	application DesktopCompatibilityApplication
}

func desktopCompatibilityPolicyResource(application DesktopCompatibilityApplication) resource {
	module := desktopCompatibilityPolicyResourceModule{application: application}
	readErrors := operatorReadErrorCodes("desktop_compatibility_policy.unavailable")
	mutationErrors := operatorMutationErrorCodes(
		"authentication.strong_required",
		"authentication.reauthentication_required",
		"idempotency.key_required",
		"idempotency.invalid_key",
		"idempotency.conflict",
		"idempotency.in_progress",
		"request.invalid",
		"desktop_compatibility_policy.invalid",
		"desktop_compatibility_policy.revision_conflict",
		"desktop_compatibility_policy.unavailable",
	)
	replace := strongRecentSessionRoute(
		http.MethodPut,
		apiPath(literal("desktop-compatibility-policy")),
		mutationErrors,
		module.replace,
	)
	replace.idempotency = IdempotencyRequired
	return newResource(
		"desktop-compatibility-policy",
		principalRoute(
			http.MethodGet,
			apiPath(literal("desktop-compatibility-policy")),
			readErrors,
			module.get,
		),
		replace,
	)
}

func (module desktopCompatibilityPolicyResourceModule) get(
	request operationRequest,
) (operationResult, error) {
	policy, err := module.application.GetDesktopCompatibilityPolicy(
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

func (module desktopCompatibilityPolicyResourceModule) replace(
	request operationRequest,
) (operationResult, error) {
	var body desktopCompatibilityPolicyRequest
	if err := request.decodeJSON(&body, "replaceDesktopCompatibilityPolicy"); err != nil {
		return operationResult{}, err
	}
	settings, err := requiredDesktopCompatibilityPolicySettings(body)
	if err != nil {
		return operationResult{}, err
	}
	policy, err := module.application.ReplaceDesktopCompatibilityPolicy(
		request.context,
		request.invocation(),
		application.ReplaceDesktopCompatibilityPolicyCommand{
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
		return model.DesktopCompatibilityPolicySettings{}, application.NewError(
			"request.invalid",
		).WithField("field", "minimum_desktop_release")
	}
	revokedBuildIDs := body.RevokedDesktopBuildIDs.ValuePointer()
	if revokedBuildIDs == nil {
		return model.DesktopCompatibilityPolicySettings{}, application.NewError(
			"request.invalid",
		).WithField("field", "revoked_desktop_build_ids")
	}
	message := body.AdministratorMessage.ValuePointer()
	if message == nil {
		return model.DesktopCompatibilityPolicySettings{}, application.NewError(
			"request.invalid",
		).WithField("field", "administrator_message")
	}
	availability := body.Availability.ValuePointer()
	if availability == nil {
		return model.DesktopCompatibilityPolicySettings{}, application.NewError(
			"request.invalid",
		).WithField("field", "availability")
	}
	if !body.RetryAt.IsSet() {
		return model.DesktopCompatibilityPolicySettings{}, application.NewError(
			"request.invalid",
		).WithField("field", "retry_at")
	}
	var retryAt model.OptionalTime
	if value := body.RetryAt.ValuePointer(); value != nil {
		parsed, parseErr := time.Parse(time.RFC3339Nano, *value)
		if parseErr != nil {
			return model.DesktopCompatibilityPolicySettings{}, application.NewError(
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
		value := policy.RetryAt.Time.UTC().Format(time.RFC3339Nano)
		response.RetryAt = &value
	}
	response.CreatedAt = model.TimeUTC(policy.CreatedAt).Format(time.RFC3339Nano)
	response.UpdatedAt = model.TimeUTC(policy.UpdatedAt).Format(time.RFC3339Nano)
	return response
}
