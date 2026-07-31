// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// Institution is the university or school represented by one logical Proctor
// installation. Its academic organization begins at its root AcademicUnit
// records; programmes and classes are reached through those units.
type Institution struct {
	Id          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type InstitutionPatch struct {
	Name        *string `json:"name,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (i *Institution) Patch(p *InstitutionPatch) {
	if p.Name != nil {
		i.Name = *p.Name
	}
	if p.DisplayName != nil {
		i.DisplayName = *p.DisplayName
	}
	if p.Description != nil {
		i.Description = *p.Description
	}
}

func (i *Institution) PreSave() {
	preSave(&i.Id, &i.CreateAt, &i.UpdateAt)
	sanitizeNamed(&i.Name, &i.DisplayName, &i.Description)
}

func (i *Institution) PreUpdate() {
	preUpdate(&i.UpdateAt)
	sanitizeNamed(&i.Name, &i.DisplayName, &i.Description)
}

func (i *Institution) IsValid() *AppError {
	const where = "Institution.IsValid"
	if appErr := validatePersistentFields(
		where,
		"institution",
		i.Id,
		i.CreateAt,
		i.UpdateAt,
	); appErr != nil {
		return appErr
	}
	return validateNamed(
		where,
		"institution",
		i.Id,
		i.Name,
		i.DisplayName,
		i.Description,
	)
}

func (i *Institution) Auditable() map[string]any {
	fields := auditFields(
		i.Id,
		i.CreateAt,
		i.UpdateAt,
		i.DeleteAt,
	)
	fields["name"] = i.Name
	fields["display_name"] = i.DisplayName
	return fields
}

var _ Auditable = (*Institution)(nil)
