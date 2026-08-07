// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// ProgrammeLevel is a curriculum stage within a Programme, such as Foundation,
// Year 1, or Year 2. It is reusable across academic periods and is distinct
// from the concrete Class roster into which students enroll.
type ProgrammeLevel struct {
	Id          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	ProgrammeId string `json:"programme_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

func (pl *ProgrammeLevel) PreSave() {
	preSave(&pl.Id, &pl.CreateAt, &pl.UpdateAt)
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (pl *ProgrammeLevel) PrepareCreate(id string, at int64) {
	pl.Id = id
	pl.CreateAt = at
	pl.UpdateAt = at
	pl.DeleteAt = 0
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

// PrepareUpdate applies the application-selected transition time.
func (pl *ProgrammeLevel) PrepareUpdate(at int64) {
	pl.UpdateAt = at
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

func (pl *ProgrammeLevel) PreUpdate() {
	preUpdate(&pl.UpdateAt)
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

func (pl *ProgrammeLevel) IsValid() error {
	const where = "ProgrammeLevel.IsValid"
	if appErr := validatePersistentFields(
		where,
		"programme_level",
		pl.Id,
		pl.CreateAt,
		pl.UpdateAt,
	); appErr != nil {
		return appErr
	}
	if !IsValidId(pl.ProgrammeId) {
		return invalidModelError(
			where,
			"programme_level",
			"programme_id",
			"must be a valid identifier",
			"id="+pl.Id,
		)
	}
	return validateNamed(
		where,
		"programme_level",
		pl.Id,
		pl.Name,
		pl.DisplayName,
		pl.Description,
	)
}

func (pl *ProgrammeLevel) Auditable() map[string]any {
	fields := auditFields(pl.Id, pl.CreateAt, pl.UpdateAt, pl.DeleteAt)
	fields["programme_id"] = pl.ProgrammeId
	fields["name"] = pl.Name
	fields["display_name"] = pl.DisplayName
	return fields
}

var _ Auditable = (*ProgrammeLevel)(nil)
