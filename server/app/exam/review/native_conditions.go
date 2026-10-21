// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type NativeConditionListQuery = store.NativeConditionListOptions

func (s *Service) ListNativeConditions(ctx context.Context, invocation invocation.Invocation, query NativeConditionListQuery) (_ *store.NativeConditionPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > 100 || query.AfterID != "" && !query.AfterID.IsValid() {
		return nil, apperror.Invalid("exam.integrity_review.invalid", "native_condition_list")
	}
	if err := s.deps.Authorizer.AuthorizeView(ctx, invocation, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := s.deps.Persistence.ListNativeConditions(ctx, query)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if page == nil || page.Items == nil || len(page.Items) > query.Limit || page.HasMore && len(page.Items) != query.Limit {
		return nil, apperror.New("exam.integrity_review.unavailable").Wrap(errors.New("invalid native condition page"))
	}
	result := &store.NativeConditionPage{Items: make([]model.NativeConditionEvidence, len(page.Items)), HasMore: page.HasMore}
	previous := query.AfterID.String()
	for i, value := range page.Items {
		if value.Validate() != nil || value.ID.String() <= previous {
			return nil, apperror.New("exam.integrity_review.unavailable").Wrap(model.ErrNativeDeliveryInvalid)
		}
		previous = value.ID.String()
		result.Items[i] = value.Clone()
	}
	return result, nil
}
