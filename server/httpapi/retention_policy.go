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
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

// RetentionPolicyApplication is the transport's complete policy capability.
type RetentionPolicyApplication interface {
	GetRetentionPolicy(context.Context, application.Invocation) (*model.RetentionPolicy, error)
	ReplaceRetentionPolicy(context.Context, application.Invocation, application.ReplaceRetentionPolicyCommand) (*model.RetentionPolicy, error)
}

type retentionPolicyRequest struct {
	ExpectedRevision                 int64          `json:"expected_revision"`
	SubmissionRetentionDays          Optional[int]  `json:"submission_retention_days"`
	IntegrityRetentionDays           Optional[int]  `json:"integrity_retention_days"`
	BrowserActivityRetentionDays     Optional[int]  `json:"browser_activity_retention_days"`
	SecurityOperationalRetentionDays Optional[int]  `json:"security_operational_retention_days"`
	AuditRetentionDays               Optional[int]  `json:"audit_retention_days"`
	ExportRetentionDays              Optional[int]  `json:"export_retention_days"`
	DeletionGraceDays                Optional[int]  `json:"deletion_grace_days"`
	CandidateNotices                 Optional[bool] `json:"candidate_notices"`
}

type retentionPolicyResponse struct {
	Revision                         int64  `json:"revision"`
	SubmissionRetentionDays          int    `json:"submission_retention_days"`
	IntegrityRetentionDays           int    `json:"integrity_retention_days"`
	BrowserActivityRetentionDays     int    `json:"browser_activity_retention_days"`
	SecurityOperationalRetentionDays int    `json:"security_operational_retention_days"`
	AuditRetentionDays               int    `json:"audit_retention_days"`
	ExportRetentionDays              int    `json:"export_retention_days"`
	DeletionGraceDays                int    `json:"deletion_grace_days"`
	CandidateNotices                 bool   `json:"candidate_notices"`
	AutomaticDeletionEnabled         bool   `json:"automatic_deletion_enabled"`
	CreatedAt                        string `json:"created_at"`
	UpdatedAt                        string `json:"updated_at"`
}

type retentionPolicyResourceModule struct {
	application RetentionPolicyApplication
}

func retentionPolicyResource(application RetentionPolicyApplication) resource {
	module := retentionPolicyResourceModule{application: application}
	readErrors := operatorReadErrorCodes("retention_policy.unavailable")
	mutationErrors := operatorMutationErrorCodes(
		"authentication.strong_required",
		"authentication.reauthentication_required",
		"idempotency.key_required",
		"idempotency.invalid_key",
		"idempotency.conflict",
		"idempotency.in_progress",
		"request.invalid",
		"retention_policy.invalid",
		"retention_policy.revision_conflict",
		"retention_policy.unavailable",
	)
	replace := strongRecentSessionRoute(
		http.MethodPut,
		apiPath(literal("retention-policy")),
		mutationErrors,
		module.replace,
	)
	replace.idempotency = IdempotencyRequired
	return newResource(
		"retention-policy",
		principalRoute(
			http.MethodGet,
			apiPath(literal("retention-policy")),
			readErrors,
			module.get,
		),
		replace,
	)
}

func (module retentionPolicyResourceModule) get(
	request operationRequest,
) (operationResult, error) {
	policy, err := module.application.GetRetentionPolicy(
		request.context,
		request.invocation(),
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(
		http.StatusOK,
		retentionPolicyResponseFromModel(policy),
	).withHeaders(privateNoStoreHeaders()), nil
}

func (module retentionPolicyResourceModule) replace(
	request operationRequest,
) (operationResult, error) {
	var body retentionPolicyRequest
	if err := request.decodeJSON(&body, "replaceRetentionPolicy"); err != nil {
		return operationResult{}, err
	}
	settings, err := requiredRetentionPolicySettings(body)
	if err != nil {
		return operationResult{}, err
	}
	policy, err := module.application.ReplaceRetentionPolicy(
		request.context,
		request.invocation(),
		application.ReplaceRetentionPolicyCommand{
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
		retentionPolicyResponseFromModel(policy),
	).withHeaders(privateNoStoreHeaders()), nil
}

func requiredRetentionPolicySettings(body retentionPolicyRequest) (model.RetentionPolicySettings, error) {
	settings := model.RetentionPolicySettings{}
	if body.CandidateNotices.IsNull() {
		return settings, application.NewError("request.invalid").WithField("field", "candidate_notices")
	}
	if value := body.CandidateNotices.ValuePointer(); value != nil {
		settings.CandidateNotices = *value
	}
	fields := []struct {
		name        string
		source      Optional[int]
		destination *int
	}{
		{"submission_retention_days", body.SubmissionRetentionDays, &settings.SubmissionRetentionDays},
		{"integrity_retention_days", body.IntegrityRetentionDays, &settings.IntegrityRetentionDays},
		{"browser_activity_retention_days", body.BrowserActivityRetentionDays, &settings.BrowserActivityRetentionDays},
		{"security_operational_retention_days", body.SecurityOperationalRetentionDays, &settings.SecurityOperationalRetentionDays},
		{"audit_retention_days", body.AuditRetentionDays, &settings.AuditRetentionDays},
		{"export_retention_days", body.ExportRetentionDays, &settings.ExportRetentionDays},
		{"deletion_grace_days", body.DeletionGraceDays, &settings.DeletionGraceDays},
	}
	for _, field := range fields {
		value := field.source.ValuePointer()
		if value == nil {
			return model.RetentionPolicySettings{}, application.NewError("request.invalid").WithField("field", field.name)
		}
		*field.destination = *value
	}
	return settings, nil
}

func retentionPolicyResponseFromModel(policy *model.RetentionPolicy) retentionPolicyResponse {
	if policy == nil {
		return retentionPolicyResponse{}
	}
	return retentionPolicyResponse{
		Revision:                policy.Revision,
		SubmissionRetentionDays: policy.SubmissionRetentionDays, IntegrityRetentionDays: policy.IntegrityRetentionDays, BrowserActivityRetentionDays: policy.BrowserActivityRetentionDays, SecurityOperationalRetentionDays: policy.SecurityOperationalRetentionDays,
		AuditRetentionDays: policy.AuditRetentionDays, ExportRetentionDays: policy.ExportRetentionDays,
		DeletionGraceDays:        policy.DeletionGraceDays,
		CandidateNotices:         policy.CandidateNotices,
		AutomaticDeletionEnabled: policy.AutomaticDeletionEnabled,
		CreatedAt:                model.TimeUTC(policy.CreatedAt).Format(time.RFC3339Nano),
		UpdatedAt:                model.TimeUTC(policy.UpdatedAt).Format(time.RFC3339Nano),
	}
}
