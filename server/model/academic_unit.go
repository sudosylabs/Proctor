// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// AcademicUnit is one node in the institution's organizational tree, such as a
// college, school, or another institution-defined unit. ParentId is empty for
// a root unit and points to another AcademicUnit for a nested unit. The core
// deliberately does not classify units: their names and positions in the tree
// carry institution-specific meaning. Programmes are owned by units; classes
// are reached through those programmes, not attached directly here.
type AcademicUnit struct {
	Id            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionId string `json:"institution_id"`
	ParentId      string `json:"parent_id,omitempty"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

func (au *AcademicUnit) PreSave() {
	preSave(&au.Id, &au.CreateAt, &au.UpdateAt)
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

// PrepareCreate applies the explicit identity and time chosen by the
// application and normalizes the local Academic Unit fields for validation.
func (au *AcademicUnit) PrepareCreate(id string, at int64) {
	au.Id = id
	au.CreateAt = at
	au.UpdateAt = at
	au.DeleteAt = 0
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

// PrepareUpdate applies the application-selected transition time and
// normalizes mutable local fields before validation.
func (au *AcademicUnit) PrepareUpdate(at int64) {
	au.UpdateAt = at
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

func (au *AcademicUnit) PreUpdate() {
	preUpdate(&au.UpdateAt)
	sanitizeNamed(&au.Name, &au.DisplayName, &au.Description)
}

func (au *AcademicUnit) IsValid() error {
	const where = "AcademicUnit.IsValid"
	if appErr := validatePersistentFields(
		where,
		"academic_unit",
		au.Id,
		au.CreateAt,
		au.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + au.Id
	if !IsValidId(au.InstitutionId) {
		return invalidModelError(
			where,
			"academic_unit",
			"institution_id",
			"must be a valid identifier",
			details,
		)
	}
	if au.ParentId != "" && !IsValidId(au.ParentId) {
		return invalidModelError(
			where,
			"academic_unit",
			"parent_id",
			"must be empty or a valid identifier",
			details,
		)
	}
	if au.ParentId == au.Id {
		return invalidModelError(
			where,
			"academic_unit",
			"parent_id",
			"must not reference the unit itself",
			details,
		)
	}
	return validateNamed(where, "academic_unit", au.Id, au.Name, au.DisplayName, au.Description)
}

func (au *AcademicUnit) Auditable() map[string]any {
	fields := auditFields(au.Id, au.CreateAt, au.UpdateAt, au.DeleteAt)
	fields["institution_id"] = au.InstitutionId
	fields["parent_id"] = au.ParentId
	fields["name"] = au.Name
	fields["display_name"] = au.DisplayName
	return fields
}

var _ Auditable = (*AcademicUnit)(nil)
