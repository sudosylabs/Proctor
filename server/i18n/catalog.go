// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package i18n owns server-side localized product copy.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const EnglishLocale = "en"

// Key identifies one closed transactional-mail meaning.
type Key string

const (
	IdentityVerifyEmail                  Key = "identity.verify_email"
	IdentityPasswordReset                Key = "identity.password_reset"
	IdentityPasswordChanged              Key = "identity.password_changed"
	IdentityEmailChangeWarningOld        Key = "identity.email_change_warning_old"
	IdentityEmailChangeVerifyNew         Key = "identity.email_change_verify_new"
	IdentityEmailVerifiedByAdmin         Key = "identity.email_verified_by_admin"
	IdentityAccountDisabled              Key = "identity.account_disabled"
	IdentityAccountEnabled               Key = "identity.account_enabled"
	IdentitySessionsRevokedByAdmin       Key = "identity.sessions_revoked_by_admin"
	IdentityMFAEnabled                   Key = "identity.mfa_enabled"
	IdentityMFADisabled                  Key = "identity.mfa_disabled"
	IdentityMFARecoveryCodesRegenerated  Key = "identity.mfa_recovery_codes_regenerated"
	IdentityPersonalAccessTokenCreated   Key = "identity.personal_access_token_created"
	IdentityPersonalAccessTokenEnabled   Key = "identity.personal_access_token_enabled"
	IdentityPersonalAccessTokenDisabled  Key = "identity.personal_access_token_disabled"
	IdentityPersonalAccessTokenRevoked   Key = "identity.personal_access_token_revoked"
	AcademicClassEnrolled                Key = "academic.class_enrolled"
	AcademicClassEnrollmentEnded         Key = "academic.class_enrollment_ended"
	AcademicClassTransferred             Key = "academic.class_transferred"
	AcademicAcademicUnitAssigned         Key = "academic.academic_unit_assigned"
	AcademicAcademicUnitAssignmentEnded  Key = "academic.academic_unit_assignment_ended"
	AuthorizationScopedRoleAssigned      Key = "authorization.scoped_role_assigned"
	AuthorizationScopedRoleEnded         Key = "authorization.scoped_role_ended"
	AuthorizationInstitutionRoleAssigned Key = "authorization.institution_role_assigned"
	AuthorizationInstitutionRoleEnded    Key = "authorization.institution_role_ended"
	AccessStudentClassInvitation         Key = "access.student_class_invitation"
	AccessTeacherAcademicUnitInvitation  Key = "access.teacher_academic_unit_invitation"
	AccessAcademicUnitRoleInvitation     Key = "access.academic_unit_role_invitation"
	AccessInstitutionRoleInvitation      Key = "access.institution_role_invitation"
	AccessInvitationAccepted             Key = "access.invitation_accepted"
	AccessInvitationRevoked              Key = "access.invitation_revoked"
	ExamManagerAdded                     Key = "exam.manager_added"
	ExamManagerRemoved                   Key = "exam.manager_removed"
	ExamOwnershipTransferredToYou        Key = "exam.ownership_transferred_to_you"
	ExamOwnershipTransferredFromYou      Key = "exam.ownership_transferred_from_you"
	ExamSittingScheduled                 Key = "exam.sitting_scheduled"
	ExamSittingRescheduled               Key = "exam.sitting_rescheduled"
	ExamSittingCancelled                 Key = "exam.sitting_cancelled"
	ExamSittingAssignmentRemoved         Key = "exam.sitting_assignment_removed"
	ExamSubmissionReceived               Key = "exam.submission_received"
	ExamSubmissionAutomaticallySealed    Key = "exam.submission_automatically_sealed"
	ExamResultReleased                   Key = "exam.result_released"
	SystemMailTest                       Key = "system.mail_test"
)

var allKeys = []Key{
	IdentityVerifyEmail, IdentityPasswordReset, IdentityPasswordChanged,
	IdentityEmailChangeWarningOld, IdentityEmailChangeVerifyNew,
	IdentityEmailVerifiedByAdmin, IdentityAccountDisabled,
	IdentityAccountEnabled, IdentitySessionsRevokedByAdmin, IdentityMFAEnabled,
	IdentityMFADisabled, IdentityMFARecoveryCodesRegenerated,
	IdentityPersonalAccessTokenCreated, IdentityPersonalAccessTokenEnabled,
	IdentityPersonalAccessTokenDisabled, IdentityPersonalAccessTokenRevoked,
	AcademicClassEnrolled, AcademicClassEnrollmentEnded, AcademicClassTransferred,
	AcademicAcademicUnitAssigned, AcademicAcademicUnitAssignmentEnded,
	AuthorizationScopedRoleAssigned, AuthorizationScopedRoleEnded,
	AuthorizationInstitutionRoleAssigned, AuthorizationInstitutionRoleEnded,
	AccessStudentClassInvitation, AccessTeacherAcademicUnitInvitation,
	AccessAcademicUnitRoleInvitation, AccessInstitutionRoleInvitation,
	AccessInvitationAccepted, AccessInvitationRevoked, ExamManagerAdded,
	ExamManagerRemoved, ExamOwnershipTransferredToYou,
	ExamOwnershipTransferredFromYou, ExamSittingScheduled,
	ExamSittingRescheduled, ExamSittingCancelled,
	ExamSittingAssignmentRemoved, ExamSubmissionReceived,
	ExamSubmissionAutomaticallySealed, ExamResultReleased, SystemMailTest,
}

// Copy is the complete, markup-free prose model shared by HTML and text mail.
type Copy struct {
	Subject             string                   `json:"subject"`
	Preheader           string                   `json:"preheader"`
	Heading             string                   `json:"heading"`
	Body                string                   `json:"body"`
	ActionLabel         string                   `json:"action_label"`
	Footer              string                   `json:"footer"`
	PersonalAccessToken *PersonalAccessTokenCopy `json:"personal_access_token,omitempty"`
}

// PersonalAccessTokenCopy contains the localized labels and closed scope
// vocabulary used by PAT transition security notices.
type PersonalAccessTokenCopy struct {
	DescriptionLabel  string `json:"description_label"`
	ExpiresAtLabel    string `json:"expires_at_label"`
	ActionAtLabel     string `json:"action_at_label"`
	ScopeLabel        string `json:"scope_label"`
	ActionCountLabel  string `json:"action_count_label"`
	InstitutionScope  string `json:"institution_scope"`
	AcademicUnitScope string `json:"academic_unit_scope"`
}

// ResolvedCopy records which locale supplied a complete copy model.
type ResolvedCopy struct {
	Locale string
	Copy   Copy
}

// Catalog resolves immutable localized copy without consulting runtime state.
type Catalog struct {
	locales map[string]map[Key]Copy
}

//go:embed catalog/*.json
var embeddedCatalogs embed.FS

// AllKeys returns the closed catalog in stable lexical order.
func AllKeys() []Key {
	keys := append([]Key(nil), allKeys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// NewCatalog validates and copies caller-provided server catalogs. It keeps
// tests and future server-owned locale additions on the production resolver.
func NewCatalog(locales map[string]map[Key]Copy) (*Catalog, error) {
	if len(locales) == 0 {
		return nil, errors.New("i18n catalog is empty")
	}
	result := &Catalog{locales: make(map[string]map[Key]Copy, len(locales))}
	for rawLocale, messages := range locales {
		locale, ok := normalizeLocale(rawLocale)
		if !ok {
			return nil, fmt.Errorf("invalid locale %q", rawLocale)
		}
		if _, exists := result.locales[locale]; exists {
			return nil, fmt.Errorf("duplicate normalized locale %q", locale)
		}
		copied := make(map[Key]Copy, len(messages))
		for key, copy := range messages {
			if strings.TrimSpace(string(key)) == "" {
				return nil, fmt.Errorf("locale %q has an empty message key", locale)
			}
			if err := copy.validate(); err != nil {
				return nil, fmt.Errorf("locale %q key %q: %w", locale, key, err)
			}
			copied[key] = copy
		}
		result.locales[locale] = copied
	}
	if _, ok := result.locales[EnglishLocale]; !ok {
		return nil, errors.New("i18n catalog requires English fallback")
	}
	return result, nil
}

// DefaultCatalog loads the server-owned catalogs embedded in the binary.
func DefaultCatalog() (*Catalog, error) {
	entries, err := embeddedCatalogs.ReadDir("catalog")
	if err != nil {
		return nil, fmt.Errorf("read embedded i18n catalogs: %w", err)
	}
	locales := make(map[string]map[Key]Copy, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".json" {
			continue
		}
		content, readErr := embeddedCatalogs.ReadFile("catalog/" + entry.Name())
		if readErr != nil {
			return nil, fmt.Errorf("read i18n catalog %q: %w", entry.Name(), readErr)
		}
		messages := make(map[Key]Copy)
		if unmarshalErr := json.Unmarshal(content, &messages); unmarshalErr != nil {
			return nil, fmt.Errorf("decode i18n catalog %q: %w", entry.Name(), unmarshalErr)
		}
		locales[strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))] = messages
	}
	catalog, err := NewCatalog(locales)
	if err != nil {
		return nil, err
	}
	english := catalog.locales[EnglishLocale]
	for _, key := range allKeys {
		if _, ok := english[key]; !ok {
			return nil, fmt.Errorf("English i18n catalog is missing %q", key)
		}
	}
	if len(english) != len(allKeys) {
		return nil, fmt.Errorf("English i18n catalog has %d keys, want %d", len(english), len(allKeys))
	}
	return catalog, nil
}

// Resolve selects a complete copy model using recipient, installation, then
// English fallback order. Language-only parents follow each requested locale.
func (c *Catalog) Resolve(key Key, recipientLocale, installationLocale string) (ResolvedCopy, error) {
	if c == nil {
		return ResolvedCopy{}, errors.New("i18n catalog is nil")
	}
	candidates := localeCandidates(recipientLocale, installationLocale)
	for _, locale := range candidates {
		messages, ok := c.locales[locale]
		if !ok {
			continue
		}
		copy, ok := messages[key]
		if ok {
			return ResolvedCopy{Locale: locale, Copy: copy}, nil
		}
	}
	return ResolvedCopy{}, fmt.Errorf("localized copy %q is unavailable", key)
}

func (c Copy) validate() error {
	fields := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "subject", value: c.Subject, required: true},
		{name: "preheader", value: c.Preheader, required: true},
		{name: "heading", value: c.Heading, required: true},
		{name: "body", value: c.Body, required: true},
		{name: "action_label", value: c.ActionLabel},
		{name: "footer", value: c.Footer, required: true},
	}
	for _, field := range fields {
		if field.required && strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is empty", field.name)
		}
		if !utf8.ValidString(field.value) || len(field.value) > 16*1024 {
			return fmt.Errorf("%s is not bounded valid UTF-8", field.name)
		}
		for _, character := range field.value {
			if unicode.IsControl(character) && character != '\n' && character != '\t' {
				return fmt.Errorf("%s contains a control character", field.name)
			}
		}
	}
	if c.PersonalAccessToken != nil {
		patFields := []struct{ name, value string }{
			{name: "personal_access_token.description_label", value: c.PersonalAccessToken.DescriptionLabel},
			{name: "personal_access_token.expires_at_label", value: c.PersonalAccessToken.ExpiresAtLabel},
			{name: "personal_access_token.action_at_label", value: c.PersonalAccessToken.ActionAtLabel},
			{name: "personal_access_token.scope_label", value: c.PersonalAccessToken.ScopeLabel},
			{name: "personal_access_token.action_count_label", value: c.PersonalAccessToken.ActionCountLabel},
			{name: "personal_access_token.institution_scope", value: c.PersonalAccessToken.InstitutionScope},
			{name: "personal_access_token.academic_unit_scope", value: c.PersonalAccessToken.AcademicUnitScope},
		}
		for _, field := range patFields {
			if strings.TrimSpace(field.value) == "" || !utf8.ValidString(field.value) || len(field.value) > 1024 {
				return fmt.Errorf("%s is empty or invalid", field.name)
			}
			for _, character := range field.value {
				if unicode.IsControl(character) {
					return fmt.Errorf("%s contains a control character", field.name)
				}
			}
		}
	}
	return nil
}

func localeCandidates(values ...string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values)*2+1)
	appendLocale := func(raw string) {
		locale, ok := normalizeLocale(raw)
		if !ok {
			return
		}
		for _, candidate := range []string{locale, strings.SplitN(locale, "-", 2)[0]} {
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	for _, value := range values {
		appendLocale(value)
	}
	appendLocale(EnglishLocale)
	return result
}

func normalizeLocale(raw string) (string, bool) {
	raw = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "_", "-"))
	if raw == "" || len(raw) > 35 || strings.HasPrefix(raw, "-") || strings.HasSuffix(raw, "-") || strings.Contains(raw, "--") {
		return "", false
	}
	for _, character := range raw {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return "", false
		}
	}
	return raw, true
}
