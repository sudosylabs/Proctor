// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

// Institution is the university or school represented by one logical Proctor
// installation. Its academic organization begins at its root AcademicUnit
// records; programmes and classes are reached through those units.
//
// Domain time is UTC time.Time. Optional archive uses OptionalTime. Revision
// supports optimistic concurrency on profile updates.
type Institution struct {
	ID           InstitutionID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   OptionalTime
	Revision     int64
	Name         string
	DisplayName  string
	Description  string
	ExamCapacity ExamCapacityPolicy
}

// NewInstitution constructs a new institution with application-supplied identity
// and clock. It does not persist.
func NewInstitution(
	id InstitutionID,
	name, displayName, description string,
	at time.Time,
) (*Institution, error) {
	if !id.IsValid() {
		return nil, fmt.Errorf("model: institution id is invalid")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return nil, fmt.Errorf("model: institution create time is required")
	}
	institution := &Institution{
		ID:           id,
		CreatedAt:    at,
		UpdatedAt:    at,
		Revision:     1,
		Name:         name,
		DisplayName:  displayName,
		Description:  description,
		ExamCapacity: DefaultExamCapacityPolicy(),
	}
	sanitizeNamed(&institution.Name, &institution.DisplayName, &institution.Description)
	if err := institution.Validate(); err != nil {
		return nil, err
	}
	return institution, nil
}

// UpdateProfile applies a profile change with the expected revision and clock.
func (i *Institution) UpdateProfile(
	expectedRevision int64,
	name, displayName, description string,
	at time.Time,
) error {
	if i == nil {
		return fmt.Errorf("model: institution is nil")
	}
	if i.IsArchived() {
		return fmt.Errorf("model: institution is archived")
	}
	if expectedRevision != 0 && i.Revision != expectedRevision {
		return fmt.Errorf("model: institution revision conflict")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: institution update time is required")
	}
	if at.Before(i.CreatedAt) {
		return fmt.Errorf("model: institution update time precedes create time")
	}
	i.Name = name
	i.DisplayName = displayName
	i.Description = description
	i.UpdatedAt = at
	if expectedRevision != 0 || i.Revision > 0 {
		i.Revision++
	} else {
		i.Revision = 1
	}
	sanitizeNamed(&i.Name, &i.DisplayName, &i.Description)
	return i.Validate()
}

// PrepareUpdate is a transitional helper for application code still patching
// fields before a full command rewrite. Prefer UpdateProfile.
func (i *Institution) PrepareUpdate(at time.Time) {
	if i == nil {
		return
	}
	i.UpdatedAt = TimeUTC(at)
	if i.Revision <= 0 {
		i.Revision = 1
	}
	i.Revision++
	sanitizeNamed(&i.Name, &i.DisplayName, &i.Description)
}

// Archive marks the institution archived. Installations normally do not archive
// the singleton institution; the method exists for domain completeness.
func (i *Institution) Archive(at time.Time) error {
	if i == nil {
		return fmt.Errorf("model: institution is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: institution archive time is required")
	}
	if i.IsArchived() {
		return fmt.Errorf("model: institution is already archived")
	}
	i.ArchivedAt = OptionalTimeFrom(at)
	i.UpdatedAt = at
	i.Revision++
	return i.Validate()
}

// IsArchived reports whether the institution is archived.
func (i *Institution) IsArchived() bool {
	return i != nil && i.ArchivedAt.Valid
}

// Validate checks rehydrated institution state.
func (i *Institution) Validate() error {
	const where = "Institution.Validate"
	if i == nil {
		return invalidModelError(where, "institution", "value", "is required", "")
	}
	if !i.ID.IsValid() {
		return invalidModelError(where, "institution", "id", "must be a valid identifier", "")
	}
	details := "id=" + i.ID.String()
	if i.CreatedAt.IsZero() {
		return invalidModelError(where, "institution", "created_at", "must be set", details)
	}
	if i.UpdatedAt.IsZero() {
		return invalidModelError(where, "institution", "updated_at", "must be set", details)
	}
	if i.UpdatedAt.Before(i.CreatedAt) {
		return invalidModelError(where, "institution", "updated_at", "must not precede created_at", details)
	}
	if i.Revision < 0 {
		return invalidModelError(where, "institution", "revision", "must not be negative", details)
	}
	if i.ArchivedAt.Valid && i.ArchivedAt.Time.Before(i.CreatedAt) {
		return invalidModelError(where, "institution", "archived_at", "must not precede created_at", details)
	}
	if err := i.ExamCapacity.Validate(); err != nil {
		return invalidModelError(where, "institution", "exam_capacity", err.Error(), details)
	}
	return validateNamed(where, "institution", i.ID.String(), i.Name, i.DisplayName, i.Description)
}

// Auditable returns a deliberately safe audit projection.
func (i *Institution) Auditable() map[string]any {
	if i == nil {
		return map[string]any{}
	}
	fields := map[string]any{
		"id":            i.ID.String(),
		"created_at":    MillisFromTime(i.CreatedAt),
		"updated_at":    MillisFromTime(i.UpdatedAt),
		"archived_at":   i.ArchivedAt.Millis(),
		"revision":      i.Revision,
		"name":          i.Name,
		"display_name":  i.DisplayName,
		"exam_capacity": i.ExamCapacity,
	}
	return fields
}

// ResourceID returns the string form used by authorization Resource contracts.
func (i *Institution) ResourceID() string {
	if i == nil {
		return ""
	}
	return i.ID.String()
}

var _ Auditable = (*Institution)(nil)
