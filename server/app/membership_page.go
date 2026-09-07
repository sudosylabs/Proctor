// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"github.com/sudosylabs/proctor/server/model"
	"time"
)

// MembershipPageQuery selects an effective instant or full retained history.
// Transport adapters resolve opaque continuations into explicit selectors.
type MembershipPageQuery struct {
	Limit    int
	ActiveAt *int64
	History  *bool
}

func resolveMembershipPage(query MembershipPageQuery, now time.Time) (int64, int, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return 0, 0, NewError("request.invalid").WithField("field", "limit")
	}
	if query.ActiveAt != nil && *query.ActiveAt <= 0 {
		return 0, 0, NewError("request.invalid").WithField("field", "active_at")
	}
	if query.History != nil && *query.History && query.ActiveAt != nil {
		return 0, 0, NewError("request.invalid").WithField("field", "history")
	}
	activeAt := model.MillisFromTime(now)
	if query.ActiveAt != nil {
		activeAt = *query.ActiveAt
	}
	if query.History != nil && *query.History {
		activeAt = 0
	}
	return activeAt, limit, nil
}
