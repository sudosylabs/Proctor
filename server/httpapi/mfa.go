// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/user.go MFA handlers. Proctor
// exposes the assurance transition explicitly, returns setup secrets and
// recovery codes only once, and classifies sensitive mutations with the
// strong/recent session wrappers.

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type mfaCodeRequest struct {
	Code string `json:"code"`
}

type mfaRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type mfaSetupResponse struct {
	Secret          string `json:"secret"`
	ProvisioningURI string `json:"provisioning_uri"`
	ExpiresAt       int64  `json:"expires_at"`
}

type mfaActivationResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type mfaStatusResponse struct {
	Enabled                bool  `json:"enabled"`
	Pending                bool  `json:"pending"`
	PendingExpiresAt       int64 `json:"pending_expires_at,omitempty"`
	RecoveryCodesRemaining int   `json:"recovery_codes_remaining"`
}

type mfaResourceModule struct {
	mfa MFA
}

func mfaResource(mfa MFA) resource {
	module := mfaResourceModule{mfa: mfa}
	base := apiPath(literal("users"), literal("me"), literal("mfa"))
	return newResource(
		"multi-factor-authentication",
		sessionRoute(http.MethodGet, base, personalAccessTokenSessionCodes("authentication.mfa.disabled", "authentication.mfa.unavailable"), module.status),
		recentSessionRoute(http.MethodPost, appendRoutePath(base, literal("setup")), mfaRecentMutationCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.setup),
		recentSessionRoute(http.MethodPost, appendRoutePath(base, literal("activate")), mfaRecentMutationCodes("request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.activate),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("challenge")), mfaSessionMutationCodes("request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.challenge),
		strongRecentSessionRoute(http.MethodPost, appendRoutePath(base, literal("recovery-codes"), literal("regenerate")), mfaStrongRecentMutationCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.regenerateRecoveryCodes),
		strongRecentSessionRoute(http.MethodPost, appendRoutePath(base, literal("disable")), mfaStrongRecentMutationCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "audit.unavailable"), module.disable),
	)
}

func mfaSessionMutationCodes(extra ...string) []string {
	return personalAccessTokenSessionMutationCodes(extra...)
}

func mfaRecentMutationCodes(extra ...string) []string {
	return personalAccessTokenRecentMutationCodes(extra...)
}

func mfaStrongRecentMutationCodes(extra ...string) []string {
	return mfaRecentMutationCodes(append([]string{"authentication.strong_required"}, extra...)...)
}

func (module mfaResourceModule) status(request operationRequest) (operationResult, error) {
	status, err := module.mfa.GetMFAStatus(
		request.context,
		request.invocation(),
		application.GetMFAStatusQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaStatusResponse{
		Enabled:                status.Enabled,
		Pending:                status.Pending,
		PendingExpiresAt:       status.PendingExpiresAt.Millis(),
		RecoveryCodesRemaining: status.RecoveryCodesRemaining,
	}).withHeaders(noStoreHeaders()), nil
}

func (module mfaResourceModule) setup(request operationRequest) (operationResult, error) {
	setup, err := module.mfa.SetupMFA(
		request.context,
		request.invocation(),
		application.SetupMFACommand{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, mfaSetupResponse{
		Secret:          setup.Secret,
		ProvisioningURI: setup.ProvisioningURI,
		ExpiresAt:       model.MillisFromTime(setup.ExpiresAt),
	}).withHeaders(noStoreHeaders()), nil
}

func (module mfaResourceModule) activate(request operationRequest) (operationResult, error) {
	code, err := mfaCode(request, "activateMFA")
	if err != nil {
		return operationResult{}, err
	}
	activation, err := module.mfa.ActivateMFA(
		request.context,
		request.invocation(),
		application.ActivateMFACommand{Code: code},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaActivationResponse{
		RecoveryCodes: activation.RecoveryCodes,
	}).withHeaders(noStoreHeaders()), nil
}

func (module mfaResourceModule) challenge(request operationRequest) (operationResult, error) {
	code, err := mfaCode(request, "challengeMFA")
	if err != nil {
		return operationResult{}, err
	}
	session, err := module.mfa.ChallengeMFA(
		request.context,
		request.invocation(),
		application.ChallengeMFACommand{Code: code},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, sessionResponseFromModel(request.request, session)).withHeaders(noStoreHeaders()), nil
}

func (module mfaResourceModule) regenerateRecoveryCodes(request operationRequest) (operationResult, error) {
	codes, err := module.mfa.RegenerateMFARecoveryCodes(
		request.context,
		request.invocation(),
		application.RegenerateMFARecoveryCodesCommand{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaRecoveryCodesResponse{
		RecoveryCodes: codes,
	}).withHeaders(noStoreHeaders()), nil
}

func (module mfaResourceModule) disable(request operationRequest) (operationResult, error) {
	if err := module.mfa.DisableMFA(
		request.context,
		request.invocation(),
		application.DisableMFACommand{},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func mfaCode(request operationRequest, where string) (string, error) {
	var input mfaCodeRequest
	if err := request.decodeJSON(&input, where); err != nil {
		return "", err
	}
	return input.Code, nil
}
