// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

// AcademicPeriodOwner identifies exactly one immutable owner. Institution
// periods apply everywhere; Academic Unit periods apply to the owner's
// subtree.
type AcademicPeriodOwner struct {
	InstitutionID  InstitutionID
	AcademicUnitID AcademicUnitID
}

func NewInstitutionAcademicPeriodOwner(id InstitutionID) AcademicPeriodOwner {
	return AcademicPeriodOwner{InstitutionID: id}
}

func NewAcademicUnitAcademicPeriodOwner(id AcademicUnitID) AcademicPeriodOwner {
	return AcademicPeriodOwner{AcademicUnitID: id}
}

func (owner AcademicPeriodOwner) Validate() error {
	institution := owner.InstitutionID.IsValid()
	unit := owner.AcademicUnitID.IsValid()
	if institution == unit {
		return fmt.Errorf("model: academic period owner must contain exactly one institution or academic unit")
	}
	return nil
}

func (owner AcademicPeriodOwner) Resource() Resource {
	if owner.InstitutionID.IsValid() {
		return Resource{Type: ResourceInstitution, ID: owner.InstitutionID.String()}
	}
	if owner.AcademicUnitID.IsValid() {
		return Resource{Type: ResourceAcademicUnit, ID: owner.AcademicUnitID.String()}
	}
	return Resource{}
}

func (owner AcademicPeriodOwner) Type() ResourceType { return owner.Resource().Type }
func (owner AcademicPeriodOwner) ID() string         { return owner.Resource().ID }

// AcademicPeriod is the time window in which classes and enrollments apply,
// such as an academic year or semester. StartsAt is inclusive and EndsAt is
// exclusive.
type AcademicPeriod struct {
	ID          AcademicPeriodID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  OptionalTime
	Revision    int64
	Owner       AcademicPeriodOwner
	Name        string
	DisplayName string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
}

// NewAcademicPeriod constructs a period with application-supplied identity and clock.
func NewAcademicPeriod(
	id AcademicPeriodID,
	owner AcademicPeriodOwner,
	name, displayName, description string,
	startsAt, endsAt, at time.Time,
) (*AcademicPeriod, error) {
	at = TimeUTC(at)
	period := &AcademicPeriod{
		ID:          id,
		CreatedAt:   at,
		UpdatedAt:   at,
		Revision:    1,
		Owner:       owner,
		Name:        name,
		DisplayName: displayName,
		Description: description,
		StartsAt:    TimeUTC(startsAt),
		EndsAt:      TimeUTC(endsAt),
	}
	sanitizeNamed(&period.Name, &period.DisplayName, &period.Description)
	if err := period.Validate(); err != nil {
		return nil, err
	}
	return period, nil
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (ap *AcademicPeriod) PrepareCreate(id AcademicPeriodID, at time.Time) {
	if ap == nil {
		return
	}
	ap.ID = id
	at = TimeUTC(at)
	ap.CreatedAt = at
	ap.UpdatedAt = at
	ap.ArchivedAt = OptionalTime{}
	if ap.Revision <= 0 {
		ap.Revision = 1
	}
	ap.StartsAt = TimeUTC(ap.StartsAt)
	ap.EndsAt = TimeUTC(ap.EndsAt)
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (ap *AcademicPeriod) PrepareUpdate(at time.Time) {
	if ap == nil {
		return
	}
	ap.UpdatedAt = TimeUTC(at)
	if ap.Revision <= 0 {
		ap.Revision = 1
	}
	ap.Revision++
	ap.StartsAt = TimeUTC(ap.StartsAt)
	ap.EndsAt = TimeUTC(ap.EndsAt)
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

// Archive marks the period archived.
func (ap *AcademicPeriod) Archive(at time.Time) error {
	if ap == nil {
		return fmt.Errorf("model: academic period is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: academic period archive time is required")
	}
	if ap.IsArchived() {
		return fmt.Errorf("model: academic period is already archived")
	}
	ap.ArchivedAt = OptionalTimeFrom(at)
	ap.UpdatedAt = at
	ap.Revision++
	return ap.Validate()
}

// IsArchived reports whether the period is archived.
func (ap *AcademicPeriod) IsArchived() bool {
	return ap != nil && ap.ArchivedAt.Valid
}

// Validate checks rehydrated academic-period state.
func (ap *AcademicPeriod) Validate() error {
	const where = "AcademicPeriod.Validate"
	if ap == nil {
		return invalidModelError(where, "academic_period", "value", "is required", "")
	}
	if !ap.ID.IsValid() {
		return invalidModelError(where, "academic_period", "id", "must be a valid identifier", "")
	}
	details := "id=" + ap.ID.String()
	if ap.CreatedAt.IsZero() {
		return invalidModelError(where, "academic_period", "created_at", "must be set", details)
	}
	if ap.UpdatedAt.IsZero() {
		return invalidModelError(where, "academic_period", "updated_at", "must be set", details)
	}
	if ap.UpdatedAt.Before(ap.CreatedAt) {
		return invalidModelError(where, "academic_period", "updated_at", "must not precede created_at", details)
	}
	if err := ap.Owner.Validate(); err != nil {
		return invalidModelError(where, "academic_period", "owner", "must identify exactly one institution or academic unit", details)
	}
	if ap.StartsAt.IsZero() {
		return invalidModelError(where, "academic_period", "start_at", "must be set", details)
	}
	if !ap.EndsAt.After(ap.StartsAt) {
		return invalidModelError(where, "academic_period", "end_at", "must be after start_at", details)
	}
	if ap.Revision < 0 {
		return invalidModelError(where, "academic_period", "revision", "must not be negative", details)
	}
	if ap.ArchivedAt.Valid && ap.ArchivedAt.Time.Before(ap.CreatedAt) {
		return invalidModelError(where, "academic_period", "archived_at", "must not precede created_at", details)
	}
	return validateNamed(where, "academic_period", ap.ID.String(), ap.Name, ap.DisplayName, ap.Description)
}

// Auditable returns a deliberately safe audit projection.
func (ap *AcademicPeriod) Auditable() map[string]any {
	if ap == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           ap.ID.String(),
		"created_at":   MillisFromTime(ap.CreatedAt),
		"updated_at":   MillisFromTime(ap.UpdatedAt),
		"archived_at":  ap.ArchivedAt.Millis(),
		"revision":     ap.Revision,
		"owner_type":   string(ap.Owner.Type()),
		"owner_id":     ap.Owner.ID(),
		"name":         ap.Name,
		"display_name": ap.DisplayName,
		"start_at":     MillisFromTime(ap.StartsAt),
		"end_at":       MillisFromTime(ap.EndsAt),
	}
}

// ResourceID returns the string form used by authorization Resource contracts.
func (ap *AcademicPeriod) ResourceID() string {
	if ap == nil {
		return ""
	}
	return ap.ID.String()
}

var _ Auditable = (*AcademicPeriod)(nil)
