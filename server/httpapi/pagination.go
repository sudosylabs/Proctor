// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
)

// Every collection uses one envelope and one pair of query parameters:
// `limit` (1..maximumPageLimit, default defaultPageLimit) and an opaque
// `cursor` returned unchanged from the previous page's `next_cursor`, which is
// omitted on the last page.
const (
	defaultPageLimit = 50
	maximumPageLimit = 200
)

// listResponse is the one collection envelope.
type listResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// pageRequest is the parsed `limit` and raw `cursor` of a collection read.
type pageRequest struct {
	Limit  int
	Cursor string
}

// pageRequest parses the collection parameters. A repeated, empty, or
// out-of-range value is invalid.
func (r operationRequest) pageRequest() (pageRequest, error) {
	values := r.request.URL.Query()
	page := pageRequest{Limit: defaultPageLimit}
	for _, name := range []string{"limit", "cursor"} {
		if entries, present := values[name]; present && (len(entries) != 1 || entries[0] == "") {
			return pageRequest{}, invalidRequestError(errors.New(name + " must be one non-empty value"))
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maximumPageLimit {
			return pageRequest{}, invalidRequestError(errors.New("limit must be between 1 and 200"))
		}
		page.Limit = limit
	}
	page.Cursor = values.Get("cursor")
	return page, nil
}

// decodePageCursor decodes the request's cursor with spec, or reports false
// on the first page.
func decodePageCursor[T any](page pageRequest, spec opaqueCursorSpec[T]) (T, bool, error) {
	var zero T
	if page.Cursor == "" {
		return zero, false, nil
	}
	cursor, err := decodeOpaqueCursor(page.Cursor, spec)
	if err != nil {
		return zero, false, invalidRequestError(err)
	}
	return cursor, true, nil
}

// newListResponse maps one page of items into the envelope. When hasMore is
// set, next encodes the continuation after the page's last item; a page that
// claims more without items made no progress and cannot be presented.
func newListResponse[I, T any](items []I, hasMore bool, present func(I) T, next func(I) (string, error)) (listResponse[T], error) {
	response := listResponse[T]{Items: make([]T, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, present(item))
	}
	if !hasMore {
		return response, nil
	}
	if len(items) == 0 {
		return listResponse[T]{}, unpresentableResult(errors.New("collection page made no progress"))
	}
	cursor, err := next(items[len(items)-1])
	if err != nil {
		return listResponse[T]{}, unpresentableResult(err)
	}
	response.NextCursor = cursor
	return response, nil
}

// boundedPageKey is one item's position in a complete bounded collection
// (a User's Sessions, the Institution's Roles) that the application returns
// whole. Such a collection is still paged on the wire, as keyset slices of
// its stable order: newest first by At then ID, or ascending by Name then ID.
type boundedPageKey struct {
	At   time.Time
	Name string
	ID   string
}

// boundedPageCursor is the continuation of a bounded collection page.
type boundedPageCursor struct {
	At   string `json:"at,omitempty"`
	Name string `json:"name,omitempty"`
	ID   string `json:"id"`
}

func boundedPageCursorSpec(label string) opaqueCursorSpec[boundedPageCursor] {
	return opaqueCursorSpec[boundedPageCursor]{
		label: label, maximumEncodedLength: 2048,
		members: []string{"at", "name", "id"},
		validate: func(cursor boundedPageCursor) error {
			if cursor.ID == "" || len(cursor.ID) > 128 || !utf8.ValidString(cursor.ID) || len(cursor.Name) > 1024 || !utf8.ValidString(cursor.Name) || (cursor.At == "") == (cursor.Name == "") {
				return errors.New("invalid " + label + " keyset")
			}
			if cursor.At != "" {
				if at, err := model.ParseRFC3339(cursor.At); err != nil || at.IsZero() {
					return errors.New("invalid " + label + " keyset")
				}
			}
			return nil
		},
	}
}

func (k boundedPageKey) cursor() boundedPageCursor {
	if k.Name != "" {
		return boundedPageCursor{Name: k.Name, ID: k.ID}
	}
	return boundedPageCursor{At: model.FormatRFC3339(k.At), ID: k.ID}
}

func (c boundedPageCursor) key() boundedPageKey {
	at, _ := model.ParseRFC3339(c.At)
	return boundedPageKey{At: at, Name: c.Name, ID: c.ID}
}

// compareBoundedPageKeys orders name keys ascending and instant keys newest
// first, identity breaking ties in the same direction.
func compareBoundedPageKeys(a, b boundedPageKey) int {
	if a.Name != "" || b.Name != "" {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	}
	if order := b.At.Compare(a.At); order != 0 {
		return order
	}
	return strings.Compare(b.ID, a.ID)
}

// boundedListResult pages a complete bounded collection by the request's
// limit and cursor and presents the page in the collection envelope.
func boundedListResult[I, T any](request operationRequest, label string, items []I, key func(I) boundedPageKey, present func(I) T) (listResponse[T], error) {
	page, err := request.pageRequest()
	if err != nil {
		return listResponse[T]{}, err
	}
	spec := boundedPageCursorSpec(label)
	cursor, continued, err := decodePageCursor(page, spec)
	if err != nil {
		return listResponse[T]{}, err
	}
	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b I) int { return compareBoundedPageKeys(key(a), key(b)) })
	start := 0
	if continued {
		pivot := cursor.key()
		start, _ = slices.BinarySearchFunc(ordered, pivot, func(item I, target boundedPageKey) int {
			return compareBoundedPageKeys(key(item), target)
		})
		if start < len(ordered) && compareBoundedPageKeys(key(ordered[start]), pivot) == 0 {
			start++
		}
	}
	ordered = ordered[start:]
	hasMore := len(ordered) > page.Limit
	if hasMore {
		ordered = ordered[:page.Limit]
	}
	return newListResponse(ordered, hasMore, present, func(last I) (string, error) {
		return encodeOpaqueCursor(key(last).cursor(), spec)
	})
}
