// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// InstallationState is the durable one-time bootstrap marker for a logical
// Proctor installation. It is deliberately distinct from process/node state:
// every application node sharing the database observes the same marker.
type InstallationState struct {
	InitializedAt       int64  `json:"initialized_at"`
	InstitutionId       string `json:"institution_id"`
	AdministratorUserId string `json:"administrator_user_id"`
}

type InstallationStatus struct {
	Initialized bool `json:"initialized"`
}

func (is *InstallationState) IsValid() bool {
	return is != nil &&
		is.InitializedAt > 0 &&
		IsValidId(is.InstitutionId) &&
		IsValidId(is.AdministratorUserId)
}

func (is *InstallationState) Auditable() map[string]any {
	if is == nil {
		return nil
	}
	return map[string]any{
		"initialized_at":        is.InitializedAt,
		"institution_id":        is.InstitutionId,
		"administrator_user_id": is.AdministratorUserId,
	}
}

// InstallationBootstrapResult contains the public, non-secret records created
// by the atomic bootstrap aggregate.
type InstallationBootstrapResult struct {
	State         *InstallationState `json:"state"`
	Institution   *Institution       `json:"institution"`
	Administrator *User              `json:"administrator"`
	Role          *Role              `json:"role"`
	RoleBinding   *RoleBinding       `json:"role_binding"`
}

var _ Auditable = (*InstallationState)(nil)
