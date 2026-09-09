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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type NativeConditionListQuery = store.NativeConditionListOptions

func (service *Service) ListNativeConditions(ctx context.Context, call Call, query NativeConditionListQuery) (*store.NativeConditionPage, error) {
	if !query.SubmissionID.IsValid() || query.Limit < 1 || query.Limit > 100 || query.AfterID != "" && !query.AfterID.IsValid() {
		return nil, invalid("native_condition_list")
	}
	if err := service.deps.Authorizer.AuthorizeView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	page, err := service.deps.Persistence.ListNativeConditions(ctx, query)
	if err != nil {
		return nil, mapStore(err)
	}
	if page == nil || page.Items == nil || len(page.Items) > query.Limit || page.HasMore && len(page.Items) != query.Limit {
		return nil, unavailable(errors.New("invalid native condition page"))
	}
	result := &store.NativeConditionPage{Items: make([]model.NativeConditionEvidence, len(page.Items)), HasMore: page.HasMore}
	previous := query.AfterID.String()
	for i, value := range page.Items {
		if value.Validate() != nil || value.ID.String() <= previous {
			return nil, unavailable(model.ErrNativeDeliveryInvalid)
		}
		previous = value.ID.String()
		result.Items[i] = value.Clone()
	}
	return result, nil
}
