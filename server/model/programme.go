// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

// Programme is a course of study owned by one AcademicUnit, such as Bachelor
// of Computer Science. It describes the curriculum independently of academic
// years and concrete student rosters.
type Programme struct {
	ID             ProgrammeID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ArchivedAt     OptionalTime
	Revision       int64
	AcademicUnitID AcademicUnitID
	Name           string
	DisplayName    string
	Description    string
}

// NewProgramme constructs a programme with application-supplied identity and clock.
func NewProgramme(
	id ProgrammeID,
	academicUnitID AcademicUnitID,
	name, displayName, description string,
	at time.Time,
) (*Programme, error) {
	at = TimeUTC(at)
	programme := &Programme{
		ID:             id,
		CreatedAt:      at,
		UpdatedAt:      at,
		Revision:       1,
		AcademicUnitID: academicUnitID,
		Name:           name,
		DisplayName:    displayName,
		Description:    description,
	}
	sanitizeNamed(&programme.Name, &programme.DisplayName, &programme.Description)
	if err := programme.Validate(); err != nil {
		return nil, err
	}
	return programme, nil
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (p *Programme) PrepareCreate(id ProgrammeID, at time.Time) {
	if p == nil {
		return
	}
	p.ID = id
	at = TimeUTC(at)
	p.CreatedAt = at
	p.UpdatedAt = at
	p.ArchivedAt = OptionalTime{}
	if p.Revision <= 0 {
		p.Revision = 1
	}
	sanitizeNamed(&p.Name, &p.DisplayName, &p.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (p *Programme) PrepareUpdate(at time.Time) {
	if p == nil {
		return
	}
	p.UpdatedAt = TimeUTC(at)
	if p.Revision <= 0 {
		p.Revision = 1
	}
	p.Revision++
	sanitizeNamed(&p.Name, &p.DisplayName, &p.Description)
}

// Archive marks the programme archived.
func (p *Programme) Archive(at time.Time) error {
	if p == nil {
		return fmt.Errorf("model: programme is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: programme archive time is required")
	}
	if p.IsArchived() {
		return fmt.Errorf("model: programme is already archived")
	}
	p.ArchivedAt = OptionalTimeFrom(at)
	p.UpdatedAt = at
	p.Revision++
	return p.Validate()
}

// IsArchived reports whether the programme is archived.
func (p *Programme) IsArchived() bool {
	return p != nil && p.ArchivedAt.Valid
}

// Validate checks rehydrated programme state.
func (p *Programme) Validate() error {
	const where = "Programme.Validate"
	if p == nil {
		return invalidModelError(where, "programme", "value", "is required", "")
	}
	if !p.ID.IsValid() {
		return invalidModelError(where, "programme", "id", "must be a valid identifier", "")
	}
	details := "id=" + p.ID.String()
	if p.CreatedAt.IsZero() {
		return invalidModelError(where, "programme", "created_at", "must be set", details)
	}
	if p.UpdatedAt.IsZero() {
		return invalidModelError(where, "programme", "updated_at", "must be set", details)
	}
	if p.UpdatedAt.Before(p.CreatedAt) {
		return invalidModelError(where, "programme", "updated_at", "must not precede created_at", details)
	}
	if !p.AcademicUnitID.IsValid() {
		return invalidModelError(where, "programme", "academic_unit_id", "must be a valid identifier", details)
	}
	if p.Revision < 0 {
		return invalidModelError(where, "programme", "revision", "must not be negative", details)
	}
	if p.ArchivedAt.Valid && p.ArchivedAt.Time.Before(p.CreatedAt) {
		return invalidModelError(where, "programme", "archived_at", "must not precede created_at", details)
	}
	return validateNamed(where, "programme", p.ID.String(), p.Name, p.DisplayName, p.Description)
}

// Auditable returns a deliberately safe audit projection.
func (p *Programme) Auditable() map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":               p.ID.String(),
		"created_at":       MillisFromTime(p.CreatedAt),
		"updated_at":       MillisFromTime(p.UpdatedAt),
		"archived_at":      p.ArchivedAt.Millis(),
		"revision":         p.Revision,
		"academic_unit_id": p.AcademicUnitID.String(),
		"name":             p.Name,
		"display_name":     p.DisplayName,
	}
}

// ResourceID returns the string form used by authorization Resource contracts.
func (p *Programme) ResourceID() string {
	if p == nil {
		return ""
	}
	return p.ID.String()
}

var _ Auditable = (*Programme)(nil)
