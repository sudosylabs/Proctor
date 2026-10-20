// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/model"
)

func queryActiveAt(w http.ResponseWriter, r *http.Request) (int64, bool) {
	activeAt, err := parseActiveAt(r)
	if err != nil {
		WriteError(w, r, err)
		return 0, false
	}
	return activeAt, true
}

func parseActiveAt(r *http.Request) (int64, error) {
	history := r.URL.Query().Get("history")
	if history != "" {
		if history != "true" && history != "false" {
			return 0, invalidRequestError(nil)
		}
		if history == "true" {
			return 0, nil
		}
	}
	value := r.URL.Query().Get("active_at")
	if value == "" {
		return model.GetMillis(), nil
	}
	activeAt, err := model.ParseRFC3339(value)
	if err != nil || activeAt.IsZero() {
		return 0, invalidRequestError(err)
	}
	return model.MillisFromTime(activeAt), nil
}

func usesMembershipPaging(request *http.Request) bool {
	values := request.URL.Query()
	return values.Has("limit") || values.Has("cursor")
}

func membershipPageQuery(request *http.Request, scope model.Resource) (application.MembershipPageQuery, membershipPageCursor, error) {
	values := request.URL.Query()
	query := application.MembershipPageQuery{Limit: 50}
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

func membershipPageHeaders(request *http.Request, limit int, cursor *membershipPageCursor) (http.Header, error) {
	headers := http.Header{}
	if cursor == nil {
		return headers, nil
	}
	encoded, err := encodeOpaqueCursor(*cursor, membershipPageCursorSpec())
	if err != nil {
		return nil, err
	}
	values := url.Values{"limit": {strconv.Itoa(limit)}, "cursor": {encoded}}
	if *cursor.ActiveAt == 0 {
		values.Set("history", "true")
	} else {
		values.Set("active_at", wireTimeFromMillis(*cursor.ActiveAt))
	}
	next := url.URL{Path: request.URL.Path, RawQuery: values.Encode()}
	headers.Set("Link", "<"+next.String()+">; rel=\"next\"")
	return headers, nil
}
