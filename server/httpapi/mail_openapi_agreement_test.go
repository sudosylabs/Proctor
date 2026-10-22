// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type mailAPIFake struct{}

func (mailAPIFake) GetMailKeyState(context.Context, invocation.Invocation) (app.MailKeyStateView, error) {
	return app.MailKeyStateView{}, nil
}

func (mailAPIFake) StartMailRekey(context.Context, invocation.Invocation, string) (app.MailRekeyView, error) {
	return app.MailRekeyView{}, nil
}

func (mailAPIFake) GetMailRekeyStatus(context.Context, invocation.Invocation, model.JobID) (app.MailRekeyStatusView, error) {
	return app.MailRekeyStatusView{}, nil
}

func (mailAPIFake) SendTestMail(context.Context, invocation.Invocation) (app.MailDeliveryView, error) {
	return app.MailDeliveryView{}, nil
}
func (mailAPIFake) GetMailMetrics(context.Context, invocation.Invocation) (app.MailMetricsSnapshot, error) {
	return app.MailMetricsSnapshot{}, nil
}
func (mailAPIFake) GetMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error) {
	return app.MailDeliveryView{}, nil
}
func (mailAPIFake) ListMailDeliveries(context.Context, invocation.Invocation, app.ListMailDeliveriesQuery) (app.MailDeliveryPage, error) {
	return app.MailDeliveryPage{}, nil
}
func (mailAPIFake) CancelMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error) {
	return app.MailDeliveryView{}, nil
}
func (mailAPIFake) RetryMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error) {
	return app.MailDeliveryView{}, nil
}

func TestMailOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	mutationCodes := principalMutationContractCodes("authentication.reauthentication_required", "request.invalid", "mail.recipient_unverified", "mail.recipient_ineligible", "mail.test.rate_limited", "mail.conflict", "mail.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/mail/keys", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailKeyStateOK", SuccessSchema: "MailKeyStateResponse", PublicErrorCodes: principalContractCodes("authentication.strong_required", "authentication.reauthentication_required", "mail.unavailable")},
			{Key: "POST /api/v1/mail/rekey", Auth: AuthStrongRecentSessionRequired, RequestBodyRef: "#/components/requestBodies/StartMailRekey", RequestSchema: "MailRekeyRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/MailRekeyAccepted", SuccessSchema: "MailRekeyResponse", PublicErrorCodes: principalMutationContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "mail.rekey.invalid", "mail.rekey.conflict", "mail.unavailable")},
			{Key: "GET /api/v1/mail/rekey/{job_id}", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailRekeyStatusOK", SuccessSchema: "MailRekeyStatusResponse", PublicErrorCodes: principalContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "mail.unavailable")},
			{Key: "POST /api/v1/mail/test", Auth: AuthRecentSessionRequired, SuccessStatus: "202", SuccessRef: "#/components/responses/MailDeliveryAccepted", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/mail/metrics", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailMetricsOK", SuccessSchema: "MailMetricsResponse", PublicErrorCodes: principalContractCodes("mail.unavailable")},
			{Key: "GET /api/v1/mail/deliveries", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailDeliveryListOK", SuccessSchema: "MailDeliveryListResponse", PublicErrorCodes: principalContractCodes("mail.query.invalid", "mail.unavailable", "request.invalid")},
			{Key: "GET /api/v1/mail/deliveries/{mail_delivery_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailDeliveryOK", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "mail.unavailable")},
			{Key: "POST /api/v1/mail/deliveries/{mail_delivery_id}/cancel", Auth: AuthRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailDeliveryOK", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: principalMutationContractCodes("authentication.reauthentication_required", "request.invalid", "resource.not_found", "mail.conflict", "mail.unavailable")},
			{Key: "POST /api/v1/mail/deliveries/{mail_delivery_id}/retry", Auth: AuthRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailDeliveryOK", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: principalMutationContractCodes("authentication.reauthentication_required", "request.invalid", "resource.not_found", "mail.conflict", "mail.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "MailRekeyRequest", DTO: reflect.TypeOf(mailRekeyRequest{}), Required: []string{"retiring_key_id"}},
			{Name: "MailKeyStateResponse", DTO: reflect.TypeOf(mailKeyStateResponse{}), Required: []string{"primary_key_id", "active"}},
			{Name: "MailPayloadKeyUsage", DTO: reflect.TypeOf(mailPayloadKeyUsageResponse{}), Required: []string{"key_id", "active_references"}},
			{Name: "MailRekeyResponse", DTO: reflect.TypeOf(mailRekeyResponse{}), Required: []string{"job_id", "primary_key_id", "retiring_key_id", "created_at"}},
			{Name: "MailRekeyProgress", DTO: reflect.TypeOf(mailRekeyProgressResponse{}), Required: []string{"current", "total", "stage"}},
			{Name: "MailRekeyProof", DTO: reflect.TypeOf(mailRekeyProofResponse{}), Required: []string{"non_primary_references", "retiring_references", "retirement_safe"}},
			{Name: "MailRekeyStatusResponse", DTO: reflect.TypeOf(mailRekeyStatusResponse{}), Required: []string{"job_id", "status", "primary_key_id", "retiring_key_id", "created_at", "updated_at", "attempt_count", "maximum_attempts", "processed", "reencrypted"}},
			{Name: "MailDeliveryResponse", DTO: reflect.TypeOf(mailDeliveryResponse{}), Required: []string{"id", "occurrence_id", "target_user_id", "template_key", "template_digest", "masked_recipient", "state", "created_at", "updated_at", "message_date", "deadline", "message_id", "attempt_count"}},
			{Name: "MailDeliveryListResponse", DTO: reflect.TypeOf(mailDeliveryListResponse{}), Required: []string{"items"}},
			{Name: "MailMetricsResponse", DTO: reflect.TypeOf(mailMetricsResponse{}), Required: []string{"deliveries", "queues", "health_code"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, mailResource(mailAPIFake{})))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
