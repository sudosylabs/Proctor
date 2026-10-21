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

	application "github.com/sudosylabs/proctor/server/app"
)

type MFAAdministration interface {
	ResetUserMFA(context.Context, application.Invocation, application.ResetUserMFACommand) error
}

type mfaAdministrationResourceModule struct{ application MFAAdministration }

type resetUserMFARequest struct {
	IdentityVerified      bool   `json:"identity_verified"`
	Reason                string `json:"reason"`
	VerificationReference string `json:"verification_reference"`
}

func mfaAdministrationResource(application MFAAdministration) resource {
	module := mfaAdministrationResourceModule{application: application}
	return newResource("mfa-administration", strongRecentSessionRoute(http.MethodPost, apiPath(literal("users"), canonicalID("user_id"), literal("mfa"), literal("reset")), operatorMutationErrorCodes("request.invalid", "resource.not_found", "authentication.mfa.reset_invalid", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal"), module.reset))
}
func (m mfaAdministrationResourceModule) reset(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	var body resetUserMFARequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if err := m.application.ResetUserMFA(request.context, request.invocation(), application.ResetUserMFACommand{UserID: userID, IdentityVerified: body.IdentityVerified, Reason: body.Reason, VerificationReference: body.VerificationReference}); err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(noStoreHeaders()), nil
}
