// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	IdentityProviderMaxLength = 64
	IdentitySubjectMaxRunes   = 512
)

var validIdentityProvider = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// IsValidIdentityProviderID reports whether value is a deployment-safe,
// immutable provider identifier. Access Policy, External Identity, and
// provider-bound Sessions deliberately share this grammar.
func IsValidIdentityProviderID(value string) bool {
	return len(value) > 0 && len(value) <= IdentityProviderMaxLength && validIdentityProvider.MatchString(value)
}

// ExternalIdentity links a User to one identity-provider subject. A user can
// have several links, while the store must keep (Provider, Subject) globally
// unique. Provider adapters may represent OIDC, CAS, SAML, LDAP, or another
// configured system without coupling the model to one protocol.
//
// Subject is deliberately excluded from JSON and must never be logged or
// audited. Soft archive uses the explicit optional ArchivedAt instant.
type ExternalIdentity struct {
	ID         ExternalIdentityID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt OptionalTime
	UserID     UserID
	Provider   string
	Subject    string `json:"-"`
	LastSeenAt OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (ei *ExternalIdentity) PrepareCreate(id ExternalIdentityID, at time.Time) {
	if ei == nil {
		return
	}
	ei.ID = id
	at = TimeUTC(at)
	ei.CreatedAt = at
	ei.UpdatedAt = at
	ei.ArchivedAt = OptionalTime{}
	ei.Provider = strings.ToLower(SanitizeUnicode(ei.Provider))
	if ei.LastSeenAt.Valid && ei.LastSeenAt.Time.Before(at) {
		ei.LastSeenAt = OptionalTimeFrom(at)
	}
}

// PrepareUpdate applies the application-selected transition time and normalizes
// the provider identifier.
func (ei *ExternalIdentity) PrepareUpdate(at time.Time) {
	if ei == nil {
		return
	}
	ei.UpdatedAt = TimeUTC(at)
	ei.Provider = strings.ToLower(SanitizeUnicode(ei.Provider))
}

// Validate checks rehydrated external-identity state.
func (ei *ExternalIdentity) Validate() error {
	const where = "ExternalIdentity.Validate"
	if ei == nil {
		return invalidModelError(where, "external_identity", "value", "is required", "")
	}
	if !ei.ID.IsValid() {
		return invalidModelError(where, "external_identity", "id", "must be a valid identifier", "")
	}
	details := "id=" + ei.ID.String()
	if ei.CreatedAt.IsZero() || ei.UpdatedAt.IsZero() {
		return invalidModelError(where, "external_identity", "created_at", "must be set", details)
	}
	if ei.UpdatedAt.Before(ei.CreatedAt) {
		return invalidModelError(where, "external_identity", "updated_at", "must not precede created_at", details)
	}
	if !ei.UserID.IsValid() {
		return invalidModelError(where, "external_identity", "user_id", "must be a valid identifier", details)
	}
	if !IsValidIdentityProviderID(ei.Provider) {
		return invalidModelError(where, "external_identity", "provider", "has an invalid format", details)
	}
	if utf8.RuneCountInString(ei.Subject) == 0 || utf8.RuneCountInString(ei.Subject) > IdentitySubjectMaxRunes {
		return invalidModelError(where, "external_identity", "subject", "has an invalid length", details)
	}
	if !utf8.ValidString(ei.Subject) || SanitizeUnicode(ei.Subject) != ei.Subject {
		return invalidModelError(where, "external_identity", "subject", "contains unsafe characters", details)
	}
	if ei.ArchivedAt.Valid && ei.ArchivedAt.Time.Before(ei.CreatedAt) {
		return invalidModelError(where, "external_identity", "archived_at", "must not precede created_at", details)
	}
	if ei.LastSeenAt.Valid && ei.LastSeenAt.Time.Before(ei.CreatedAt) {
		return invalidModelError(
			where,
			"external_identity",
			"last_seen_at",
			"must not precede create_at",
			details,
		)
	}
	return nil
}

// Auditable returns a deliberately safe audit projection. The opaque subject
// is never included.
func (ei *ExternalIdentity) Auditable() map[string]any {
	if ei == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           ei.ID.String(),
		"created_at":   MillisFromTime(ei.CreatedAt),
		"updated_at":   MillisFromTime(ei.UpdatedAt),
		"archived_at":  ei.ArchivedAt.Millis(),
		"user_id":      ei.UserID.String(),
		"provider":     ei.Provider,
		"last_seen_at": ei.LastSeenAt.Millis(),
	}
}

var _ Auditable = (*ExternalIdentity)(nil)
