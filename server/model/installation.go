// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "time"

// InstallationState is the durable one-time bootstrap marker for a logical
// Proctor installation. It is deliberately distinct from process/node state:
// every application node sharing the database observes the same marker.
type InstallationState struct {
	InitializedAt       time.Time
	InstitutionID       InstitutionID
	AdministratorUserID UserID
}

// InstallationStatus is the public bootstrap readiness projection.
type InstallationStatus struct {
	Initialized bool `json:"initialized"`
}

// Validate checks rehydrated installation-marker state.
func (is *InstallationState) Validate() error {
	const where = "InstallationState.Validate"
	if is == nil {
		return invalidModelError(where, "installation", "value", "is required", "")
	}
	if is.InitializedAt.IsZero() {
		return invalidModelError(where, "installation", "initialized_at", "must be set", "")
	}
	if !is.InstitutionID.IsValid() {
		return invalidModelError(where, "installation", "institution_id", "must be a valid identifier", "")
	}
	if !is.AdministratorUserID.IsValid() {
		return invalidModelError(where, "installation", "administrator_user_id", "must be a valid identifier", "")
	}
	return nil
}

// Auditable returns a deliberately safe audit projection.
func (is *InstallationState) Auditable() map[string]any {
	if is == nil {
		return map[string]any{}
	}
	return map[string]any{
		"initialized_at":        MillisFromTime(is.InitializedAt),
		"institution_id":        is.InstitutionID.String(),
		"administrator_user_id": is.AdministratorUserID.String(),
	}
}

// InstallationBootstrapResult contains the public, non-secret records created
// by the atomic bootstrap aggregate.
type InstallationBootstrapResult struct {
	State                      *InstallationState
	Institution                *Institution
	Administrator              *User
	Role                       *Role
	RoleBinding                *RoleBinding
	AccessPolicy               *AccessPolicy
	DesktopCompatibilityPolicy *DesktopCompatibilityPolicy
}

var _ Auditable = (*InstallationState)(nil)
