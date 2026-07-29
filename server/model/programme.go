// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// Programme is a course of study owned by one AcademicUnit, such as Bachelor
// of Computer Science. It describes the curriculum independently of academic
// years and concrete student rosters.
type Programme struct {
	Id             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitId string `json:"academic_unit_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
}

func (p *Programme) PreSave() {
	preSave(&p.Id, &p.CreateAt, &p.UpdateAt)
	sanitizeNamed(&p.Name, &p.DisplayName, &p.Description)
}

func (p *Programme) PreUpdate() {
	preUpdate(&p.UpdateAt)
	sanitizeNamed(&p.Name, &p.DisplayName, &p.Description)
}

func (p *Programme) IsValid() *AppError {
	const where = "Programme.IsValid"
	if appErr := validatePersistentFields(
		where,
		"programme",
		p.Id,
		p.CreateAt,
		p.UpdateAt,
	); appErr != nil {
		return appErr
	}
	if !IsValidId(p.AcademicUnitId) {
		return invalidModelError(
			where,
			"programme",
			"academic_unit_id",
			"must be a valid identifier",
			"id="+p.Id,
		)
	}
	return validateNamed(
		where,
		"programme",
		p.Id,
		p.Name,
		p.DisplayName,
		p.Description,
	)
}

func (p *Programme) Auditable() map[string]any {
	fields := auditFields(p.Id, p.CreateAt, p.UpdateAt, p.DeleteAt)
	fields["academic_unit_id"] = p.AcademicUnitId
	fields["name"] = p.Name
	fields["display_name"] = p.DisplayName
	return fields
}

var _ Auditable = (*Programme)(nil)
