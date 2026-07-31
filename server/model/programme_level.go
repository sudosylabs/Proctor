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

type ProgrammeLevelPatch struct {
	Name        *string `json:"name,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (pl *ProgrammeLevel) Patch(p *ProgrammeLevelPatch) {
	if p.Name != nil {
		pl.Name = *p.Name
	}
	if p.DisplayName != nil {
		pl.DisplayName = *p.DisplayName
	}
	if p.Description != nil {
		pl.Description = *p.Description
	}
}

func (pl *ProgrammeLevel) PreSave() {
	preSave(&pl.Id, &pl.CreateAt, &pl.UpdateAt)
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

func (pl *ProgrammeLevel) PreUpdate() {
	preUpdate(&pl.UpdateAt)
	sanitizeNamed(&pl.Name, &pl.DisplayName, &pl.Description)
}

func (pl *ProgrammeLevel) IsValid() *AppError {
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
