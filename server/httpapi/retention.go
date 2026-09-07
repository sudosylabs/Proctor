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
	"net/http"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type RetentionApplication interface {
	ListRetentionNotices(context.Context, application.Invocation, model.RetentionRetirementID, int) (*application.RetentionNoticePage, error)
	GetRetentionControl(context.Context, application.Invocation) (*model.RetentionControl, error)
	GetRetentionPreview(context.Context, application.Invocation, model.RetentionPreviewID) (*model.RetentionPreview, error)
	CreateRetentionPreview(context.Context, application.Invocation, application.CreateRetentionPreviewCommand) (*model.RetentionPreview, error)
	ChangeRetentionControl(context.Context, application.Invocation, application.ChangeRetentionControlCommand) (*model.RetentionControl, error)
	ListRetentionRecords(context.Context, application.Invocation, model.SubmissionID, int) (*application.RetentionRecordPage, error)
}

type retentionHTTPModule struct{ application RetentionApplication }

func retentionResource(app RetentionApplication) resource {
	m := retentionHTTPModule{application: app}
	readErrors := operatorReadErrorCodes("request.invalid", "retention.invalid", "retention.unavailable", "retention_policy.unavailable", "resource.not_found")
	mutationErrors := operatorMutationErrorCodes("request.invalid", "retention.invalid", "retention.conflict", "retention.unavailable", "retention_policy.unavailable", "retention_policy.revision_conflict", "resource.not_found",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	preview := sessionRoute(http.MethodPost, apiPath(literal("retention"), literal("previews")), mutationErrors, m.preview)
	control := strongRecentSessionRoute(http.MethodPut, apiPath(literal("retention"), literal("control")), append(append([]string(nil), mutationErrors...), "authentication.strong_required", "authentication.reauthentication_required"), m.changeControl)
	preview.idempotency, control.idempotency = IdempotencyRequired, IdempotencyRequired
	return newResource("retention",
		sessionRoute(http.MethodGet, apiPath(literal("retention"), literal("control")), readErrors, m.getControl), control, preview,
		sessionRoute(http.MethodGet, apiPath(literal("retention"), literal("previews"), canonicalID("retention_preview_id")), readErrors, m.getPreview),
		sessionRoute(http.MethodGet, apiPath(literal("retention"), literal("records")), readErrors, m.records),
		sessionRoute(http.MethodGet, apiPath(literal("users"), literal("me"), literal("retention-notices")), operatorReadErrorCodes("request.invalid", "retention.invalid", "retention.unavailable"), m.notices))
}

type retentionPreviewRequest struct {
	ExpectedPolicyRevision int64 `json:"expected_policy_revision"`
}
type retentionControlRequest struct {
	ExpectedRevision       int64  `json:"expected_revision"`
	ExpectedPolicyRevision int64  `json:"expected_policy_revision"`
	PreviewID              string `json:"preview_id,omitempty"`
	State                  string `json:"state"`
}
type retentionControlResponse struct {
	Revision               int64  `json:"revision"`
	State                  string `json:"state"`
	ApprovedPolicyRevision *int64 `json:"approved_policy_revision,omitempty"`
	ApprovedPreviewID      string `json:"approved_preview_id,omitempty"`
	UpdatedAt              string `json:"updated_at"`
}
type retentionPreviewCountsResponse struct {
	Total            int64 `json:"total"`
	Eligible         int64 `json:"eligible"`
	AwaitingDeadline int64 `json:"awaiting_deadline"`
	Incomplete       int64 `json:"incomplete"`
	Held             int64 `json:"held"`
	Unconfigured     int64 `json:"unconfigured"`
	SupportingWork   int64 `json:"supporting_work"`
	ExportProtected  int64 `json:"export_protected"`
	Retired          int64 `json:"retired"`
}
type retentionExpiryCountsResponse struct {
	Total            int64 `json:"total"`
	Eligible         int64 `json:"eligible"`
	AwaitingDeadline int64 `json:"awaiting_deadline"`
	Unconfigured     int64 `json:"unconfigured"`
	Unfinished       int64 `json:"unfinished"`
	Referenced       int64 `json:"referenced"`
	Held             int64 `json:"held"`
	PurgePending     int64 `json:"purge_pending"`
	SourceProtected  int64 `json:"source_protected"`
}

type retentionPreviewResponse struct {
	Audit          retentionExpiryCountsResponse  `json:"audit"`
	Receipts       retentionExpiryCountsResponse  `json:"receipts"`
	ID             string                         `json:"id"`
	PolicyRevision int64                          `json:"policy_revision"`
	CreatedAt      string                         `json:"created_at"`
	ExpiresAt      string                         `json:"expires_at"`
	Work           retentionPreviewCountsResponse `json:"work"`
	Integrity      retentionPreviewCountsResponse `json:"integrity"`
}
type retirementResponse struct {
	ID                 string  `json:"id"`
	State              string  `json:"state"`
	PolicyRevision     int64   `json:"policy_revision"`
	ControlRevision    int64   `json:"control_revision"`
	CompletionRevision int64   `json:"completion_revision"`
	ScheduledAt        string  `json:"scheduled_at"`
	RetireAfter        string  `json:"retire_after"`
	RetiredAt          *string `json:"retired_at,omitempty"`
	PurgePending       int64   `json:"purge_pending"`
	PurgeVerified      int64   `json:"purge_verified"`
}
type retentionRecordResponse struct {
	ExamID                 string              `json:"exam_id"`
	SittingID              string              `json:"exam_sitting_id"`
	SubmissionID           string              `json:"submission_id"`
	Category               string              `json:"category"`
	CompletionRevision     int64               `json:"completion_revision"`
	CompletedAt            *string             `json:"completed_at,omitempty"`
	CompletionCurrent      bool                `json:"completion_current"`
	HasIntegrity           bool                `json:"has_integrity"`
	Held                   bool                `json:"held"`
	ExportProtectedUntil   *string             `json:"export_protected_until,omitempty"`
	RetiredAt              *string             `json:"retired_at,omitempty"`
	SharedPublishedObjects int64               `json:"shared_published_objects"`
	Blocker                string              `json:"blocker"`
	EligibleAt             *string             `json:"eligible_at,omitempty"`
	Retirement             *retirementResponse `json:"retirement,omitempty"`
}
type retentionRecordsResponse struct {
	PolicyRevision int64                     `json:"policy_revision"`
	AsOf           string                    `json:"as_of"`
	Items          []retentionRecordResponse `json:"items"`
	NextCursor     string                    `json:"next_cursor,omitempty"`
}

func (m retentionHTTPModule) getControl(request operationRequest) (operationResult, error) {
	c, err := m.application.GetRetentionControl(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, retentionControlDTO(c)).withHeaders(privateNoStoreHeaders()), nil
}
func (m retentionHTTPModule) changeControl(request operationRequest) (operationResult, error) {
	var body retentionControlRequest
	if err := request.decodeJSON(&body, "changeRetentionControl"); err != nil {
		return operationResult{}, err
	}
	c, err := m.application.ChangeRetentionControl(request.context, request.invocation(), application.ChangeRetentionControlCommand{
		ExpectedRevision: body.ExpectedRevision, ExpectedPolicyRevision: body.ExpectedPolicyRevision, PreviewID: model.RetentionPreviewID(body.PreviewID),
		State: model.RetentionControlState(body.State), IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, retentionControlDTO(c)).withHeaders(privateNoStoreHeaders()), nil
}
func (m retentionHTTPModule) preview(request operationRequest) (operationResult, error) {
	var body retentionPreviewRequest
	if err := request.decodeJSON(&body, "createRetentionPreview"); err != nil {
		return operationResult{}, err
	}
	p, err := m.application.CreateRetentionPreview(request.context, request.invocation(), application.CreateRetentionPreviewCommand{ExpectedPolicyRevision: body.ExpectedPolicyRevision, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, retentionPreviewDTO(p)).withHeaders(privateNoStoreHeaders()), nil
}
func (m retentionHTTPModule) getPreview(request operationRequest) (operationResult, error) {
	id, err := model.ParseRetentionPreviewID(request.params.RetentionPreviewID)
	if err != nil {
		return operationResult{}, invalidRequestError("retention_preview_id", err)
	}
	p, err := m.application.GetRetentionPreview(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, retentionPreviewDTO(p)).withHeaders(privateNoStoreHeaders()), nil
}
func (m retentionHTTPModule) records(request operationRequest) (operationResult, error) {
	values := request.request.URL.Query()
	for key, items := range values {
		if len(items) != 1 || (key != "limit" && key != "cursor") {
			return operationResult{}, invalidRequestError("query", nil)
		}
	}
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	var after model.SubmissionID
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeOpaqueCursor(raw, retentionCursorSpec())
		if err != nil {
			return operationResult{}, invalidRequestError("cursor", err)
		}
		after = model.SubmissionID(cursor.After)
	}
	page, err := m.application.ListRetentionRecords(request.context, request.invocation(), after, limit)
	if err != nil {
		return operationResult{}, err
	}
	if page == nil || len(page.Items) > limit*2 || (page.HasMore && len(page.Items) == 0) {
		return operationResult{}, application.NewError("retention.unavailable")
	}
	response := retentionRecordsResponse{PolicyRevision: page.PolicyRevision, AsOf: retentionTime(page.AsOf), Items: make([]retentionRecordResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		r := item.Record
		value := retentionRecordResponse{ExamID: r.Scope.ExamID.String(), SittingID: r.Scope.SittingID.String(), SubmissionID: r.Scope.SubmissionID.String(), Category: string(r.Category),
			CompletionRevision: r.CompletionRevision, CompletedAt: retentionOptionalTime(r.CompletedAt), CompletionCurrent: r.CompletionCurrent, HasIntegrity: r.HasIntegrity,
			Held: r.Held, ExportProtectedUntil: retentionOptionalTime(r.ExportProtectedUntil), RetiredAt: retentionOptionalTime(r.RetiredAt), SharedPublishedObjects: r.SharedPublishedObjects,
			Blocker: string(item.Eligibility.Blocker), EligibleAt: retentionOptionalTime(item.Eligibility.EligibleAt)}
		if t := item.Retirement; t != nil {
			value.Retirement = &retirementResponse{ID: t.ID.String(), State: string(t.State), PolicyRevision: t.PolicyRevision, ControlRevision: t.ControlRevision,
				CompletionRevision: t.CompletionRevision, ScheduledAt: retentionTime(t.ScheduledAt), RetireAfter: retentionTime(t.RetireAfter), RetiredAt: retentionOptionalTime(t.RetiredAt), PurgePending: t.PurgePending, PurgeVerified: t.PurgeVerified}
		}
		response.Items = append(response.Items, value)
	}
	if page.HasMore {
		response.NextCursor, err = encodeOpaqueCursor(retentionCursor{After: page.Items[len(page.Items)-1].Record.Scope.SubmissionID.String()}, retentionCursorSpec())
		if err != nil {
			return operationResult{}, application.NewError("retention.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(privateNoStoreHeaders()), nil
}

type retentionCursor struct {
	Version int    `json:"version"`
	After   string `json:"after_submission_id"`
}

func retentionCursorSpec() opaqueCursorSpec[retentionCursor] {
	return opaqueCursorSpec[retentionCursor]{label: "retention", currentVersion: 1, maximumEncodedLength: 256, members: []string{"version", "after_submission_id"},
		version: func(c retentionCursor) int { return c.Version }, setVersion: func(c *retentionCursor, v int) { c.Version = v }, acceptsVersion: func(v int) bool { return v == 1 },
		validate: func(c retentionCursor) error {
			if !model.SubmissionID(c.After).IsValid() {
				return errors.New("invalid retention cursor")
			}
			return nil
		}}
}
func retentionTime(at time.Time) string { return model.TimeUTC(at).Format(time.RFC3339Nano) }
func retentionOptionalTime(at model.OptionalTime) *string {
	if !at.Valid {
		return nil
	}
	value := retentionTime(at.Time)
	return &value
}
func retentionControlDTO(c *model.RetentionControl) retentionControlResponse {
	if c == nil {
		return retentionControlResponse{}
	}
	value := retentionControlResponse{Revision: c.Revision, State: string(c.State), UpdatedAt: retentionTime(c.UpdatedAt)}
	if c.ApprovedPolicyRevision > 0 {
		value.ApprovedPolicyRevision = &c.ApprovedPolicyRevision
		value.ApprovedPreviewID = c.ApprovedPreviewID.String()
	}
	return value
}
func retentionPreviewCountsDTO(c model.RetentionPreviewCounts) retentionPreviewCountsResponse {
	return retentionPreviewCountsResponse{Total: c.Total, Eligible: c.Eligible, AwaitingDeadline: c.AwaitingDeadline, Incomplete: c.Incomplete, Held: c.Held, Unconfigured: c.Unconfigured, SupportingWork: c.SupportingWork, ExportProtected: c.ExportProtected, Retired: c.Retired}
}
func retentionPreviewDTO(p *model.RetentionPreview) retentionPreviewResponse {
	if p == nil {
		return retentionPreviewResponse{}
	}
	return retentionPreviewResponse{Audit: retentionExpiryCountsDTO(p.Audit), Receipts: retentionExpiryCountsDTO(p.Receipts), ID: p.ID.String(), PolicyRevision: p.PolicyRevision, CreatedAt: retentionTime(p.CreatedAt), ExpiresAt: retentionTime(p.ExpiresAt), Work: retentionPreviewCountsDTO(p.Work), Integrity: retentionPreviewCountsDTO(p.Integrity)}
}

func retentionExpiryCountsDTO(c model.RetentionExpiryCounts) retentionExpiryCountsResponse {
	return retentionExpiryCountsResponse{Total: c.Total, Eligible: c.Eligible, AwaitingDeadline: c.AwaitingDeadline, Unconfigured: c.Unconfigured, Unfinished: c.Unfinished, Referenced: c.Referenced, Held: c.Held, PurgePending: c.PurgePending, SourceProtected: c.SourceProtected}
}
