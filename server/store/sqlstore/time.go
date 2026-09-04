// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// Time SQL adapters map domain time.Time / OptionalTime onto PostgreSQL
// timestamptz columns (nullable via sql.NullTime).

// NullTimeFromOptional maps OptionalTime onto database/sql.NullTime.
func NullTimeFromOptional(value model.OptionalTime) sql.NullTime {
	if !value.Valid {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: model.TimeUTC(value.Time), Valid: true}
}

// OptionalTimeFromNullTime maps sql.NullTime onto OptionalTime.
func OptionalTimeFromNullTime(value sql.NullTime) model.OptionalTime {
	if !value.Valid {
		return model.OptionalTime{}
	}
	return model.OptionalTimeFrom(value.Time)
}

// NullTimeFromTime maps a concrete time onto sql.NullTime. Zero becomes NULL.
func NullTimeFromTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: model.TimeUTC(value), Valid: true}
}

// TimeFromNullTime maps sql.NullTime onto time.Time. NULL becomes zero.
func TimeFromNullTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return model.TimeUTC(value.Time)
}

// UTCTime normalizes a non-zero time to UTC microseconds for row writes.
func UTCTime(value time.Time) time.Time {
	return model.TimeUTC(value)
}
