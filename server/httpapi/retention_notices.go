// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"net/http"
)

type retentionNoticeResponse struct {
	RetirementID      string  `json:"retirement_id"`
	ExamID            string  `json:"exam_id"`
	SittingID         string  `json:"exam_sitting_id"`
	SubmissionID      string  `json:"submission_id"`
	Category          string  `json:"category"`
	State             string  `json:"state"`
	CreatedAt         string  `json:"created_at"`
	RetireAfter       string  `json:"retire_after"`
	CancelledAt       *string `json:"cancelled_at,omitempty"`
	DeliveryState     string  `json:"delivery_state"`
	DeliveryErrorCode string  `json:"delivery_error_code,omitempty"`
}
type retentionNoticesResponse struct {
	Items      []retentionNoticeResponse `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
type retentionNoticeCursor struct {
	Version int    `json:"version"`
	After   string `json:"after_retirement_id"`
}

func retentionNoticeCursorSpec() opaqueCursorSpec[retentionNoticeCursor] {
	return opaqueCursorSpec[retentionNoticeCursor]{label: "retention-notices", currentVersion: 1, maximumEncodedLength: 256, members: []string{"version", "after_retirement_id"},
		version: func(c retentionNoticeCursor) int { return c.Version }, setVersion: func(c *retentionNoticeCursor, v int) { c.Version = v }, acceptsVersion: func(v int) bool { return v == 1 },
		validate: func(c retentionNoticeCursor) error {
			if !model.RetentionRetirementID(c.After).IsValid() {
				return errors.New("invalid retention notice cursor")
			}
			return nil
		}}
}
func (m retentionHTTPModule) notices(request operationRequest) (operationResult, error) {
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
	var after model.RetentionRetirementID
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeOpaqueCursor(raw, retentionNoticeCursorSpec())
		if err != nil {
			return operationResult{}, invalidRequestError("cursor", err)
		}
		after = model.RetentionRetirementID(cursor.After)
	}
	page, err := m.application.ListRetentionNotices(request.context, request.invocation(), after, limit)
	if err != nil {
		return operationResult{}, err
	}
	if page == nil || len(page.Items) > limit || (page.HasMore && len(page.Items) == 0) {
		return operationResult{}, application.NewError("retention.unavailable")
	}
	response := retentionNoticesResponse{Items: make([]retentionNoticeResponse, 0, len(page.Items))}
	previous := after
	for _, n := range page.Items {
		if !n.RetirementID.IsValid() || n.RetirementID <= previous || n.RecipientUserID != request.invocation().Principal().UserID {
			return operationResult{}, application.NewError("retention.unavailable")
		}
		response.Items = append(response.Items, retentionNoticeResponse{RetirementID: n.RetirementID.String(), ExamID: n.Scope.ExamID.String(), SittingID: n.Scope.SittingID.String(), SubmissionID: n.Scope.SubmissionID.String(), Category: string(n.Category), State: string(n.State), CreatedAt: retentionTime(n.CreatedAt), RetireAfter: retentionTime(n.RetireAfter), CancelledAt: retentionOptionalTime(n.CancelledAt), DeliveryState: n.DeliveryState, DeliveryErrorCode: n.DeliveryErrorCode})
		previous = n.RetirementID
	}
	if page.HasMore {
		response.NextCursor, err = encodeOpaqueCursor(retentionNoticeCursor{After: previous.String()}, retentionNoticeCursorSpec())
		if err != nil {
			return operationResult{}, application.NewError("retention.unavailable")
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(privateNoStoreHeaders()), nil
}
