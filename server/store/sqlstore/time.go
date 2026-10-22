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

// latestTime returns the later of a row's persisted modification time and a
// requested mutation instant, matching SQL GREATEST(updated_at, ?) so a
// returned model reports the stored value.
func latestTime(persisted, requested time.Time) time.Time {
	if requested.After(persisted) {
		return UTCTime(requested)
	}
	return UTCTime(persisted)
}

// transitionInstant returns the millisecond instant recorded for one
// PostgreSQL-timed transition. PostgreSQL time still makes every validity
// decision, but rows created by a node whose clock leads the database must not
// record a transition that precedes their own lifecycle, so the instant is
// raised to the latest such floor, rounded up to the next millisecond.
func transitionInstant(databaseNow time.Time, floors ...time.Time) time.Time {
	at := UTCTime(databaseNow).Truncate(time.Millisecond)
	for _, floor := range floors {
		floor = UTCTime(floor)
		if floor.After(at) {
			at = floor.Truncate(time.Millisecond)
			if at.Before(floor) {
				at = at.Add(time.Millisecond)
			}
		}
	}
	return at
}
