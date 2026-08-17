// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"io"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type mailDeliveryResponse struct {
	ID                string                  `json:"id"`
	OccurrenceID      string                  `json:"occurrence_id"`
	TargetUserID      string                  `json:"target_user_id"`
	TemplateKey       model.MailTemplateKey   `json:"template_key"`
	TemplateDigest    string                  `json:"template_digest"`
	MaskedRecipient   string                  `json:"masked_recipient"`
	State             model.MailDeliveryState `json:"state"`
	CreatedAt         int64                   `json:"created_at"`
	UpdatedAt         int64                   `json:"updated_at"`
	MessageDate       int64                   `json:"message_date"`
	Deadline          int64                   `json:"deadline"`
	MessageID         string                  `json:"message_id"`
	AttemptCount      int                     `json:"attempt_count"`
	AcceptedAt        int64                   `json:"accepted_at,omitempty"`
	FailedAt          int64                   `json:"failed_at,omitempty"`
	PublicFailureCode string                  `json:"public_failure_code,omitempty"`
}

type mailResourceModule struct{ mail MailApplication }

func mailResource(application MailApplication) resource {
	module := mailResourceModule{mail: application}
	return newResource("mail",
		recentSessionRoute(http.MethodPost, apiPath(literal("mail"), literal("test")), operatorMutationErrorCodes("authentication.reauthentication_required", "request.invalid", "mail.recipient_unverified", "mail.recipient_ineligible", "mail.test.rate_limited", "mail.conflict", "mail.unavailable"), module.sendTest),
		principalRoute(http.MethodGet, apiPath(literal("mail"), literal("deliveries"), canonicalID("mail_delivery_id")), operatorReadErrorCodes("request.invalid", "resource.not_found", "mail.unavailable"), module.getDelivery),
	)
}

func (m mailResourceModule) sendTest(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, application.NewError("mail.unavailable")
	}
	if request.request.Body != nil {
		content, err := io.ReadAll(io.LimitReader(request.request.Body, 1))
		if err != nil || len(content) != 0 {
			return operationResult{}, application.NewError("request.invalid")
		}
	}
	view, appErr := m.mail.SendTestMail(request.context, request.invocation())
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusAccepted, mailResponse(view)), nil
}

func (m mailResourceModule) getDelivery(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, application.NewError("mail.unavailable")
	}
	raw, err := request.params.RequireMailDeliveryID()
	if err != nil {
		return operationResult{}, err
	}
	view, appErr := m.mail.GetMailDelivery(request.context, request.invocation(), model.MailDeliveryID(raw))
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusOK, mailResponse(view)), nil
}

func mailResponse(view application.MailDeliveryView) mailDeliveryResponse {
	result := mailDeliveryResponse{ID: view.ID.String(), OccurrenceID: view.OccurrenceID.String(), TargetUserID: view.TargetUserID.String(), TemplateKey: view.TemplateKey, TemplateDigest: view.TemplateDigest, MaskedRecipient: view.MaskedRecipient, State: view.State, CreatedAt: model.MillisFromTime(view.CreatedAt), UpdatedAt: model.MillisFromTime(view.UpdatedAt), MessageDate: model.MillisFromTime(view.MessageDate), Deadline: model.MillisFromTime(view.Deadline), MessageID: view.MessageID, AttemptCount: view.AttemptCount, PublicFailureCode: view.PublicFailureCode}
	if view.AcceptedAt.Valid {
		result.AcceptedAt = view.AcceptedAt.Millis()
	}
	if view.FailedAt.Valid {
		result.FailedAt = view.FailedAt.Millis()
	}
	return result
}
