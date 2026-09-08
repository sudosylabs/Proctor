// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type prepareSecurityPreflightRequest struct {
	AttemptID                        string `json:"attempt_id,omitempty"`
	NativeRegistryDigest             string `json:"native_registry_digest"`
	SourceManifestDigest             string `json:"source_manifest_digest"`
	ConfigurationManifestFingerprint string `json:"configuration_manifest_fingerprint"`
}

func (r *prepareSecurityPreflightRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONObjectMembers(data, "security preflight"); err != nil {
		return err
	}
	type wire prepareSecurityPreflightRequest
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	for name, value := range members {
		if !slices.Contains([]string{"attempt_id", "native_registry_digest", "source_manifest_digest", "configuration_manifest_fingerprint"}, name) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("invalid security preflight member")
		}
	}
	if decoded.AttemptID != "" && !model.ExamAttemptID(decoded.AttemptID).IsValid() || !model.IsValidSHA256Fingerprint(decoded.NativeRegistryDigest) || !model.IsValidSHA256Fingerprint(decoded.SourceManifestDigest) || !model.IsValidSHA256Fingerprint(decoded.ConfigurationManifestFingerprint) {
		return errors.New("invalid security preflight identity")
	}
	if _, exists := members["attempt_id"]; exists && decoded.AttemptID == "" {
		return errors.New("attempt_id must be omitted or a canonical ID")
	}
	*r = prepareSecurityPreflightRequest(decoded)
	return nil
}

type securityPolicyResponse struct {
	ServerTime                 time.Time                         `json:"server_time"`
	Policy                     model.EffectiveExamSecurityPolicy `json:"policy"`
	PolicyContentDigest        string                            `json:"policy_content_digest"`
	CapabilityMatrixDigest     string                            `json:"capability_matrix_digest"`
	CatalogBindings            []model.SecurityCatalogBinding    `json:"catalog_bindings"`
	PreflightChallenge         model.SecurityPreflightChallenge  `json:"preflight_challenge"`
	FrozenAttemptConfiguration *model.AttemptConfiguration       `json:"frozen_attempt_configuration"`
	BrowserActivityDisclosure  model.BrowserActivityDisclosure   `json:"browser_activity_disclosure"`
}

func securityPreflightErrorCodes() []string {
	return academicMutationErrorCodes("exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.desktop_incompatible", "exam.attempt.sitting_unavailable", "exam.attempt.management_authority_conflict", "exam.attempt.configuration_unsupported", "exam.attempt.unavailable", "exam.security.preflight_expired", "exam.security.preflight_superseded", "exam.security.preflight_consumed", "exam.security.preflight_rate_limited", "exam.security.policy_changed", "exam.security.session_changed", "exam.security.configuration_unsupported", "exam.security.attempt_not_ready", "exam.security.catalog_unavailable", "exam.security.unsupported_capability", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
}
func (m examAttemptHTTPModule) prepareSecurityPreflight(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamSittingId()
	if err != nil {
		return operationResult{}, err
	}
	sittingID, err := model.ParseExamSittingID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_sitting_id", err)
	}
	var body prepareSecurityPreflightRequest
	if err := request.decodeJSON(&body, "prepareSecurityPreflight"); err != nil {
		return operationResult{}, err
	}
	prepared, err := m.application.PrepareExamSecurityPreflight(request.context, request.invocation(), application.PrepareSecurityPreflightCommand{SittingID: sittingID, AttemptID: model.ExamAttemptID(body.AttemptID), NativeRegistryDigest: body.NativeRegistryDigest, SourceManifestDigest: body.SourceManifestDigest, ConfigurationManifestFingerprint: body.ConfigurationManifestFingerprint, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if prepared == nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	result := securityPolicyResponse{ServerTime: prepared.ServerTime, Policy: prepared.Resolved.Policy, PolicyContentDigest: prepared.Resolved.PolicyContentDigest, CapabilityMatrixDigest: prepared.Resolved.CapabilityMatrixDigest, CatalogBindings: prepared.Resolved.CatalogBindings, PreflightChallenge: prepared.Challenge, FrozenAttemptConfiguration: prepared.FrozenAttemptConfiguration, BrowserActivityDisclosure: prepared.BrowserActivityDisclosure}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > model.SecurityPolicyResponseMaxBytes {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, result), nil
}
func (m examAttemptHTTPModule) reportSecurityPreflight(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireSecurityPreflightID()
	if err != nil {
		return operationResult{}, err
	}
	var report model.SecurityPreflightReport
	if err := request.decodeJSON(&report, "reportSecurityPreflight"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.ReportExamSecurityPreflight(request.context, request.invocation(), application.ReportSecurityPreflightCommand{PreflightID: id, Report: report, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if result == nil {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, result), nil
}

func securityPreflightResource(application ExamAttemptApplication) resource {
	module := examAttemptHTTPModule{application: application}
	return newResource("security-preflights",
		sessionRoute(http.MethodGet, apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"), literal("security-policy")), securityPreflightErrorCodes(), module.recoverSecurityPolicy),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("exam-sittings"), canonicalID("exam_sitting_id"), literal("security-preflights")), securityPreflightErrorCodes(), module.prepareSecurityPreflight),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("security-preflights"), canonicalID("security_preflight_id"), literal("report")), securityPreflightErrorCodes(), module.reportSecurityPreflight))
}

func (m examAttemptHTTPModule) recoverSecurityPolicy(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return operationResult{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("attempt_id", err)
	}
	result, err := m.application.RecoverExamSecurityPolicy(request.context, request.invocation(), attemptID)
	if err != nil {
		return operationResult{}, err
	}
	encoded, err := json.Marshal(result)
	if result == nil || err != nil || len(encoded) > model.SecurityPolicyResponseMaxBytes {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, result), nil
}
