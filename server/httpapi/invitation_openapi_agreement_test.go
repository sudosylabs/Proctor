// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInvitationAdministrationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST /api/v1/invitation-batches", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/RunInvitationBatch", RequestSchema: "InvitationBatchRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/InvitationBatchOK", SuccessSchema: "InvitationBatchResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "authentication.strong_required", "authentication.reauthentication_required", "invitation.unavailable", "idempotency.key_required", "idempotency.key_invalid"}},
			{Key: "GET /api/v1/invitations", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/InvitationAdministrationListOK", SuccessSchema: "InvitationAdministrationListResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "invitation.query.invalid", "invitation.unavailable"}},
			{Key: "GET /api/v1/invitations/{invitation_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/InvitationAdministrationOK", SuccessSchema: "InvitationAdministrationResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "invitation.unavailable"}},
			{Key: "POST /api/v1/invitations/{invitation_id}/resend", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/MutateInvitation", RequestSchema: "InvitationMutationRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/InvitationAdministrationOK", SuccessSchema: "InvitationAdministrationResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.reauthentication_required", "authentication.strong_required", "onboarding_batch.duplicate"}},
			{Key: "POST /api/v1/invitations/{invitation_id}/revoke", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/MutateInvitation", RequestSchema: "InvitationMutationRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/InvitationAdministrationOK", SuccessSchema: "InvitationAdministrationResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.reauthentication_required", "authentication.strong_required", "onboarding_batch.duplicate"}},
			{Key: "POST /api/v1/invitations/{invitation_id}/replacement", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/ReplaceInvitation", RequestSchema: "ReplaceInvitationRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/InvitationAdministrationCreated", SuccessSchema: "InvitationAdministrationResponse", PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.strong_required", "authentication.reauthentication_required"}},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "InvitationBatchItemRequest", DTO: reflect.TypeOf(invitationBatchItemRequest{}), Required: []string{"key"}},
			{Name: "InvitationBatchRequest", DTO: reflect.TypeOf(invitationBatchRequest{}), Required: []string{"operation", "scope_type", "scope_id", "items"}},
			{Name: "InvitationBatchItemResponse", DTO: reflect.TypeOf(invitationBatchItemResponse{}), Required: []string{"index", "status"}},
			{Name: "InvitationBatchResponse", DTO: reflect.TypeOf(invitationBatchResponse{}), Required: []string{"operation", "items", "succeeded", "no_op", "failed"}},
			{Name: "InvitationMutationRequest", DTO: reflect.TypeOf(invitationMutationRequest{}), Required: []string{"expected_revision"}},
			{Name: "ReplaceInvitationRequest", DTO: reflect.TypeOf(replaceInvitationRequest{}), Required: []string{"expected_revision", "purpose", "email"}},
			{Name: "InvitationDeliveryResponse", DTO: reflect.TypeOf(invitationDeliveryResponse{}), Required: []string{"template_key", "state", "masked_recipient", "created_at", "updated_at", "deadline"}},
			{Name: "InvitationAdministrationResponse", DTO: reflect.TypeOf(invitationAdministrationResponse{}), Required: []string{"id", "purpose", "state", "start_at", "expires_at", "email", "inviter_user_id", "created_at", "updated_at", "revision"}},
			{Name: "InvitationAdministrationListResponse", DTO: reflect.TypeOf(invitationListResponse{}), Required: []string{"items"}},
		},
		OperationSelector: func(_ string, path string) bool {
			return path == "/api/v1/invitation-batches" || path == "/api/v1/invitations" || strings.HasPrefix(path, "/api/v1/invitations/{invitation_id}")
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, invitationResource(&invitationHTTPApplication{})))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
