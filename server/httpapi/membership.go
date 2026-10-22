// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/model"
)

func membershipPageQuery(request *http.Request, scope model.Resource) (app.MembershipPageQuery, membershipPageCursor, error) {
	values := request.URL.Query()
	query := app.MembershipPageQuery{Limit: 50}
	var cursor membershipPageCursor
	for _, key := range []string{"limit", "cursor", "active_at", "history"} {
		if entries, present := values[key]; present && (len(entries) != 1 || entries[0] == "") {
			return query, cursor, invalidRequestError(nil)
		}
	}
	if values.Has("limit") {
		limit, err := strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			return query, cursor, invalidRequestError(err)
		}
		query.Limit = limit
	}
	if values.Has("active_at") {
		parsed, err := model.ParseRFC3339(values.Get("active_at"))
		if err != nil || parsed.IsZero() {
			return query, cursor, invalidRequestError(err)
		}
		at := model.MillisFromTime(parsed)
		query.ActiveAt = &at
	}
	if values.Has("history") {
		value := values.Get("history")
		if value != "true" && value != "false" {
			return query, cursor, invalidRequestError(nil)
		}
		history := value == "true"
		query.History = &history
	}
	if query.History != nil && *query.History && query.ActiveAt != nil {
		return query, cursor, invalidRequestError(nil)
	}
	if values.Has("cursor") {
		decoded, err := decodeOpaqueCursor(values.Get("cursor"), membershipPageCursorSpec())
		if err != nil {
			return query, cursor, invalidRequestError(err)
		}
		if decoded.ScopeType != scope.Type || decoded.ScopeID != scope.ID ||
			(query.ActiveAt != nil && *query.ActiveAt != *decoded.ActiveAt) ||
			(query.History != nil && *query.History != (*decoded.ActiveAt == 0)) {
			return query, cursor, invalidRequestError(nil)
		}
		cursor = decoded
		history := *cursor.ActiveAt == 0
		query.History = &history
		if !history {
			query.ActiveAt = cursor.ActiveAt
		}
	}
	return query, cursor, nil
}

// membershipListResult presents one membership page; its continuation fixes
// the scope and effective time of the first page.
func membershipListResult[I, T any](scope model.Resource, activeAt int64, members []I, hasMore bool, present func(I) T, key func(I) (string, string)) (operationResult, error) {
	response, err := newListResponse(members, hasMore, present, func(last I) (string, error) {
		userID, id := key(last)
		return encodeOpaqueCursor(membershipPageCursor{ScopeType: scope.Type, ScopeID: scope.ID, ActiveAt: &activeAt, AfterUserID: userID, AfterID: id}, membershipPageCursorSpec())
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response), nil
}
