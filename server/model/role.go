// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/role.go for Proctor's
// action-based authorization vocabulary.

package model

import (
	"fmt"
	"regexp"
	"time"
)

const (
	RolePermissionMaxLength = 128
	RolePermissionMaxCount  = 256

	SystemAdministratorRoleName = "system_admin"
)

var validPermission = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$`)

// Role is a reusable named set of stable domain permissions. It has no scope
// by itself; RoleBinding assigns it to a User at an institution, AcademicUnit,
// or Class scope.
//
// Domain time is UTC time.Time. Soft archive uses ArchivedAt.
// Roles are not revisioned: updates and soft-delete are serialized through
// explicit store operations and built-in protection, not optimistic concurrency.
type Role struct {
	ID          RoleID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  OptionalTime
	Name        string
	DisplayName string
	Description string
	Permissions []string
	BuiltIn     bool
}

// RolePatch contains the mutable fields of a custom role. Role names are
// stable identifiers and built-in roles are managed by server code.
type RolePatch struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

// Patch applies non-nil fields from patch.
func (r *Role) Patch(patch *RolePatch) {
	if r == nil || patch == nil {
		return
	}
	if patch.DisplayName != nil {
		r.DisplayName = *patch.DisplayName
	}
	if patch.Description != nil {
		r.Description = *patch.Description
	}
	if patch.Permissions != nil {
		r.Permissions = cloneStrings(*patch.Permissions)
	}
}

// IsEmpty reports whether the patch carries no field updates.
func (rp *RolePatch) IsEmpty() bool {
	return rp == nil ||
		(rp.DisplayName == nil && rp.Description == nil && rp.Permissions == nil)
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (r *Role) PrepareCreate(id RoleID, at time.Time) {
	if r == nil {
		return
	}
	r.ID = id
	at = TimeUTC(at)
	r.CreatedAt = at
	r.UpdatedAt = at
	r.ArchivedAt = OptionalTime{}
	sanitizeNamed(&r.Name, &r.DisplayName, &r.Description)
	r.Permissions = cloneStrings(r.Permissions)
}

// PrepareUpdate applies the application-selected transition time.
func (r *Role) PrepareUpdate(at time.Time) {
	if r == nil {
		return
	}
	r.UpdatedAt = TimeUTC(at)
	sanitizeNamed(&r.Name, &r.DisplayName, &r.Description)
	r.Permissions = cloneStrings(r.Permissions)
}

// Archive marks the role soft-archived (legacy soft-delete).
func (r *Role) Archive(at time.Time) error {
	if r == nil {
		return fmt.Errorf("model: role is nil")
	}
	at = TimeUTC(at)
	if at.IsZero() {
		return fmt.Errorf("model: role archive time is required")
	}
	if r.IsArchived() {
		return fmt.Errorf("model: role is already archived")
	}
	if r.BuiltIn {
		return fmt.Errorf("model: built-in role cannot be archived")
	}
	r.ArchivedAt = OptionalTimeFrom(at)
	r.UpdatedAt = at
	return r.Validate()
}

// IsArchived reports soft archive state.
func (r *Role) IsArchived() bool {
	return r != nil && r.ArchivedAt.Valid
}

// Validate checks rehydrated role state.
func (r *Role) Validate() error {
	const where = "Role.Validate"
	if r == nil {
		return invalidModelError(where, "role", "value", "is required", "")
	}
	if !r.ID.IsValid() {
		return invalidModelError(where, "role", "id", "must be a valid identifier", "")
	}
	details := "id=" + r.ID.String()
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return invalidModelError(where, "role", "created_at", "must be set", details)
	}
	if r.UpdatedAt.Before(r.CreatedAt) {
		return invalidModelError(where, "role", "updated_at", "must not precede created_at", details)
	}
	if r.ArchivedAt.Valid && r.ArchivedAt.Time.Before(r.CreatedAt) {
		return invalidModelError(where, "role", "archived_at", "must not precede created_at", details)
	}
	if err := validateNamed(where, "role", r.ID.String(), r.Name, r.DisplayName, r.Description); err != nil {
		return err
	}
	if len(r.Permissions) > RolePermissionMaxCount {
		return invalidModelError(where, "role", "permissions", "contains too many values", details)
	}
	seen := make(map[string]struct{}, len(r.Permissions))
	for _, permission := range r.Permissions {
		if len(permission) > RolePermissionMaxLength || !validPermission.MatchString(permission) {
			return invalidModelError(where, "role", "permissions", "contains an invalid action", details)
		}
		if _, exists := seen[permission]; exists {
			return invalidModelError(where, "role", "permissions", "contains a duplicate action", details)
		}
		seen[permission] = struct{}{}
	}
	return nil
}

// Clone returns a deep copy of the role including its permissions slice.
func (r *Role) Clone() *Role {
	if r == nil {
		return nil
	}
	cloned := *r
	cloned.Permissions = cloneStrings(r.Permissions)
	return &cloned
}

// Auditable returns a deliberately safe audit projection. Lifecycle times remain
// as Unix milliseconds for wire and audit compatibility during expand.
func (r *Role) Auditable() map[string]any {
	if r == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           r.ID.String(),
		"created_at":   MillisFromTime(r.CreatedAt),
		"updated_at":   MillisFromTime(r.UpdatedAt),
		"archived_at":  r.ArchivedAt.Millis(),
		"name":         r.Name,
		"display_name": r.DisplayName,
		"permissions":  cloneStrings(r.Permissions),
		"built_in":     r.BuiltIn,
	}
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

var _ Auditable = (*Role)(nil)
