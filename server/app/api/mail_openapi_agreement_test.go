// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"reflect"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type mailAPIFake struct{}

func (mailAPIFake) SendTestMail(context.Context, application.Invocation) (application.MailDeliveryView, error) {
	return application.MailDeliveryView{}, nil
}
func (mailAPIFake) GetMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error) {
	return application.MailDeliveryView{}, nil
}

func TestMailOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	mutationCodes := principalMutationContractCodes("authentication.reauthentication_required", "request.invalid", "mail.recipient_unverified", "mail.recipient_ineligible", "mail.test.rate_limited", "mail.conflict", "mail.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST /api/v1/mail/test", Auth: AuthRecentSessionRequired, SuccessStatus: "202", SuccessRef: "#/components/responses/MailDeliveryAccepted", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/mail/deliveries/{mail_delivery_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MailDeliveryOK", SuccessSchema: "MailDeliveryResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "mail.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "MailDeliveryResponse", DTO: reflect.TypeOf(mailDeliveryResponse{}), Required: []string{"id", "occurrence_id", "target_user_id", "template_key", "template_digest", "masked_recipient", "state", "created_at", "updated_at", "message_date", "deadline", "message_id", "attempt_count"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, mailResource(mailAPIFake{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
