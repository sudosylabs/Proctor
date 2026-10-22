// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

const defaultMailDeliveryPageSize = 50

type mailRekeyRequest struct {
	RetiringKeyID string `json:"retiring_key_id"`
}

type mailRekeyResponse struct {
	JobID         string `json:"job_id"`
	PrimaryKeyID  string `json:"primary_key_id"`
	RetiringKeyID string `json:"retiring_key_id"`
	CreatedAt     string `json:"created_at"`
}

type mailRekeyProgressResponse struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Stage   string `json:"stage"`
}

type mailRekeyProofResponse struct {
	NonPrimaryReferences int64 `json:"non_primary_references"`
	RetiringReferences   int64 `json:"retiring_references"`
	RetirementSafe       bool  `json:"retirement_safe"`
}

type mailRekeyStatusResponse struct {
	JobID           string                     `json:"job_id"`
	Status          model.JobStatus            `json:"status"`
	PrimaryKeyID    string                     `json:"primary_key_id"`
	RetiringKeyID   string                     `json:"retiring_key_id"`
	CreatedAt       string                     `json:"created_at"`
	UpdatedAt       string                     `json:"updated_at"`
	CompletedAt     string                     `json:"completed_at,omitempty"`
	PublicErrorCode string                     `json:"public_error_code,omitempty"`
	AttemptCount    int                        `json:"attempt_count"`
	MaximumAttempts int                        `json:"maximum_attempts"`
	Processed       int64                      `json:"processed"`
	Reencrypted     int64                      `json:"reencrypted"`
	Progress        *mailRekeyProgressResponse `json:"progress,omitempty"`
	Proof           *mailRekeyProofResponse    `json:"proof,omitempty"`
}

type mailPayloadKeyUsageResponse struct {
	KeyID            string `json:"key_id"`
	ActiveReferences int64  `json:"active_references"`
}

type mailKeyStateResponse struct {
	PrimaryKeyID         string                        `json:"primary_key_id"`
	RequiredPrimaryKeyID string                        `json:"required_primary_key_id,omitempty"`
	Active               []mailPayloadKeyUsageResponse `json:"active"`
}

type mailDeliveryResponse struct {
	ID                string                  `json:"id"`
	OccurrenceID      string                  `json:"occurrence_id"`
	TargetUserID      string                  `json:"target_user_id"`
	TemplateKey       model.MailTemplateKey   `json:"template_key"`
	TemplateDigest    string                  `json:"template_digest"`
	MaskedRecipient   string                  `json:"masked_recipient"`
	State             model.MailDeliveryState `json:"state"`
	CreatedAt         string                  `json:"created_at"`
	UpdatedAt         string                  `json:"updated_at"`
	MessageDate       string                  `json:"message_date"`
	Deadline          string                  `json:"deadline"`
	MessageID         string                  `json:"message_id"`
	AttemptCount      int                     `json:"attempt_count"`
	AcceptedAt        string                  `json:"accepted_at,omitempty"`
	FailedAt          string                  `json:"failed_at,omitempty"`
	PublicFailureCode string                  `json:"public_failure_code,omitempty"`
}

type mailDeliveryListResponse struct {
	Items      []mailDeliveryResponse `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type mailDeliveryMetricResponse struct {
	TemplateKey                    model.MailTemplateKey   `json:"template_key"`
	State                          model.MailDeliveryState `json:"state"`
	OutcomeCode                    string                  `json:"outcome_code"`
	Count                          uint64                  `json:"count"`
	AttemptCount                   uint64                  `json:"attempt_count"`
	ProcessingLatencyMillis        int64                   `json:"processing_latency_millis"`
	MaximumProcessingLatencyMillis int64                   `json:"maximum_processing_latency_millis"`
}

type mailQueueMetricResponse struct {
	TemplateKey     model.MailTemplateKey   `json:"template_key"`
	State           model.MailDeliveryState `json:"state"`
	OutcomeCode     string                  `json:"outcome_code"`
	Count           int64                   `json:"count"`
	OldestAgeMillis int64                   `json:"oldest_age_millis"`
	HealthCode      string                  `json:"health_code"`
	Truncated       bool                    `json:"truncated"`
}

type mailMetricsResponse struct {
	Deliveries []mailDeliveryMetricResponse `json:"deliveries"`
	Queues     []mailQueueMetricResponse    `json:"queues"`
	HealthCode string                       `json:"health_code"`
}

type mailDeliveryCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type mailResourceModule struct{ mail MailApplication }

func mailResource(application MailApplication) resource {
	module := mailResourceModule{mail: application}
	return newResource("mail",
		strongRecentSessionRoute(http.MethodGet, apiPath(literal("mail"), literal("keys")), operatorReadErrorCodes("mail.unavailable"), module.getKeyState),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("mail"), literal("rekey")), operatorMutationErrorCodes("request.invalid", "mail.rekey.invalid", "mail.rekey.conflict", "mail.unavailable"), module.startRekey),
		strongRecentSessionRoute(http.MethodGet, apiPath(literal("mail"), literal("rekey"), canonicalID("job_id")), operatorReadErrorCodes("request.invalid", "resource.not_found", "mail.unavailable"), module.getRekeyStatus),
		recentSessionRoute(http.MethodPost, apiPath(literal("mail"), literal("test")), operatorMutationErrorCodes("request.invalid", "mail.recipient_unverified", "mail.recipient_ineligible", "mail.test.rate_limited", "mail.conflict", "mail.unavailable"), module.sendTest),
		principalRoute(http.MethodGet, apiPath(literal("mail"), literal("metrics")), operatorReadErrorCodes("mail.unavailable"), module.getMetrics),
		principalRoute(http.MethodGet, apiPath(literal("mail"), literal("deliveries")), operatorReadErrorCodes("mail.query.invalid", "mail.unavailable", "request.invalid"), module.listDeliveries),
		principalRoute(http.MethodGet, apiPath(literal("mail"), literal("deliveries"), canonicalID("mail_delivery_id")), operatorReadErrorCodes("request.invalid", "resource.not_found", "mail.unavailable"), module.getDelivery),
		recentSessionRoute(http.MethodPost, apiPath(literal("mail"), literal("deliveries"), canonicalID("mail_delivery_id"), literal("cancel")), operatorMutationErrorCodes("request.invalid", "resource.not_found", "mail.conflict", "mail.unavailable"), module.cancelDelivery),
		recentSessionRoute(http.MethodPost, apiPath(literal("mail"), literal("deliveries"), canonicalID("mail_delivery_id"), literal("retry")), operatorMutationErrorCodes("request.invalid", "resource.not_found", "mail.conflict", "mail.unavailable"), module.retryDelivery),
	)
}

func (m mailResourceModule) getKeyState(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	view, appErr := m.mail.GetMailKeyState(request.context, request.invocation())
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := mailKeyStateResponse{PrimaryKeyID: view.PrimaryKeyID,
		RequiredPrimaryKeyID: view.RequiredPrimaryKeyID,
		Active:               make([]mailPayloadKeyUsageResponse, 0, len(view.Active))}
	for _, usage := range view.Active {
		response.Active = append(response.Active, mailPayloadKeyUsageResponse{KeyID: usage.KeyID,
			ActiveReferences: usage.ActiveReferences})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m mailResourceModule) startRekey(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	var body mailRekeyRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	view, appErr := m.mail.StartMailRekey(request.context, request.invocation(), body.RetiringKeyID)
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusAccepted, mailRekeyResponse{JobID: view.JobID.String(),
		PrimaryKeyID: view.PrimaryKeyID, RetiringKeyID: view.RetiringKeyID, CreatedAt: model.FormatRFC3339(view.CreatedAt)}), nil
}

func (m mailResourceModule) getRekeyStatus(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	raw, err := request.params.RequireJobID()
	if err != nil {
		return operationResult{}, err
	}
	view, appErr := m.mail.GetMailRekeyStatus(request.context, request.invocation(), model.JobID(raw))
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := mailRekeyStatusResponse{JobID: view.JobID.String(), Status: view.Status,
		PrimaryKeyID: view.PrimaryKeyID, RetiringKeyID: view.RetiringKeyID,
		CreatedAt: model.FormatRFC3339(view.CreatedAt), UpdatedAt: model.FormatRFC3339(view.UpdatedAt),
		PublicErrorCode: view.PublicErrorCode, AttemptCount: view.AttemptCount, MaximumAttempts: view.MaximumAttempts,
		Processed: view.Processed, Reencrypted: view.Reencrypted}
	if view.CompletedAt.Valid {
		response.CompletedAt = view.CompletedAt.FormatRFC3339()
	}
	if view.Progress != nil {
		response.Progress = &mailRekeyProgressResponse{Current: view.Progress.Current, Total: view.Progress.Total, Stage: view.Progress.Stage}
	}
	if view.Proof != nil {
		response.Proof = &mailRekeyProofResponse{NonPrimaryReferences: view.Proof.NonPrimaryReferences,
			RetiringReferences: view.Proof.RetiringReferences, RetirementSafe: view.Proof.RetirementSafe}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m mailResourceModule) getMetrics(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	snapshot, appErr := m.mail.GetMailMetrics(request.context, request.invocation())
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := mailMetricsResponse{
		Deliveries: make([]mailDeliveryMetricResponse, 0, len(snapshot.Deliveries)),
		Queues:     make([]mailQueueMetricResponse, 0, len(snapshot.Queues)), HealthCode: snapshot.HealthCode,
	}
	for _, metric := range snapshot.Deliveries {
		response.Deliveries = append(response.Deliveries, mailDeliveryMetricResponse{
			TemplateKey: metric.TemplateKey, State: metric.State, OutcomeCode: metric.OutcomeCode,
			Count: metric.Count, AttemptCount: metric.AttemptCount,
			ProcessingLatencyMillis:        metric.ProcessingLatency.Milliseconds(),
			MaximumProcessingLatencyMillis: metric.MaximumProcessingLatency.Milliseconds(),
		})
	}
	for _, metric := range snapshot.Queues {
		response.Queues = append(response.Queues, mailQueueMetricResponse{
			TemplateKey: metric.TemplateKey, State: metric.State, OutcomeCode: metric.OutcomeCode,
			Count: metric.Count, OldestAgeMillis: metric.OldestAge.Milliseconds(),
			HealthCode: metric.HealthCode, Truncated: metric.Truncated,
		})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m mailResourceModule) sendTest(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	if request.request.Body != nil {
		content, err := io.ReadAll(io.LimitReader(request.request.Body, 1))
		if err != nil || len(content) != 0 {
			return operationResult{}, apperror.New("request.invalid")
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
		return operationResult{}, errUnpresentableResult
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

func (m mailResourceModule) listDeliveries(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	query, err := listMailDeliveriesQuery(request.request)
	if err != nil {
		return operationResult{}, apperror.Invalid("request.invalid", "query")
	}
	page, appErr := m.mail.ListMailDeliveries(request.context, request.invocation(), query)
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := mailDeliveryListResponse{Items: make([]mailDeliveryResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, mailResponse(item))
	}
	if len(page.Items) == query.Limit {
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeMailDeliveryCursor(mailDeliveryCursor{CreatedAt: model.FormatRFC3339(last.CreatedAt), ID: last.ID.String()})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m mailResourceModule) cancelDelivery(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	return m.mutateDelivery(request, m.mail.CancelMailDelivery)
}

func (m mailResourceModule) retryDelivery(request operationRequest) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	return m.mutateDelivery(request, m.mail.RetryMailDelivery)
}

func (m mailResourceModule) mutateDelivery(request operationRequest, mutate func(context.Context, app.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)) (operationResult, error) {
	if m.mail == nil {
		return operationResult{}, errUnpresentableResult
	}
	if err := requireEmptyMailBody(request.request); err != nil {
		return operationResult{}, err
	}
	raw, err := request.params.RequireMailDeliveryID()
	if err != nil {
		return operationResult{}, err
	}
	view, appErr := mutate(request.context, request.invocation(), model.MailDeliveryID(raw))
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusOK, mailResponse(view)), nil
}

func requireEmptyMailBody(request *http.Request) error {
	if request.Body == nil {
		return nil
	}
	content, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(content) != 0 {
		return apperror.New("request.invalid")
	}
	return nil
}

func listMailDeliveriesQuery(request *http.Request) (app.ListMailDeliveriesQuery, error) {
	query := app.ListMailDeliveriesQuery{Limit: defaultMailDeliveryPageSize}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, errors.New("invalid mail delivery limit")
		}
		query.Limit = limit
	}
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeMailDeliveryCursor(raw)
		if err != nil {
			return query, err
		}
		query.BeforeCreatedAt, err = model.ParseRFC3339(cursor.CreatedAt)
		if err != nil {
			return query, errors.New("invalid mail delivery cursor time")
		}
		query.BeforeCreatedAt, query.BeforeID = query.BeforeCreatedAt.UTC(), model.MailDeliveryID(cursor.ID)
	}
	for _, raw := range request.URL.Query()["state"] {
		state := model.MailDeliveryState(raw)
		if !state.IsValid() {
			return query, errors.New("invalid mail delivery state")
		}
		query.States = append(query.States, state)
	}
	if len(query.States) > 6 {
		return query, errors.New("too many mail delivery states")
	}
	for _, raw := range request.URL.Query()["template_key"] {
		key := model.MailTemplateKey(raw)
		if !key.IsValid() {
			return query, errors.New("invalid mail template key")
		}
		query.TemplateKeys = append(query.TemplateKeys, key)
	}
	if len(query.TemplateKeys) > 64 {
		return query, errors.New("too many mail template keys")
	}
	var err error
	if raw := request.URL.Query().Get("created_after"); raw != "" {
		query.CreatedAfter, err = parseMailFilterTime(raw)
		if err != nil {
			return query, err
		}
	}
	if raw := request.URL.Query().Get("created_before"); raw != "" {
		query.CreatedBefore, err = parseMailFilterTime(raw)
		if err != nil {
			return query, err
		}
	}
	if !query.CreatedAfter.IsZero() && !query.CreatedBefore.IsZero() && !query.CreatedAfter.Before(query.CreatedBefore) {
		return query, errors.New("invalid mail delivery time range")
	}
	return query, nil
}

func parseMailFilterTime(raw string) (time.Time, error) {
	parsed, err := model.ParseRFC3339(raw)
	if err != nil || parsed.IsZero() {
		return time.Time{}, errors.New("invalid mail delivery time")
	}
	return parsed, nil
}

func encodeMailDeliveryCursor(cursor mailDeliveryCursor) (string, error) {
	return encodeOpaqueCursor(cursor, mailDeliveryCursorSpec())
}

func decodeMailDeliveryCursor(raw string) (mailDeliveryCursor, error) {
	return decodeOpaqueCursor(raw, mailDeliveryCursorSpec())
}

func mailDeliveryCursorSpec() opaqueCursorSpec[mailDeliveryCursor] {
	return opaqueCursorSpec[mailDeliveryCursor]{
		label: "mail delivery", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"created_at", "id"},
		validate: func(cursor mailDeliveryCursor) error {
			parsed, err := model.ParseRFC3339(cursor.CreatedAt)
			if err != nil || parsed.IsZero() || !model.MailDeliveryID(cursor.ID).IsValid() {
				return errors.New("invalid mail delivery keyset")
			}
			return nil
		},
	}
}

func mailResponse(view app.MailDeliveryView) mailDeliveryResponse {
	result := mailDeliveryResponse{ID: view.ID.String(), OccurrenceID: view.OccurrenceID.String(), TargetUserID: view.TargetUserID.String(), TemplateKey: view.TemplateKey, TemplateDigest: view.TemplateDigest, MaskedRecipient: view.MaskedRecipient, State: view.State, CreatedAt: model.FormatRFC3339(view.CreatedAt), UpdatedAt: model.FormatRFC3339(view.UpdatedAt), MessageDate: model.FormatRFC3339(view.MessageDate), Deadline: model.FormatRFC3339(view.Deadline), MessageID: view.MessageID, AttemptCount: view.AttemptCount, PublicFailureCode: view.PublicFailureCode}
	if view.AcceptedAt.Valid {
		result.AcceptedAt = view.AcceptedAt.FormatRFC3339()
	}
	if view.FailedAt.Valid {
		result.FailedAt = view.FailedAt.FormatRFC3339()
	}
	return result
}
