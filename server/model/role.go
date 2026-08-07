// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/role.go for Proctor's
// action-based authorization vocabulary.

package model

import "regexp"

const (
	RolePermissionMaxLength = 128
	RolePermissionMaxCount  = 256

	SystemAdministratorRoleName = "system_admin"
)

var validPermission = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$`)

// Role is a reusable named set of stable domain permissions. It has no scope
// by itself; RoleBinding assigns it to a User at an institution, AcademicUnit,
// or Class scope.
type Role struct {
	Id          string   `json:"id"`
	CreateAt    int64    `json:"create_at"`
	UpdateAt    int64    `json:"update_at"`
	DeleteAt    int64    `json:"delete_at"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	BuiltIn     bool     `json:"built_in"`
}

// RolePatch contains the mutable fields of a custom role. Role names are
// stable identifiers and built-in roles are managed by server code.
type RolePatch struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

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

func (rp *RolePatch) IsEmpty() bool {
	return rp == nil ||
		(rp.DisplayName == nil && rp.Description == nil && rp.Permissions == nil)
}

func (r *Role) PreSave() {
	preSave(&r.Id, &r.CreateAt, &r.UpdateAt)
	sanitizeNamed(&r.Name, &r.DisplayName, &r.Description)
	r.Permissions = cloneStrings(r.Permissions)
}

func (r *Role) PreUpdate() {
	preUpdate(&r.UpdateAt)
	sanitizeNamed(&r.Name, &r.DisplayName, &r.Description)
	r.Permissions = cloneStrings(r.Permissions)
}

func (r *Role) IsValid() error {
	const where = "Role.IsValid"
	if appErr := validatePersistentFields(where, "role", r.Id, r.CreateAt, r.UpdateAt); appErr != nil {
		return appErr
	}
	if appErr := validateNamed(where, "role", r.Id, r.Name, r.DisplayName, r.Description); appErr != nil {
		return appErr
	}
	details := "id=" + r.Id
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

func (r *Role) Clone() *Role {
	if r == nil {
		return nil
	}
	cloned := *r
	cloned.Permissions = cloneStrings(r.Permissions)
	return &cloned
}

func (r *Role) Auditable() map[string]any {
	fields := auditFields(r.Id, r.CreateAt, r.UpdateAt, r.DeleteAt)
	fields["name"] = r.Name
	fields["display_name"] = r.DisplayName
	fields["permissions"] = cloneStrings(r.Permissions)
	fields["built_in"] = r.BuiltIn
	return fields
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

var _ Auditable = (*Role)(nil)
