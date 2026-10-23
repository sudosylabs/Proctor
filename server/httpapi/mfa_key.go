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

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

// MFAKeyApplication is the operator surface of the MFA key ring.
type MFAKeyApplication interface {
	GetMFAKeyState(context.Context, invocation.Invocation) (app.MFAKeyStateView, error)
	StartMFARekey(context.Context, invocation.Invocation, app.StartMFARekeyCommand) (app.MFARekeyView, error)
	GetMFARekeyStatus(context.Context, invocation.Invocation, app.GetMFARekeyStatusQuery) (app.MFARekeyStatusView, error)
}

type mfaRekeyRequest struct {
	RetiringKeyID string `json:"retiring_key_id"`
}

type mfaRekeyResponse struct {
	JobID         string `json:"job_id"`
	PrimaryKeyID  string `json:"primary_key_id"`
	RetiringKeyID string `json:"retiring_key_id"`
	CreatedAt     string `json:"created_at"`
}

type mfaRekeyProgressResponse struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Stage   string `json:"stage"`
}

type mfaRekeyProofResponse struct {
	NonPrimaryReferences int64 `json:"non_primary_references"`
	RetiringReferences   int64 `json:"retiring_references"`
	RetirementSafe       bool  `json:"retirement_safe"`
}

type mfaRekeyStatusResponse struct {
	JobID           string                    `json:"job_id"`
	Status          model.JobStatus           `json:"status"`
	PrimaryKeyID    string                    `json:"primary_key_id"`
	RetiringKeyID   string                    `json:"retiring_key_id"`
	CreatedAt       string                    `json:"created_at"`
	UpdatedAt       string                    `json:"updated_at"`
	CompletedAt     string                    `json:"completed_at,omitempty"`
	PublicErrorCode string                    `json:"public_error_code,omitempty"`
	AttemptCount    int                       `json:"attempt_count"`
	MaximumAttempts int                       `json:"maximum_attempts"`
	Processed       int64                     `json:"processed"`
	Resealed        int64                     `json:"resealed"`
	Progress        *mfaRekeyProgressResponse `json:"progress,omitempty"`
	Proof           *mfaRekeyProofResponse    `json:"proof,omitempty"`
}

type mfaSecretKeyUsageResponse struct {
	KeyID       string `json:"key_id"`
	Credentials int64  `json:"credentials"`
	Configured  bool   `json:"configured"`
}

type mfaKeyStateResponse struct {
	PrimaryKeyID         string                      `json:"primary_key_id"`
	RequiredPrimaryKeyID string                      `json:"required_primary_key_id,omitempty"`
	ActiveRekeyJobID     string                      `json:"active_rekey_job_id,omitempty"`
	Active               []mfaSecretKeyUsageResponse `json:"active"`
}

type mfaKeyResourceModule struct{ application MFAKeyApplication }

func mfaKeyResource(application MFAKeyApplication) apiResource {
	module := mfaKeyResourceModule{application: application}
	return newResource("mfa-keys",
		strongRecentSessionRoute(http.MethodGet, apiPath(literal("mfa"), literal("keys")), []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "authentication.mfa.unavailable", "resource.not_found"}, module.getKeyState),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("mfa"), literal("rekey")), []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "authentication.mfa.rekey.invalid", "authentication.mfa.rekey.conflict", "authentication.mfa.unavailable", "resource.not_found"}, module.startRekey),
		strongRecentSessionRoute(http.MethodGet, apiPath(literal("mfa"), literal("rekey"), canonicalID("job_id")), operatorReadErrorCodes("request.invalid", "resource.not_found", "authentication.mfa.unavailable"), module.getRekeyStatus),
	)
}

func (m mfaKeyResourceModule) getKeyState(request operationRequest) (operationResult, error) {
	view, err := m.application.GetMFAKeyState(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	response := mfaKeyStateResponse{PrimaryKeyID: view.PrimaryKeyID, RequiredPrimaryKeyID: view.RequiredPrimaryKeyID,
		ActiveRekeyJobID: view.ActiveRekeyJobID.String(), Active: make([]mfaSecretKeyUsageResponse, 0, len(view.Active))}
	for _, usage := range view.Active {
		response.Active = append(response.Active, mfaSecretKeyUsageResponse{KeyID: usage.KeyID, Credentials: usage.Credentials, Configured: usage.Configured})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m mfaKeyResourceModule) startRekey(request operationRequest) (operationResult, error) {
	var body mfaRekeyRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	view, err := m.application.StartMFARekey(request.context, request.invocation(), app.StartMFARekeyCommand{RetiringKeyID: body.RetiringKeyID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusAccepted, mfaRekeyResponse{JobID: view.JobID.String(), PrimaryKeyID: view.PrimaryKeyID,
		RetiringKeyID: view.RetiringKeyID, CreatedAt: model.FormatRFC3339(view.CreatedAt)}), nil
}

func (m mfaKeyResourceModule) getRekeyStatus(request operationRequest) (operationResult, error) {
	jobID, err := request.params.RequireJobID()
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.application.GetMFARekeyStatus(request.context, request.invocation(), app.GetMFARekeyStatusQuery{JobID: jobID})
	if err != nil {
		return operationResult{}, err
	}
	response := mfaRekeyStatusResponse{JobID: view.JobID.String(), Status: view.Status,
		PrimaryKeyID: view.PrimaryKeyID, RetiringKeyID: view.RetiringKeyID,
		CreatedAt: model.FormatRFC3339(view.CreatedAt), UpdatedAt: model.FormatRFC3339(view.UpdatedAt),
		PublicErrorCode: view.PublicErrorCode, AttemptCount: view.AttemptCount, MaximumAttempts: view.MaximumAttempts,
		Processed: view.Processed, Resealed: view.Resealed}
	if view.CompletedAt.Valid {
		response.CompletedAt = view.CompletedAt.FormatRFC3339()
	}
	if view.Progress != nil {
		response.Progress = &mfaRekeyProgressResponse{Current: view.Progress.Current, Total: view.Progress.Total, Stage: view.Progress.Stage}
	}
	if view.Proof != nil {
		response.Proof = &mfaRekeyProofResponse{NonPrimaryReferences: view.Proof.NonPrimaryReferences,
			RetiringReferences: view.Proof.RetiringReferences, RetirementSafe: view.Proof.RetirementSafe}
	}
	return jsonResult(http.StatusOK, response), nil
}
