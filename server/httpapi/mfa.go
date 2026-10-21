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

	"github.com/sudosylabs/proctor/server/app"
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
	ExpiresAt       string `json:"expires_at"`
}

type mfaActivationResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type mfaStatusResponse struct {
	ServiceEnabled           bool   `json:"service_enabled"`
	MFARecoveryRequired      bool   `json:"mfa_recovery_required"`
	AuthenticationMethod     string `json:"authentication_method"`
	AuthenticationProviderID string `json:"authentication_provider_id,omitempty"`
	AuthenticationStrength   string `json:"authentication_strength"`
	RecentlyAuthenticated    bool   `json:"recently_authenticated"`
	Enabled                  bool   `json:"enabled"`
	Pending                  bool   `json:"pending"`
	PendingExpiresAt         string `json:"pending_expires_at,omitempty"`
	RecoveryCodesRemaining   int    `json:"recovery_codes_remaining"`
}

type mfaResourceModule struct {
	mfa MFA
}

func mfaResource(mfa MFA) resource {
	module := mfaResourceModule{mfa: mfa}
	base := apiPath(literal("users"), literal("me"), literal("mfa"))
	return newResource(
		"multi-factor-authentication",
		mfaRecoverySessionRoute(http.MethodGet, base, personalAccessTokenSessionCodes("authentication.mfa.disabled", "authentication.mfa.unavailable"), module.status),
		recentMFARecoverySessionRoute(http.MethodPost, appendRoutePath(base, literal("setup")), mfaRecentMutationCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.setup),
		recentMFARecoverySessionRoute(http.MethodPost, appendRoutePath(base, literal("activate")), mfaRecentMutationCodes("request.invalid", "authentication.mfa.code_invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.activate),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("challenge")), mfaSessionMutationCodes("request.invalid", "authentication.mfa.code_invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"), module.challenge),
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
	return mfaRecentMutationCodes(extra...)
}

func (m mfaResourceModule) status(request operationRequest) (operationResult, error) {
	status, err := m.mfa.GetMFAStatus(
		request.context,
		request.invocation(),
		app.GetMFAStatusQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaStatusResponse{
		ServiceEnabled: status.ServiceEnabled, MFARecoveryRequired: status.MFARecoveryRequired,
		AuthenticationMethod: status.AuthenticationMethod, AuthenticationProviderID: status.AuthenticationProviderID,
		AuthenticationStrength: string(status.AuthenticationStrength), RecentlyAuthenticated: status.RecentlyAuthenticated,
		Enabled:                status.Enabled,
		Pending:                status.Pending,
		PendingExpiresAt:       status.PendingExpiresAt.FormatRFC3339(),
		RecoveryCodesRemaining: status.RecoveryCodesRemaining,
	}).withHeaders(noStoreHeaders()), nil
}

func (m mfaResourceModule) setup(request operationRequest) (operationResult, error) {
	setup, err := m.mfa.SetupMFA(
		request.context,
		request.invocation(),
		app.SetupMFACommand{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, mfaSetupResponse{
		Secret:          setup.Secret,
		ProvisioningURI: setup.ProvisioningURI,
		ExpiresAt:       model.FormatRFC3339(setup.ExpiresAt),
	}).withHeaders(noStoreHeaders()), nil
}

func (m mfaResourceModule) activate(request operationRequest) (operationResult, error) {
	code, err := mfaCode(request)
	if err != nil {
		return operationResult{}, err
	}
	activation, err := m.mfa.ActivateMFA(
		request.context,
		request.invocation(),
		app.ActivateMFACommand{Code: code},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaActivationResponse{
		RecoveryCodes: activation.RecoveryCodes,
	}).withHeaders(noStoreHeaders()), nil
}

func (m mfaResourceModule) challenge(request operationRequest) (operationResult, error) {
	code, err := mfaCode(request)
	if err != nil {
		return operationResult{}, err
	}
	session, err := m.mfa.ChallengeMFA(
		request.context,
		request.invocation(),
		app.ChallengeMFACommand{Code: code},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, sessionResponseFromModel(request.request, session)).withHeaders(noStoreHeaders()), nil
}

func (m mfaResourceModule) regenerateRecoveryCodes(request operationRequest) (operationResult, error) {
	codes, err := m.mfa.RegenerateMFARecoveryCodes(
		request.context,
		request.invocation(),
		app.RegenerateMFARecoveryCodesCommand{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, mfaRecoveryCodesResponse{
		RecoveryCodes: codes,
	}).withHeaders(noStoreHeaders()), nil
}

func (m mfaResourceModule) disable(request operationRequest) (operationResult, error) {
	if err := m.mfa.DisableMFA(
		request.context,
		request.invocation(),
		app.DisableMFACommand{},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func mfaCode(request operationRequest) (string, error) {
	var input mfaCodeRequest
	if err := request.decodeJSON(&input); err != nil {
		return "", err
	}
	return input.Code, nil
}
