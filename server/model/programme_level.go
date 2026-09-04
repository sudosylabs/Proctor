// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"fmt"
	"time"
)

// ProgrammeLevel is a curriculum stage within a Programme, such as Foundation,
// Year 1, or Year 2. It is reusable across academic periods and is distinct
// from the concrete Class roster into which students enroll.
type ProgrammeLevel struct {
	ID          ProgrammeLevelID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  OptionalTime
	Revision    int64
	ProgrammeID ProgrammeID
	Name        string
	DisplayName string
	Description string
}

// NewProgrammeLevel constructs a level with application-supplied identity and clock.
func NewProgrammeLevel(
	id ProgrammeLevelID,
	programmeID ProgrammeID,
	name, displayName, description string,
	at time.Time,
) (*ProgrammeLevel, error) {
	at = TimeUTC(at)
	level := &ProgrammeLevel{
		ID:          id,
		CreatedAt:   at,
		UpdatedAt:   at,
		Revision:    1,
		ProgrammeID: programmeID,
		Name:        name,
		DisplayName: displayName,
		Description: description,
	}
	sanitizeNamed(&level.Name, &level.DisplayName, &level.Description)
	if err := level.Validate(); err != nil {
		return nil, err
	}
	return level, nil
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (pl *ProgrammeLevel) PrepareCreate(id ProgrammeLevelID, at time.Time) {
	if pl == nil {
		return
	}
	pl.ID = id
	at = TimeUTC(at)
	pl.CreatedAt = at
	pl.UpdatedAt = at
	pl.ArchivedAt = OptionalTime{}
	if pl.Revision <= 0 {
		pl.Revision = 1
	}
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (pl *ProgrammeLevel) PrepareUpdate(at time.Time) {
	if pl == nil {
		return
	}
	pl.UpdatedAt = TimeUTC(at)
	if pl.Revision <= 0 {
		pl.Revision = 1
	}
	pl.Revision++
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

// Archive marks the programme level archived.
func (pl *ProgrammeLevel) Archive(at time.Time) error {
	if pl == nil {
		return fmt.Errorf("model: programme level is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: programme level archive time is required")
	}
	if pl.IsArchived() {
		return fmt.Errorf("model: programme level is already archived")
	}
	pl.ArchivedAt = OptionalTimeFrom(at)
	pl.UpdatedAt = at
	pl.Revision++
	return pl.Validate()
}

// IsArchived reports whether the level is archived.
func (pl *ProgrammeLevel) IsArchived() bool {
	return pl != nil && pl.ArchivedAt.Valid
}

// Validate checks rehydrated programme-level state.
func (pl *ProgrammeLevel) Validate() error {
	const where = "ProgrammeLevel.Validate"
	if pl == nil {
		return invalidModelError(where, "programme_level", "value", "is required", "")
	}
	if !pl.ID.IsValid() {
		return invalidModelError(where, "programme_level", "id", "must be a valid identifier", "")
	}
	details := "id=" + pl.ID.String()
	if pl.CreatedAt.IsZero() {
		return invalidModelError(where, "programme_level", "created_at", "must be set", details)
	}
	if pl.UpdatedAt.IsZero() {
		return invalidModelError(where, "programme_level", "updated_at", "must be set", details)
	}
	if pl.UpdatedAt.Before(pl.CreatedAt) {
		return invalidModelError(where, "programme_level", "updated_at", "must not precede created_at", details)
	}
	if !pl.ProgrammeID.IsValid() {
		return invalidModelError(where, "programme_level", "programme_id", "must be a valid identifier", details)
	}
	if pl.Revision < 0 {
		return invalidModelError(where, "programme_level", "revision", "must not be negative", details)
	}
	if pl.ArchivedAt.Valid && pl.ArchivedAt.Time.Before(pl.CreatedAt) {
		return invalidModelError(where, "programme_level", "archived_at", "must not precede created_at", details)
	}
	return validateNamed(where, "programme_level", pl.ID.String(), pl.Name, pl.DisplayName, pl.Description)
}

// Auditable returns a deliberately safe audit projection.
func (pl *ProgrammeLevel) Auditable() map[string]any {
	if pl == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           pl.ID.String(),
		"created_at":   MillisFromTime(pl.CreatedAt),
		"updated_at":   MillisFromTime(pl.UpdatedAt),
		"archived_at":  pl.ArchivedAt.Millis(),
		"revision":     pl.Revision,
		"programme_id": pl.ProgrammeID.String(),
		"name":         pl.Name,
		"display_name": pl.DisplayName,
	}
}

// ResourceID returns the string form used by authorization Resource contracts.
func (pl *ProgrammeLevel) ResourceID() string {
	if pl == nil {
		return ""
	}
	return pl.ID.String()
}

var _ Auditable = (*ProgrammeLevel)(nil)
