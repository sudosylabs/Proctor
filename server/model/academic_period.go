// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// AcademicPeriod is the time window in which classes and enrollments apply,
// such as an academic year or semester. It is institution-wide rather than a
// child of one programme. StartAt is inclusive and EndAt is exclusive, both in
// Unix milliseconds.
type AcademicPeriod struct {
	Id            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionId string `json:"institution_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	StartAt       int64  `json:"start_at"`
	EndAt         int64  `json:"end_at"`
}

func (ap *AcademicPeriod) PrepareCreate(id string, at int64) {
	ap.Id, ap.CreateAt, ap.UpdateAt, ap.DeleteAt = id, at, at, 0
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

func (ap *AcademicPeriod) PrepareUpdate(at int64) {
	ap.UpdateAt = at
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

func (ap *AcademicPeriod) PreSave() {
	preSave(&ap.Id, &ap.CreateAt, &ap.UpdateAt)
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

func (ap *AcademicPeriod) PreUpdate() {
	preUpdate(&ap.UpdateAt)
	sanitizeNamed(&ap.Name, &ap.DisplayName, &ap.Description)
}

func (ap *AcademicPeriod) IsValid() *AppError {
	const where = "AcademicPeriod.IsValid"
	if appErr := validatePersistentFields(
		where,
		"academic_period",
		ap.Id,
		ap.CreateAt,
		ap.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + ap.Id
	if !IsValidId(ap.InstitutionId) {
		return invalidModelError(
			where,
			"academic_period",
			"institution_id",
			"must be a valid identifier",
			details,
		)
	}
	if ap.StartAt <= 0 {
		return invalidModelError(where, "academic_period", "start_at", "must be set", details)
	}
	if ap.EndAt <= ap.StartAt {
		return invalidModelError(where, "academic_period", "end_at", "must be after start_at", details)
	}
	return validateNamed(
		where,
		"academic_period",
		ap.Id,
		ap.Name,
		ap.DisplayName,
		ap.Description,
	)
}

func (ap *AcademicPeriod) Auditable() map[string]any {
	fields := auditFields(ap.Id, ap.CreateAt, ap.UpdateAt, ap.DeleteAt)
	fields["institution_id"] = ap.InstitutionId
	fields["name"] = ap.Name
	fields["display_name"] = ap.DisplayName
	fields["start_at"] = ap.StartAt
	fields["end_at"] = ap.EndAt
	return fields
}

var _ Auditable = (*AcademicPeriod)(nil)
