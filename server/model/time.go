// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Time helpers normalize domain and application instants to UTC.
// Millisecond conversions exist only for compatibility at explicit transport
// boundaries; durable domain and SQL contracts use native temporal types.

// TimeUTC returns value in UTC. A zero time remains zero.
func TimeUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

// NowUTC returns the current instant in UTC. Prefer injecting clocks into
// application services; this helper exists for pure domain tests and adapters.
func NowUTC() time.Time {
	return time.Now().UTC()
}

// MillisFromTime converts a time to Unix milliseconds for legacy wire fields.
// A zero time maps to 0 at that boundary.
func MillisFromTime(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

// TimeFromMillis converts a legacy wire millisecond value to UTC. A
// non-positive boundary value maps to the zero time.
func TimeFromMillis(millis int64) time.Time {
	if millis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}

// OptionalTime is an explicit optional UTC instant for meaning-specific
// lifecycle fields such as archived_at. Valid is false for the absent state;
// a zero Time with Valid true is still a valid (if unusual) recorded instant.
type OptionalTime struct {
	Time  time.Time
	Valid bool
}

// IsSet reports whether the optional instant is present.
func (o OptionalTime) IsSet() bool { return o.Valid }

// OptionalTimeFrom wraps a concrete time as present. Zero times are still
// treated as present when callers need to distinguish from omission; prefer
// OptionalTime{} for absence.
func OptionalTimeFrom(value time.Time) OptionalTime {
	return OptionalTime{Time: value.UTC(), Valid: true}
}

// OptionalTimeFromMillis maps a legacy wire value. Non-positive milliseconds
// become the absent optional value.
func OptionalTimeFromMillis(millis int64) OptionalTime {
	if millis <= 0 {
		return OptionalTime{}
	}
	return OptionalTime{Time: time.UnixMilli(millis).UTC(), Valid: true}
}

// Millis returns the Unix-millisecond form used by legacy wire contracts.
// Absent values return 0 at that boundary.
func (o OptionalTime) Millis() int64 {
	if !o.Valid {
		return 0
	}
	return o.Time.UTC().UnixMilli()
}

// UTC returns the optional instant normalized to UTC.
func (o OptionalTime) UTC() OptionalTime {
	if !o.Valid {
		return OptionalTime{}
	}
	return OptionalTime{Time: o.Time.UTC(), Valid: true}
}

// IsZero reports absence or a present zero time.
func (o OptionalTime) IsZero() bool {
	return !o.Valid || o.Time.IsZero()
}

// Ptr returns a *time.Time for SQL/DTO nullability helpers. Absent values yield nil.
func (o OptionalTime) Ptr() *time.Time {
	if !o.Valid {
		return nil
	}
	value := o.Time.UTC()
	return &value
}

// OptionalTimeFromPtr maps a nullable pointer. Nil becomes absent.
func OptionalTimeFromPtr(value *time.Time) OptionalTime {
	if value == nil {
		return OptionalTime{}
	}
	return OptionalTimeFrom(*value)
}

// MarshalJSON encodes present times as RFC 3339 UTC strings and absence as null.
func (o OptionalTime) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.Time.UTC().Format(time.RFC3339Nano))
}

// UnmarshalJSON accepts RFC 3339 strings, null, or empty for absence.
func (o *OptionalTime) UnmarshalJSON(data []byte) error {
	if o == nil {
		return fmt.Errorf("model: OptionalTime unmarshal target is nil")
	}
	if string(data) == "null" || string(data) == `""` {
		*o = OptionalTime{}
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		// Also accept bare RFC 3339 without going through string if needed.
		var instant time.Time
		if err2 := json.Unmarshal(data, &instant); err2 != nil {
			return fmt.Errorf("model: invalid optional time: %w", err)
		}
		*o = OptionalTimeFrom(instant)
		return nil
	}
	if raw == "" {
		*o = OptionalTime{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return fmt.Errorf("model: invalid optional time %q: %w", raw, err)
		}
	}
	*o = OptionalTimeFrom(parsed)
	return nil
}

// FormatRFC3339 returns the present instant as RFC 3339 UTC, or "" when absent.
func (o OptionalTime) FormatRFC3339() string {
	if !o.Valid {
		return ""
	}
	return o.Time.UTC().Format(time.RFC3339Nano)
}
