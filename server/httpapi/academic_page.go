// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

// academicNameCursor continues an academic listing ordered by name then
// identity.
type academicNameCursor struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func academicNameCursorSpec() opaqueCursorSpec[academicNameCursor] {
	return opaqueCursorSpec[academicNameCursor]{
		label: "academic", maximumEncodedLength: 1024,
		members: []string{"name", "id"},
		validate: func(cursor academicNameCursor) error {
			if cursor.Name == "" || len(cursor.Name) > 256 || !utf8.ValidString(cursor.Name) || !model.IsValidOpaqueID(cursor.ID) {
				return errors.New("invalid academic keyset")
			}
			return nil
		},
	}
}

// academicNamePageQuery parses the page parameters of a name-ordered
// academic listing.
func (r operationRequest) academicNamePageQuery() (app.NamePageQuery, error) {
	page, err := r.pageRequest()
	if err != nil {
		return app.NamePageQuery{}, err
	}
	cursor, present, err := decodePageCursor(page, academicNameCursorSpec())
	if err != nil {
		return app.NamePageQuery{}, err
	}
	query := app.NamePageQuery{Limit: page.Limit}
	if present {
		query.AfterName, query.AfterID = cursor.Name, cursor.ID
	}
	return query, nil
}

// academicNameListResult presents one name-ordered page.
func academicNameListResult[I, T any](page app.Page[I], present func(I) T, key func(I) (string, string)) (operationResult, error) {
	response, err := newListResponse(page.Items, page.HasMore, present, func(last I) (string, error) {
		name, id := key(last)
		return encodeOpaqueCursor(academicNameCursor{Name: name, ID: id}, academicNameCursorSpec())
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response), nil
}

// academicPeriodCursor continues the Academic Period listing ordered by start,
// name, then identity.
type academicPeriodCursor struct {
	StartAt string `json:"start_at"`
	Name    string `json:"name"`
	ID      string `json:"id"`
}

func academicPeriodCursorSpec() opaqueCursorSpec[academicPeriodCursor] {
	return opaqueCursorSpec[academicPeriodCursor]{
		label: "academic period", maximumEncodedLength: 1024,
		members: []string{"start_at", "name", "id"},
		validate: func(cursor academicPeriodCursor) error {
			startAt, err := model.ParseRFC3339(cursor.StartAt)
			if err != nil || startAt.IsZero() || cursor.Name == "" || len(cursor.Name) > 256 ||
				!utf8.ValidString(cursor.Name) || !model.AcademicPeriodID(cursor.ID).IsValid() {
				return errors.New("invalid academic period keyset")
			}
			return nil
		},
	}
}

func academicPeriodCursorStart(cursor academicPeriodCursor) time.Time {
	startAt, _ := model.ParseRFC3339(cursor.StartAt)
	return startAt
}
