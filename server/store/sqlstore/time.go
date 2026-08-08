// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// Time SQL adapters bridge legacy integer-millisecond columns and future
// timestamptz columns without forcing a global aggregate migration.

// NullMillis is a nullable integer-millisecond timestamp column.
type NullMillis struct {
	Millis int64
	Valid  bool
}

// Scan implements sql.Scanner for int64 millisecond columns.
func (n *NullMillis) Scan(value any) error {
	if n == nil {
		return fmt.Errorf("sqlstore: NullMillis scan target is nil")
	}
	if value == nil {
		n.Millis = 0
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case int64:
		n.Millis = v
		n.Valid = true
	case int32:
		n.Millis = int64(v)
		n.Valid = true
	case int:
		n.Millis = int64(v)
		n.Valid = true
	default:
		return fmt.Errorf("sqlstore: cannot scan %T into millis", value)
	}
	return nil
}

// Value implements driver.Valuer. Absent and non-positive values become SQL NULL
// when mapping optional lifecycle fields; required columns should use MillisValue.
func (n NullMillis) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Millis, nil
}

// MillisValue encodes a required millisecond instant.
func MillisValue(millis int64) (driver.Value, error) {
	return millis, nil
}

// TimeValue encodes a UTC time for timestamptz columns. Zero times become NULL.
func TimeValue(value time.Time) (driver.Value, error) {
	if value.IsZero() {
		return nil, nil
	}
	return value.UTC(), nil
}

// ScanTimeUTC scans a timestamptz or time.Time column into UTC.
func ScanTimeUTC(value any) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	switch v := value.(type) {
	case time.Time:
		return v.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("sqlstore: cannot scan %T into time", value)
	}
}

// OptionalTimeFromMillisColumn maps a legacy millisecond column to OptionalTime.
// Non-positive values are absent (delete_at = 0 expand-contract mapping).
func OptionalTimeFromMillisColumn(millis int64) model.OptionalTime {
	return model.OptionalTimeFromMillis(millis)
}

// MillisFromOptionalTime maps OptionalTime onto a legacy millisecond column.
func MillisFromOptionalTime(value model.OptionalTime) int64 {
	return value.Millis()
}

// NullTimeFromOptional maps OptionalTime onto database/sql.NullTime.
func NullTimeFromOptional(value model.OptionalTime) sql.NullTime {
	if !value.Valid {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.Time.UTC(), Valid: true}
}

// OptionalTimeFromNullTime maps sql.NullTime onto OptionalTime.
func OptionalTimeFromNullTime(value sql.NullTime) model.OptionalTime {
	if !value.Valid {
		return model.OptionalTime{}
	}
	return model.OptionalTimeFrom(value.Time)
}

var (
	_ sql.Scanner   = (*NullMillis)(nil)
	_ driver.Valuer = NullMillis{}
)
