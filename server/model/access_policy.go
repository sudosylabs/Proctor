// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"regexp"
	"time"
)

// ProviderAdmissionMode is the closed policy vocabulary for one configured
// external provider. An absent provider entry is disabled.
type ProviderAdmissionMode string

const (
	ProviderAdmissionLinkedOnly         ProviderAdmissionMode = "linked_only"
	ProviderAdmissionInvitationRequired ProviderAdmissionMode = "invitation_required"
	ProviderAdmissionAutoProvision      ProviderAdmissionMode = "auto_provision"
)

const AccessPolicyProviderMaxCount = 64

var validAccessPolicyProviderID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// AccessPolicy is the singleton institution application policy selecting the
// authentication and account-admission capabilities currently available.
// Deployment protocol material and secrets never belong here.
type AccessPolicy struct {
	ID                               AccessPolicyID
	Revision                         int64
	CreatedAt                        time.Time
	UpdatedAt                        time.Time
	LocalLoginEnabled                bool
	PublicRegistrationEnabled        bool
	InvitationAdmissionEnabled       bool
	InvitationLocalCredentialEnabled bool
	DesktopAuthorizationEnabled      bool
	ProviderAdmissions               map[string]ProviderAdmissionMode
}

// NewInitialAccessPolicy returns the conservative policy established by the
// one-time installation bootstrap.
func NewInitialAccessPolicy(id AccessPolicyID, at time.Time) *AccessPolicy {
	at = TimeUTC(at)
	return &AccessPolicy{
		ID: id, Revision: 1, CreatedAt: at, UpdatedAt: at,
		LocalLoginEnabled: true, InvitationAdmissionEnabled: true,
		InvitationLocalCredentialEnabled: true,
		DesktopAuthorizationEnabled:      true,
		ProviderAdmissions:               map[string]ProviderAdmissionMode{},
	}
}

func (p *AccessPolicy) Validate() error {
	const where = "AccessPolicy.Validate"
	if p == nil {
		return invalidModelError(where, "access_policy", "value", "is required", "")
	}
	if !p.ID.IsValid() {
		return invalidModelError(where, "access_policy", "id", "must be a valid identifier", "")
	}
	details := "id=" + p.ID.String()
	if p.Revision < 1 {
		return invalidModelError(where, "access_policy", "revision", "must be positive", details)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
		return invalidModelError(where, "access_policy", "timestamps", "must be ordered and set", details)
	}
	if len(p.ProviderAdmissions) > AccessPolicyProviderMaxCount {
		return invalidModelError(where, "access_policy", "provider_admissions", "contains too many providers", details)
	}
	for providerID, mode := range p.ProviderAdmissions {
		if !validAccessPolicyProviderID.MatchString(providerID) {
			return invalidModelError(where, "access_policy", "provider_id", "is invalid", details)
		}
		switch mode {
		case ProviderAdmissionLinkedOnly, ProviderAdmissionInvitationRequired, ProviderAdmissionAutoProvision:
		default:
			return invalidModelError(where, "access_policy", "provider_admission", fmt.Sprintf("%q is invalid", mode), details)
		}
	}
	return nil
}

func (p *AccessPolicy) Clone() *AccessPolicy {
	if p == nil {
		return nil
	}
	clone := *p
	clone.ProviderAdmissions = make(map[string]ProviderAdmissionMode, len(p.ProviderAdmissions))
	for providerID, mode := range p.ProviderAdmissions {
		clone.ProviderAdmissions[providerID] = mode
	}
	return &clone
}

func (p *AccessPolicy) Auditable() map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": p.ID.String(), "revision": p.Revision,
		"local_login_enabled":                   p.LocalLoginEnabled,
		"public_registration_enabled":           p.PublicRegistrationEnabled,
		"invitation_admission_enabled":          p.InvitationAdmissionEnabled,
		"invitation_local_credential_enabled":   p.InvitationLocalCredentialEnabled,
		"desktop_authorization_enabled":         p.DesktopAuthorizationEnabled,
		"configured_provider_admission_entries": len(p.ProviderAdmissions),
	}
}

var _ Auditable = (*AccessPolicy)(nil)
