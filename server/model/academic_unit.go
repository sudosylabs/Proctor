// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

// AcademicUnit is one node in the institution's organizational tree, such as a
// college, school, or another institution-defined unit. ParentID is zero for a
// root unit and points to another AcademicUnit for a nested unit.
type AcademicUnit struct {
	ID            AcademicUnitID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ArchivedAt    OptionalTime
	Revision      int64
	InstitutionID InstitutionID
	ParentID      AcademicUnitID // zero for root units
	Name          string
	DisplayName   string
	Description   string
}

// NewAcademicUnit constructs a unit with application-supplied identity and clock.
func NewAcademicUnit(
	id AcademicUnitID,
	institutionID InstitutionID,
	parentID AcademicUnitID,
	name, displayName, description string,
	at time.Time,
) (*AcademicUnit, error) {
	at = TimeUTC(at)
	unit := &AcademicUnit{
		ID:            id,
		CreatedAt:     at,
		UpdatedAt:     at,
		Revision:      1,
		InstitutionID: institutionID,
		ParentID:      parentID,
		Name:          name,
		DisplayName:   displayName,
		Description:   description,
	}
	sanitizeNamed(&unit.Name, &unit.DisplayName, &unit.Description)
	if err := unit.Validate(); err != nil {
		return nil, err
	}
	return unit, nil
}

// PrepareCreate applies explicit identity and time chosen by the application.
func (au *AcademicUnit) PrepareCreate(id AcademicUnitID, at time.Time) {
	if au == nil {
		return
	}
	au.ID = id
	at = TimeUTC(at)
	au.CreatedAt = at
	au.UpdatedAt = at
	au.ArchivedAt = OptionalTime{}
	if au.Revision <= 0 {
		au.Revision = 1
	}
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (au *AcademicUnit) PrepareUpdate(at time.Time) {
	if au == nil {
		return
	}
	au.UpdatedAt = TimeUTC(at)
	if au.Revision <= 0 {
		au.Revision = 1
	}
	au.Revision++
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

// Update mutates profile/parent fields with revision increment.
func (au *AcademicUnit) Update(
	expectedRevision int64,
	parentID AcademicUnitID,
	name, displayName, description string,
	at time.Time,
) error {
	if au == nil {
		return fmt.Errorf("model: academic unit is nil")
	}
	if au.IsArchived() {
		return fmt.Errorf("model: academic unit is archived")
	}
	if expectedRevision != 0 && au.Revision != expectedRevision {
		return fmt.Errorf("model: academic unit revision conflict")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: academic unit update time is required")
	}
	au.ParentID = parentID
	au.Name = name
	au.DisplayName = displayName
	au.Description = description
	au.UpdatedAt = at
	if expectedRevision != 0 || au.Revision > 0 {
		au.Revision++
	} else {
		au.Revision = 1
	}
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
	return au.Validate()
}

// Archive marks the unit archived.
func (au *AcademicUnit) Archive(at time.Time) error {
	if au == nil {
		return fmt.Errorf("model: academic unit is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: academic unit archive time is required")
	}
	if au.IsArchived() {
		return fmt.Errorf("model: academic unit is already archived")
	}
	au.ArchivedAt = OptionalTimeFrom(at)
	au.UpdatedAt = at
	au.Revision++
	return au.Validate()
}

// IsArchived reports whether the unit is archived.
func (au *AcademicUnit) IsArchived() bool {
	return au != nil && au.ArchivedAt.Valid
}

// Validate checks rehydrated academic-unit state.
func (au *AcademicUnit) Validate() error {
	const where = "AcademicUnit.Validate"
	if au == nil {
		return invalidModelError(where, "academic_unit", "value", "is required", "")
	}
	if !au.ID.IsValid() {
		return invalidModelError(where, "academic_unit", "id", "must be a valid identifier", "")
	}
	details := "id=" + au.ID.String()
	if au.CreatedAt.IsZero() {
		return invalidModelError(where, "academic_unit", "created_at", "must be set", details)
	}
	if au.UpdatedAt.IsZero() {
		return invalidModelError(where, "academic_unit", "updated_at", "must be set", details)
	}
	if au.UpdatedAt.Before(au.CreatedAt) {
		return invalidModelError(where, "academic_unit", "updated_at", "must not precede created_at", details)
	}
	if !au.InstitutionID.IsValid() {
		return invalidModelError(where, "academic_unit", "institution_id", "must be a valid identifier", details)
	}
	if !au.ParentID.IsZero() && !au.ParentID.IsValid() {
		return invalidModelError(where, "academic_unit", "parent_id", "must be empty or a valid identifier", details)
	}
	if !au.ParentID.IsZero() && au.ParentID == au.ID {
		return invalidModelError(where, "academic_unit", "parent_id", "must not reference the unit itself", details)
	}
	if au.Revision < 0 {
		return invalidModelError(where, "academic_unit", "revision", "must not be negative", details)
	}
	if au.ArchivedAt.Valid && au.ArchivedAt.Time.Before(au.CreatedAt) {
		return invalidModelError(where, "academic_unit", "archived_at", "must not precede created_at", details)
	}
	return validateNamed(where, "academic_unit", au.ID.String(), au.Name, au.DisplayName, au.Description)
}

// Auditable returns a deliberately safe audit projection.
func (au *AcademicUnit) Auditable() map[string]any {
	if au == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             au.ID.String(),
		"created_at":     MillisFromTime(au.CreatedAt),
		"updated_at":     MillisFromTime(au.UpdatedAt),
		"archived_at":    au.ArchivedAt.Millis(),
		"revision":       au.Revision,
		"institution_id": au.InstitutionID.String(),
		"parent_id":      au.ParentID.String(),
		"name":           au.Name,
		"display_name":   au.DisplayName,
	}
}

// ResourceID returns the string form used by authorization Resource contracts.
func (au *AcademicUnit) ResourceID() string {
	if au == nil {
		return ""
	}
	return au.ID.String()
}

var _ Auditable = (*AcademicUnit)(nil)
