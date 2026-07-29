// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// RoleScopeType identifies the resource at which a role begins to apply.
// Programme and ProgrammeLevel scopes are intentionally absent until their
// authorization requirements are confirmed.
type RoleScopeType string

const (
	RoleScopeInstitution  RoleScopeType = "institution"
	RoleScopeAcademicUnit RoleScopeType = "academic_unit"
	RoleScopeClass        RoleScopeType = "class"
)

// RoleBinding assigns one Role to one User at one scope for a time range.
// Permission inheritance is evaluated by the authorization service from the
// resource hierarchy and action rules; it is not copied into sessions.
type RoleBinding struct {
	Id        string        `json:"id"`
	CreateAt  int64         `json:"create_at"`
	UpdateAt  int64         `json:"update_at"`
	DeleteAt  int64         `json:"delete_at"`
	UserId    string        `json:"user_id"`
	RoleId    string        `json:"role_id"`
	ScopeType RoleScopeType `json:"scope_type"`
	ScopeId   string        `json:"scope_id"`
	StartAt   int64         `json:"start_at"`
	EndAt     int64         `json:"end_at,omitempty"`
}

func (rb *RoleBinding) PreSave() {
	preSaveMembership(&rb.Id, &rb.CreateAt, &rb.UpdateAt, &rb.StartAt)
}

func (rb *RoleBinding) PreUpdate() {
	preUpdate(&rb.UpdateAt)
}

func (rb *RoleBinding) IsValid() *AppError {
	const where = "RoleBinding.IsValid"
	if appErr := validatePersistentFields(
		where,
		"role_binding",
		rb.Id,
		rb.CreateAt,
		rb.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + rb.Id
	if !IsValidId(rb.UserId) {
		return invalidModelError(where, "role_binding", "user_id", "must be a valid identifier", details)
	}
	if !IsValidId(rb.RoleId) {
		return invalidModelError(where, "role_binding", "role_id", "must be a valid identifier", details)
	}
	if !rb.ScopeType.IsValid() {
		return invalidModelError(where, "role_binding", "scope_type", "has an unknown value", details)
	}
	if !IsValidId(rb.ScopeId) {
		return invalidModelError(where, "role_binding", "scope_id", "must be a valid identifier", details)
	}
	return validateEffectiveTimes(where, "role_binding", details, rb.StartAt, rb.EndAt)
}

func (st RoleScopeType) IsValid() bool {
	switch st {
	case RoleScopeInstitution, RoleScopeAcademicUnit, RoleScopeClass:
		return true
	default:
		return false
	}
}

func (rb *RoleBinding) IsActiveAt(now int64) bool {
	return rb != nil && rb.DeleteAt == 0 && rb.StartAt <= now && (rb.EndAt == 0 || now < rb.EndAt)
}

func (rb *RoleBinding) Auditable() map[string]any {
	fields := auditFields(rb.Id, rb.CreateAt, rb.UpdateAt, rb.DeleteAt)
	fields["user_id"] = rb.UserId
	fields["role_id"] = rb.RoleId
	fields["scope_type"] = rb.ScopeType
	fields["scope_id"] = rb.ScopeId
	fields["start_at"] = rb.StartAt
	fields["end_at"] = rb.EndAt
	return fields
}

var _ Auditable = (*RoleBinding)(nil)
