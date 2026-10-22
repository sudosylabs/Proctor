// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type RetentionNoticePage struct {
	Items   []model.RetentionNotice
	HasMore bool
}

// ListRetentionNotices returns only the current recipient's durable notices.
// It grants no underlying record access and has no read-acknowledgement effect.
func (s *retentionPolicyService) ListRetentionNotices(ctx context.Context, invocation invocation.Invocation, after model.RetentionRetirementID, limit int) (*RetentionNoticePage, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, apperror.New("authentication.required")
	}
	if limit < 1 || limit > model.RetentionMaximumPageSize || (!after.IsZero() && !after.IsValid()) {
		return nil, apperror.New("retention.invalid")
	}
	items, err := s.deps.Lifecycle.ListNotices(ctx, principal.UserID, after, limit+1)
	if err != nil {
		return nil, retentionError(err)
	}
	if len(items) > limit+1 {
		return nil, apperror.New("retention.unavailable")
	}
	page := &RetentionNoticePage{Items: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	for _, item := range page.Items {
		if item.RecipientUserID != principal.UserID {
			return nil, apperror.New("retention.unavailable")
		}
	}
	return page, nil
}
