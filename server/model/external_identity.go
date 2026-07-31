// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	IdentityProviderMaxLength = 64
	IdentitySubjectMaxRunes   = 512
)

var validIdentityProvider = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ExternalIdentity links a User to one identity-provider subject. A user can
// have several links, while the store must keep (Provider, Subject) globally
// unique. Provider adapters may represent OIDC, CAS, SAML, LDAP, or another
// configured system without coupling the model to one protocol.
type ExternalIdentity struct {
	Id         string `json:"id"`
	CreateAt   int64  `json:"create_at"`
	UpdateAt   int64  `json:"update_at"`
	DeleteAt   int64  `json:"delete_at"`
	UserId     string `json:"user_id"`
	Provider   string `json:"provider"`
	Subject    string `json:"-"`
	LastSeenAt int64  `json:"last_seen_at,omitempty"`
}

func (ei *ExternalIdentity) PreSave() {
	preSave(&ei.Id, &ei.CreateAt, &ei.UpdateAt)
	ei.Provider = strings.ToLower(SanitizeUnicode(ei.Provider))
	if ei.LastSeenAt != 0 && ei.LastSeenAt < ei.CreateAt {
		ei.LastSeenAt = ei.CreateAt
	}
}

func (ei *ExternalIdentity) PreUpdate() {
	preUpdate(&ei.UpdateAt)
	ei.Provider = strings.ToLower(SanitizeUnicode(ei.Provider))
}

func (ei *ExternalIdentity) IsValid() *AppError {
	const where = "ExternalIdentity.IsValid"
	if appErr := validatePersistentFields(
		where,
		"external_identity",
		ei.Id,
		ei.CreateAt,
		ei.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + ei.Id
	if !IsValidId(ei.UserId) {
		return invalidModelError(where, "external_identity", "user_id", "must be a valid identifier", details)
	}
	if len(ei.Provider) == 0 ||
		len(ei.Provider) > IdentityProviderMaxLength ||
		!validIdentityProvider.MatchString(ei.Provider) {
		return invalidModelError(where, "external_identity", "provider", "has an invalid format", details)
	}
	if utf8.RuneCountInString(ei.Subject) == 0 || utf8.RuneCountInString(ei.Subject) > IdentitySubjectMaxRunes {
		return invalidModelError(where, "external_identity", "subject", "has an invalid length", details)
	}
	if !utf8.ValidString(ei.Subject) || SanitizeUnicode(ei.Subject) != ei.Subject {
		return invalidModelError(where, "external_identity", "subject", "contains unsafe characters", details)
	}
	if ei.LastSeenAt != 0 && ei.LastSeenAt < ei.CreateAt {
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

func (ei *ExternalIdentity) Auditable() map[string]any {
	fields := auditFields(ei.Id, ei.CreateAt, ei.UpdateAt, ei.DeleteAt)
	fields["user_id"] = ei.UserId
	fields["provider"] = ei.Provider
	fields["last_seen_at"] = ei.LastSeenAt
	return fields
}

var _ Auditable = (*ExternalIdentity)(nil)
