// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

type RetentionNoticePage struct {
	Items   []model.RetentionNotice
	HasMore bool
}

// ListRetentionNotices returns only the current recipient's durable notices.
// It grants no underlying record access and has no read-acknowledgement effect.
func (a *App) ListRetentionNotices(ctx context.Context, invocation Invocation, after model.RetentionRetirementID, limit int) (*RetentionNoticePage, error) {
	if a == nil || a.retentionPolicy == nil || a.retentionPolicy.lifecycle == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.ListNotices(ctx, invocation, after, limit)
}

func (s *retentionPolicyService) ListNotices(ctx context.Context, invocation Invocation, after model.RetentionRetirementID, limit int) (*RetentionNoticePage, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, NewError("authentication.required")
	}
	if limit < 1 || limit > model.RetentionMaximumPageSize || (!after.IsZero() && !after.IsValid()) {
		return nil, NewError("retention.invalid")
	}
	items, err := s.lifecycle.ListNotices(ctx, principal.UserID, after, limit+1)
	if err != nil {
		return nil, retentionError(err)
	}
	if len(items) > limit+1 {
		return nil, NewError("retention.unavailable")
	}
	page := &RetentionNoticePage{Items: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	for _, item := range page.Items {
		if item.RecipientUserID != principal.UserID {
			return nil, NewError("retention.unavailable")
		}
	}
	return page, nil
}
