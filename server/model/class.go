// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// Class is a concrete student roster for one ProgrammeLevel during one
// AcademicPeriod, such as "Computer Science Year 1 - Class A". Several classes
// may serve the same programme level in the same period.
type Class struct {
	Id               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	Revision         int64  `json:"revision"`
	ProgrammeLevelId string `json:"programme_level_id"`
	AcademicPeriodId string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}

func (c *Class) PrepareCreate(id string, at int64) {
	c.Id, c.CreateAt, c.UpdateAt, c.DeleteAt, c.Revision = id, at, at, 0, 1
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

func (c *Class) PrepareUpdate(at int64) {
	c.UpdateAt = at
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

func (c *Class) PreSave() {
	preSave(&c.Id, &c.CreateAt, &c.UpdateAt)
	if c.Revision == 0 {
		c.Revision = 1
	}
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

func (c *Class) PreUpdate() {
	preUpdate(&c.UpdateAt)
	sanitizeNamed(&c.Name, &c.DisplayName, &c.Description)
}

func (c *Class) IsValid() error {
	const where = "Class.IsValid"
	if appErr := validatePersistentFields(
		where,
		"class",
		c.Id,
		c.CreateAt,
		c.UpdateAt,
	); appErr != nil {
		return appErr
	}
	if c.Revision <= 0 {
		return invalidModelError(where, "class", "revision", "must be positive", "id="+c.Id)
	}
	details := "id=" + c.Id
	if !IsValidId(c.ProgrammeLevelId) {
		return invalidModelError(
			where,
			"class",
			"programme_level_id",
			"must be a valid identifier",
			details,
		)
	}
	if !IsValidId(c.AcademicPeriodId) {
		return invalidModelError(
			where,
			"class",
			"academic_period_id",
			"must be a valid identifier",
			details,
		)
	}
	return validateNamed(where, "class", c.Id, c.Name, c.DisplayName, c.Description)
}

func (c *Class) Auditable() map[string]any {
	fields := auditFields(c.Id, c.CreateAt, c.UpdateAt, c.DeleteAt)
	fields["revision"] = c.Revision
	fields["programme_level_id"] = c.ProgrammeLevelId
	fields["academic_period_id"] = c.AcademicPeriodId
	fields["name"] = c.Name
	fields["display_name"] = c.DisplayName
	return fields
}

var _ Auditable = (*Class)(nil)
