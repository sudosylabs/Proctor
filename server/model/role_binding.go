// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"time"
)

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
//
// ScopeID remains a plain string because the target table depends on
// ScopeType (polymorphic scope reference). Effective dates use StartsAt and
// EndsAt OptionalTime like Affiliation.
type RoleBinding struct {
	ID         RoleBindingID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt OptionalTime
	UserID     UserID
	RoleID     RoleID
	ScopeType  RoleScopeType
	ScopeID    string
	StartsAt   time.Time
	EndsAt     OptionalTime // absent means open-ended
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (rb *RoleBinding) PrepareCreate(id RoleBindingID, at time.Time) {
	if rb == nil {
		return
	}
	rb.ID = id
	at = TimeUTC(at)
	rb.CreatedAt = at
	rb.UpdatedAt = at
	rb.ArchivedAt = OptionalTime{}
	if rb.StartsAt.IsZero() {
		rb.StartsAt = at
	} else {
		rb.StartsAt = TimeUTC(rb.StartsAt)
	}
}

// PrepareUpdate applies the application-selected transition time.
func (rb *RoleBinding) PrepareUpdate(at time.Time) {
	if rb == nil {
		return
	}
	rb.UpdatedAt = TimeUTC(at)
}

// Validate checks rehydrated role-binding state.
func (rb *RoleBinding) Validate() error {
	const where = "RoleBinding.Validate"
	if rb == nil {
		return invalidModelError(where, "role_binding", "value", "is required", "")
	}
	if !rb.ID.IsValid() {
		return invalidModelError(where, "role_binding", "id", "must be a valid identifier", "")
	}
	details := "id=" + rb.ID.String()
	if rb.CreatedAt.IsZero() || rb.UpdatedAt.IsZero() {
		return invalidModelError(where, "role_binding", "created_at", "must be set", details)
	}
	if rb.UpdatedAt.Before(rb.CreatedAt) {
		return invalidModelError(where, "role_binding", "updated_at", "must not precede created_at", details)
	}
	if rb.ArchivedAt.Valid && rb.ArchivedAt.Time.Before(rb.CreatedAt) {
		return invalidModelError(where, "role_binding", "archived_at", "must not precede created_at", details)
	}
	if !rb.UserID.IsValid() {
		return invalidModelError(where, "role_binding", "user_id", "must be a valid identifier", details)
	}
	if !rb.RoleID.IsValid() {
		return invalidModelError(where, "role_binding", "role_id", "must be a valid identifier", details)
	}
	if !rb.ScopeType.IsValid() {
		return invalidModelError(where, "role_binding", "scope_type", "has an unknown value", details)
	}
	if !IsValidId(rb.ScopeID) {
		return invalidModelError(where, "role_binding", "scope_id", "must be a valid identifier", details)
	}
	return validateEffectiveInterval(where, "role_binding", details, rb.StartsAt, rb.EndsAt)
}

// IsValid reports whether the scope type is recognized.
func (st RoleScopeType) IsValid() bool {
	switch st {
	case RoleScopeInstitution, RoleScopeAcademicUnit, RoleScopeClass:
		return true
	default:
		return false
	}
}

// IsActiveAt reports whether the binding covers the given instant.
func (rb *RoleBinding) IsActiveAt(now time.Time) bool {
	if rb == nil || rb.IsArchived() {
		return false
	}
	now = TimeUTC(now)
	if rb.StartsAt.After(now) {
		return false
	}
	if rb.EndsAt.Valid && !now.Before(rb.EndsAt.Time) {
		return false
	}
	return true
}

// IsArchived reports soft archive state.
func (rb *RoleBinding) IsArchived() bool {
	return rb != nil && rb.ArchivedAt.Valid
}

// End closes an open-ended binding at the given exclusive end time.
func (rb *RoleBinding) End(at time.Time) error {
	if rb == nil {
		return fmt.Errorf("model: role binding is nil")
	}
	at = TimeUTC(at)
	if rb.EndsAt.Valid {
		return fmt.Errorf("model: role binding already ended")
	}
	rb.EndsAt = OptionalTimeFrom(at)
	rb.UpdatedAt = at
	return rb.Validate()
}

// Auditable returns a deliberately safe audit projection.
func (rb *RoleBinding) Auditable() map[string]any {
	if rb == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          rb.ID.String(),
		"created_at":  MillisFromTime(rb.CreatedAt),
		"updated_at":  MillisFromTime(rb.UpdatedAt),
		"archived_at": rb.ArchivedAt.Millis(),
		"user_id":     rb.UserID.String(),
		"role_id":     rb.RoleID.String(),
		"scope_type":  rb.ScopeType,
		"scope_id":    rb.ScopeID,
		"start_at":    MillisFromTime(rb.StartsAt),
		"end_at":      rb.EndsAt.Millis(),
	}
}

var _ Auditable = (*RoleBinding)(nil)
