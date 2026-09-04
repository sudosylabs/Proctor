// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/identityprovider"
)

// ProviderAdmissionMode is the closed policy vocabulary for one configured
// external provider. An absent provider entry is disabled.
type ProviderAdmissionMode string

const (
	ProviderAdmissionLinkedOnly         ProviderAdmissionMode = "linked_only"
	ProviderAdmissionInvitationRequired ProviderAdmissionMode = "invitation_required"
	ProviderAdmissionAutoProvision      ProviderAdmissionMode = "auto_provision"
)

const (
	AccessPolicyProviderMaxCount       = identityprovider.MaximumCount
	AccessPolicyTransitionHistoryLimit = 100
)

var (
	ErrAccessPolicyRevisionConflict  = errors.New("access policy revision conflict")
	ErrAccessPolicyInvalidTransition = errors.New("access policy transition is invalid")
)

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

// AccessPolicySettings is the complete replaceable portion of AccessPolicy.
// A complete value keeps policy updates explicit as new capabilities are added.
type AccessPolicySettings struct {
	LocalLoginEnabled                bool
	PublicRegistrationEnabled        bool
	InvitationAdmissionEnabled       bool
	InvitationLocalCredentialEnabled bool
	DesktopAuthorizationEnabled      bool
	ProviderAdmissions               map[string]ProviderAdmissionMode
}

type AccessPolicyTransitionOutcome string

const AccessPolicyTransitionApplied AccessPolicyTransitionOutcome = "applied"

// AccessPolicyTransition is the bounded, secret-free history fact retained for
// a successful policy replacement. Failed attempts remain in durable audit.
type AccessPolicyTransition struct {
	PolicyID      AccessPolicyID
	FromRevision  int64
	ToRevision    int64
	ActorID       UserID
	ChangedFields []string
	ChangedAt     time.Time
	Outcome       AccessPolicyTransitionOutcome
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
		if !IsValidIdentityProviderID(providerID) {
			return invalidModelError(where, "access_policy", "provider_id", "is invalid", details)
		}
		switch mode {
		case ProviderAdmissionLinkedOnly, ProviderAdmissionInvitationRequired, ProviderAdmissionAutoProvision:
		default:
			return invalidModelError(where, "access_policy", "provider_admission", fmt.Sprintf("%q is invalid", mode), details)
		}
	}
	if err := p.Settings().Validate(); err != nil {
		return invalidModelError(where, "access_policy", "settings", err.Error(), details)
	}
	return nil
}

func (s AccessPolicySettings) Validate() error {
	if len(s.ProviderAdmissions) > AccessPolicyProviderMaxCount {
		return errors.New("contains too many provider admissions")
	}
	if s.PublicRegistrationEnabled && !s.LocalLoginEnabled {
		return errors.New("public registration requires local login")
	}
	if s.InvitationLocalCredentialEnabled &&
		(!s.InvitationAdmissionEnabled || !s.LocalLoginEnabled) {
		return errors.New("invitation local credentials require invitation admission and local login")
	}
	for providerID, mode := range s.ProviderAdmissions {
		if !IsValidIdentityProviderID(providerID) {
			return errors.New("provider ID is invalid")
		}
		switch mode {
		case ProviderAdmissionLinkedOnly, ProviderAdmissionInvitationRequired, ProviderAdmissionAutoProvision:
		default:
			return fmt.Errorf("provider admission %q is invalid", mode)
		}
	}
	return nil
}

func (p *AccessPolicy) Settings() AccessPolicySettings {
	if p == nil {
		return AccessPolicySettings{}
	}
	return AccessPolicySettings{
		LocalLoginEnabled:                p.LocalLoginEnabled,
		PublicRegistrationEnabled:        p.PublicRegistrationEnabled,
		InvitationAdmissionEnabled:       p.InvitationAdmissionEnabled,
		InvitationLocalCredentialEnabled: p.InvitationLocalCredentialEnabled,
		DesktopAuthorizationEnabled:      p.DesktopAuthorizationEnabled,
		ProviderAdmissions:               cloneProviderAdmissions(p.ProviderAdmissions),
	}
}

// Replace applies one complete, revision-fenced policy replacement and returns
// the only safe history fields that persistence may retain.
func (p *AccessPolicy) Replace(expectedRevision int64, settings AccessPolicySettings, actorID UserID, at time.Time) (*AccessPolicyTransition, error) {
	if p == nil || p.Validate() != nil || !actorID.IsValid() || at.IsZero() {
		return nil, ErrAccessPolicyInvalidTransition
	}
	if expectedRevision != p.Revision {
		return nil, ErrAccessPolicyRevisionConflict
	}
	at = TimeUTC(at)
	if at.Before(p.UpdatedAt) || settings.Validate() != nil {
		return nil, ErrAccessPolicyInvalidTransition
	}
	changed := changedAccessPolicyFields(p.Settings(), settings)
	if len(changed) == 0 {
		return nil, nil
	}
	from := p.Revision
	p.LocalLoginEnabled = settings.LocalLoginEnabled
	p.PublicRegistrationEnabled = settings.PublicRegistrationEnabled
	p.InvitationAdmissionEnabled = settings.InvitationAdmissionEnabled
	p.InvitationLocalCredentialEnabled = settings.InvitationLocalCredentialEnabled
	p.DesktopAuthorizationEnabled = settings.DesktopAuthorizationEnabled
	p.ProviderAdmissions = cloneProviderAdmissions(settings.ProviderAdmissions)
	p.Revision++
	p.UpdatedAt = at
	if err := p.Validate(); err != nil {
		return nil, ErrAccessPolicyInvalidTransition
	}
	return &AccessPolicyTransition{
		PolicyID: p.ID, FromRevision: from, ToRevision: p.Revision,
		ActorID: actorID, ChangedFields: changed, ChangedAt: at,
		Outcome: AccessPolicyTransitionApplied,
	}, nil
}

func (t *AccessPolicyTransition) Validate() error {
	if t == nil || !t.PolicyID.IsValid() || t.FromRevision < 1 ||
		t.ToRevision != t.FromRevision+1 || !t.ActorID.IsValid() ||
		t.ChangedAt.IsZero() || t.Outcome != AccessPolicyTransitionApplied ||
		len(t.ChangedFields) == 0 || len(t.ChangedFields) > 7 {
		return ErrAccessPolicyInvalidTransition
	}
	valid := map[string]bool{
		"local_login_enabled": true, "public_registration_enabled": true,
		"invitation_admission_enabled": true, "invitation_local_credential_enabled": true,
		"desktop_authorization_enabled": true, "provider_admissions": true,
		"revoke_existing_sessions": true,
	}
	previous := ""
	for _, field := range t.ChangedFields {
		if !valid[field] || field <= previous {
			return ErrAccessPolicyInvalidTransition
		}
		previous = field
	}
	return nil
}

func changedAccessPolicyFields(current, next AccessPolicySettings) []string {
	changed := make([]string, 0, 6)
	if current.LocalLoginEnabled != next.LocalLoginEnabled {
		changed = append(changed, "local_login_enabled")
	}
	if current.PublicRegistrationEnabled != next.PublicRegistrationEnabled {
		changed = append(changed, "public_registration_enabled")
	}
	if current.InvitationAdmissionEnabled != next.InvitationAdmissionEnabled {
		changed = append(changed, "invitation_admission_enabled")
	}
	if current.InvitationLocalCredentialEnabled != next.InvitationLocalCredentialEnabled {
		changed = append(changed, "invitation_local_credential_enabled")
	}
	if current.DesktopAuthorizationEnabled != next.DesktopAuthorizationEnabled {
		changed = append(changed, "desktop_authorization_enabled")
	}
	if !providerAdmissionsEqual(current.ProviderAdmissions, next.ProviderAdmissions) {
		changed = append(changed, "provider_admissions")
	}
	slices.Sort(changed)
	return changed
}

func providerAdmissionsEqual(left, right map[string]ProviderAdmissionMode) bool {
	if len(left) != len(right) {
		return false
	}
	for id, mode := range left {
		if right[id] != mode {
			return false
		}
	}
	return true
}

func cloneProviderAdmissions(source map[string]ProviderAdmissionMode) map[string]ProviderAdmissionMode {
	clone := make(map[string]ProviderAdmissionMode, len(source))
	for id, mode := range source {
		clone[id] = mode
	}
	return clone
}

func (p *AccessPolicy) Clone() *AccessPolicy {
	if p == nil {
		return nil
	}
	clone := *p
	clone.ProviderAdmissions = cloneProviderAdmissions(p.ProviderAdmissions)
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
