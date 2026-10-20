// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

// Every HTTP timestamp, in bodies and query parameters alike, is an RFC 3339
// string. Responses use model.FormatRFC3339 (UTC, microsecond precision,
// trailing fractional zeros removed) and omit an absent optional instant.
// Requests accept any RFC 3339 offset and fractional precision through the
// helpers below.

// optionalWireTime formats an instant whose zero value means absent; pair it
// with an omitempty field.
func optionalWireTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return model.FormatRFC3339(value)
}

// wireTimeFromMillis formats an application result that still carries Unix
// milliseconds. A non-positive value is absent and formats as "".
func wireTimeFromMillis(millis int64) string {
	return optionalWireTime(model.TimeFromMillis(millis))
}

// parseWireTime parses a required RFC 3339 request timestamp named field.
func parseWireTime(field, raw string) (time.Time, error) {
	parsed, err := model.ParseRFC3339(raw)
	if err != nil || parsed.IsZero() {
		out := apperror.New("request.invalid").WithField("field", field)
		if err != nil {
			out = out.Wrap(err)
		}
		return time.Time{}, out
	}
	return parsed, nil
}

// wireTimeMillis parses an RFC 3339 request timestamp for an application
// command that still carries Unix milliseconds. An empty value is absent and
// maps to 0; precision below one millisecond is discarded.
func wireTimeMillis(field, raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	parsed, err := parseWireTime(field, raw)
	if err != nil {
		return 0, err
	}
	return model.MillisFromTime(parsed), nil
}

// optionalWireTimeMillis is wireTimeMillis for a PATCH field: absent or null
// yields nil.
func optionalWireTimeMillis(field string, value Optional[string]) (*int64, error) {
	raw := value.ValuePointer()
	if raw == nil {
		return nil, nil
	}
	millis, err := wireTimeMillis(field, *raw)
	if err != nil {
		return nil, err
	}
	if *raw == "" {
		return nil, apperror.New("request.invalid").WithField("field", field)
	}
	return &millis, nil
}

// wireWindowMillis parses an optional start_at and end_at request pair for an
// application command that still carries Unix milliseconds.
func wireWindowMillis(startAt, endAt string) (int64, int64, error) {
	start, err := wireTimeMillis("start_at", startAt)
	if err != nil {
		return 0, 0, err
	}
	end, err := wireTimeMillis("end_at", endAt)
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}
