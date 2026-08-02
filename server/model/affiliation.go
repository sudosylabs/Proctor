// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// AffiliationKind describes a person's institution-wide relationship. It does
// not grant permissions and is deliberately non-exclusive.
type AffiliationKind string

const (
	AffiliationStudent  AffiliationKind = "student"
	AffiliationTeacher  AffiliationKind = "teacher"
	AffiliationStaff    AffiliationKind = "staff"
	AffiliationExternal AffiliationKind = "external"
)

// Affiliation records a time-bounded relationship between a User and the
// institution. A person may simultaneously be both a student and a teacher, or
// retain historical affiliations after either relationship ends.
type Affiliation struct {
	Id       string          `json:"id"`
	CreateAt int64           `json:"create_at"`
	UpdateAt int64           `json:"update_at"`
	DeleteAt int64           `json:"delete_at"`
	Revision int64           `json:"revision"`
	UserId   string          `json:"user_id"`
	Kind     AffiliationKind `json:"kind"`
	StartAt  int64           `json:"start_at"`
	EndAt    int64           `json:"end_at,omitempty"`
}

func (a *Affiliation) PrepareCreate(id string, at int64) {
	a.Id, a.CreateAt, a.UpdateAt, a.DeleteAt, a.Revision = id, at, at, 0, 1
	if a.StartAt == 0 {
		a.StartAt = at
	}
}

func (a *Affiliation) PreSave() {
	preSaveMembership(&a.Id, &a.CreateAt, &a.UpdateAt, &a.StartAt)
	if a.Revision == 0 {
		a.Revision = 1
	}
}

func (a *Affiliation) PreUpdate() {
	preUpdate(&a.UpdateAt)
}

func (a *Affiliation) IsValid() *AppError {
	const where = "Affiliation.IsValid"
	if appErr := validatePersistentFields(
		where,
		"affiliation",
		a.Id,
		a.CreateAt,
		a.UpdateAt,
	); appErr != nil {
		return appErr
	}
	if a.Revision <= 0 {
		return invalidModelError(where, "affiliation", "revision", "must be positive", "id="+a.Id)
	}
	details := "id=" + a.Id
	if !IsValidId(a.UserId) {
		return invalidModelError(where, "affiliation", "user_id", "must be a valid identifier", details)
	}
	if !a.Kind.IsValid() {
		return invalidModelError(where, "affiliation", "kind", "has an unknown value", details)
	}
	return validateEffectiveTimes(where, "affiliation", details, a.StartAt, a.EndAt)
}

func (k AffiliationKind) IsValid() bool {
	switch k {
	case AffiliationStudent, AffiliationTeacher, AffiliationStaff, AffiliationExternal:
		return true
	default:
		return false
	}
}

func (a *Affiliation) IsActiveAt(now int64) bool {
	return a != nil && a.DeleteAt == 0 && a.StartAt <= now && (a.EndAt == 0 || now < a.EndAt)
}

func (a *Affiliation) Auditable() map[string]any {
	fields := auditFields(a.Id, a.CreateAt, a.UpdateAt, a.DeleteAt)
	fields["revision"] = a.Revision
	fields["user_id"] = a.UserId
	fields["kind"] = a.Kind
	fields["start_at"] = a.StartAt
	fields["end_at"] = a.EndAt
	return fields
}

func preSaveMembership(id *string, createAt, updateAt, startAt *int64) {
	preSave(id, createAt, updateAt)
	if *startAt == 0 {
		*startAt = *createAt
	}
}

func validateEffectiveTimes(where, modelName, details string, startAt, endAt int64) *AppError {
	if startAt <= 0 {
		return invalidModelError(where, modelName, "start_at", "must be set", details)
	}
	if endAt != 0 && endAt <= startAt {
		return invalidModelError(where, modelName, "end_at", "must be after start_at", details)
	}
	return nil
}

var _ Auditable = (*Affiliation)(nil)
