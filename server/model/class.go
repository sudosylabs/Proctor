// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

// Class is a concrete student roster for one ProgrammeLevel during one
// AcademicPeriod, such as "Computer Science Year 1 - Class A". Several classes
// may serve the same programme level in the same period.
type Class struct {
	ID               ClassID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ArchivedAt       OptionalTime
	Revision         int64
	ProgrammeLevelID ProgrammeLevelID
	AcademicPeriodID AcademicPeriodID
	Name             string
	DisplayName      string
	Description      string
}

// NewClass constructs a class with application-supplied identity and clock.
func NewClass(
	id ClassID,
	programmeLevelID ProgrammeLevelID,
	academicPeriodID AcademicPeriodID,
	name, displayName, description string,
	at time.Time,
) (*Class, error) {
	at = TimeUTC(at)
	class := &Class{
		ID:               id,
		CreatedAt:        at,
		UpdatedAt:        at,
		Revision:         1,
		ProgrammeLevelID: programmeLevelID,
		AcademicPeriodID: academicPeriodID,
		Name:             name,
		DisplayName:      displayName,
		Description:      description,
	}
	sanitizeNamed(&class.Name, &class.DisplayName, &class.Description)
	if err := class.Validate(); err != nil {
		return nil, err
	}
	return class, nil
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (c *Class) PrepareCreate(id ClassID, at time.Time) {
	if c == nil {
		return
	}
	c.ID = id
	at = TimeUTC(at)
	c.CreatedAt = at
	c.UpdatedAt = at
	c.ArchivedAt = OptionalTime{}
	if c.Revision <= 0 {
		c.Revision = 1
	}
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (c *Class) PrepareUpdate(at time.Time) {
	if c == nil {
		return
	}
	c.UpdatedAt = TimeUTC(at)
	if c.Revision <= 0 {
		c.Revision = 1
	}
	c.Revision++
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

// Archive marks the class archived.
func (c *Class) Archive(at time.Time) error {
	if c == nil {
		return fmt.Errorf("model: class is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: class archive time is required")
	}
	if c.IsArchived() {
		return fmt.Errorf("model: class is already archived")
	}
	c.ArchivedAt = OptionalTimeFrom(at)
	c.UpdatedAt = at
	c.Revision++
	return c.Validate()
}

// IsArchived reports whether the class is archived.
func (c *Class) IsArchived() bool {
	return c != nil && c.ArchivedAt.Valid
}

// Validate checks rehydrated class state.
func (c *Class) Validate() error {
	const where = "Class.Validate"
	if c == nil {
		return invalidModelError(where, "class", "value", "is required", "")
	}
	if !c.ID.IsValid() {
		return invalidModelError(where, "class", "id", "must be a valid identifier", "")
	}
	details := "id=" + c.ID.String()
	if c.CreatedAt.IsZero() {
		return invalidModelError(where, "class", "created_at", "must be set", details)
	}
	if c.UpdatedAt.IsZero() {
		return invalidModelError(where, "class", "updated_at", "must be set", details)
	}
	if c.UpdatedAt.Before(c.CreatedAt) {
		return invalidModelError(where, "class", "updated_at", "must not precede created_at", details)
	}
	if c.Revision <= 0 {
		return invalidModelError(where, "class", "revision", "must be positive", details)
	}
	if !c.ProgrammeLevelID.IsValid() {
		return invalidModelError(where, "class", "programme_level_id", "must be a valid identifier", details)
	}
	if !c.AcademicPeriodID.IsValid() {
		return invalidModelError(where, "class", "academic_period_id", "must be a valid identifier", details)
	}
	if c.ArchivedAt.Valid && c.ArchivedAt.Time.Before(c.CreatedAt) {
		return invalidModelError(where, "class", "archived_at", "must not precede created_at", details)
	}
	return validateNamed(where, "class", c.ID.String(), c.Name, c.DisplayName, c.Description)
}

// Auditable returns a deliberately safe audit projection.
func (c *Class) Auditable() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                 c.ID.String(),
		"created_at":         MillisFromTime(c.CreatedAt),
		"updated_at":         MillisFromTime(c.UpdatedAt),
		"archived_at":        c.ArchivedAt.Millis(),
		"revision":           c.Revision,
		"programme_level_id": c.ProgrammeLevelID.String(),
		"academic_period_id": c.AcademicPeriodID.String(),
		"name":               c.Name,
		"display_name":       c.DisplayName,
	}
}

// ResourceID returns the string form used by authorization Resource contracts.
func (c *Class) ResourceID() string {
	if c == nil {
		return ""
	}
	return c.ID.String()
}

var _ Auditable = (*Class)(nil)
