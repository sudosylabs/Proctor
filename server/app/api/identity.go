// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// HTTP/wire conversion helpers for entity-specific IDs and native times.
// Transport DTOs keep string/RFC 3339 wire shapes; application and domain
// contracts receive typed IDs and UTC time.Time during expand-contract.

// ParsePathUserID parses a route or request user identifier.
func ParsePathUserID(value string) (model.UserID, error) {
	return model.ParseUserID(value)
}

// ParsePathInstitutionID parses a route institution identifier.
func ParsePathInstitutionID(value string) (model.InstitutionID, error) {
	return model.ParseInstitutionID(value)
}

// ParsePathAcademicUnitID parses a route academic-unit identifier.
func ParsePathAcademicUnitID(value string) (model.AcademicUnitID, error) {
	return model.ParseAcademicUnitID(value)
}

// ParsePathProgrammeID parses a route programme identifier.
func ParsePathProgrammeID(value string) (model.ProgrammeID, error) {
	return model.ParseProgrammeID(value)
}

// ParsePathProgrammeLevelID parses a route programme-level identifier.
func ParsePathProgrammeLevelID(value string) (model.ProgrammeLevelID, error) {
	return model.ParseProgrammeLevelID(value)
}

// ParsePathClassID parses a route class identifier.
func ParsePathClassID(value string) (model.ClassID, error) {
	return model.ParseClassID(value)
}

// ParsePathAcademicPeriodID parses a route academic-period identifier.
func ParsePathAcademicPeriodID(value string) (model.AcademicPeriodID, error) {
	return model.ParseAcademicPeriodID(value)
}

// ParsePathSessionID parses a route session identifier.
func ParsePathSessionID(value string) (model.SessionID, error) {
	return model.ParseSessionID(value)
}

// ParsePathRoleID parses a route role identifier.
func ParsePathRoleID(value string) (model.RoleID, error) {
	return model.ParseRoleID(value)
}

// ParsePathRoleBindingID parses a route role-binding identifier.
func ParsePathRoleBindingID(value string) (model.RoleBindingID, error) {
	return model.ParseRoleBindingID(value)
}

// WireID returns the stable string form for response DTOs.
func WireID[T ~string](id T) string {
	return string(id)
}

// FormatTimeRFC3339 encodes a UTC instant for JSON responses. Zero times yield
// an empty string so legacy omitempty fields remain omitted until DTOs switch
// to native nullable times.
func FormatTimeRFC3339(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// ParseTimeRFC3339 parses an HTTP request timestamp into UTC.
func ParseTimeRFC3339(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("api: time is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("api: invalid time %q: %w", value, err)
		}
	}
	return parsed.UTC(), nil
}

// FormatOptionalTimeRFC3339 encodes OptionalTime for JSON (null when absent).
func FormatOptionalTimeRFC3339(value model.OptionalTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

// LegacyMillisToRFC3339 converts a legacy millisecond field for transitional
// response DTOs that already expose RFC 3339 while stores still use millis.
func LegacyMillisToRFC3339(millis int64) string {
	return FormatTimeRFC3339(model.TimeFromMillis(millis))
}

// RFC3339ToLegacyMillis converts a request RFC 3339 string to legacy millis.
// Empty input yields 0.
func RFC3339ToLegacyMillis(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := ParseTimeRFC3339(value)
	if err != nil {
		return 0, err
	}
	return model.MillisFromTime(parsed), nil
}
