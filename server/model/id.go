// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"fmt"
)

// Entity-specific identifier types share the opaque 26-character z-base-32
// representation (ADR-0021) but are not freely assignable to each other
// (ADR-0022). Zero values are invalid; transport and persistence adapters
// perform textual conversion at their boundaries.

// InstitutionID identifies the singleton installation institution.
type InstitutionID string

// AcademicUnitID identifies a hierarchical academic unit.
type AcademicUnitID string

// ProgrammeID identifies a programme of study.
type ProgrammeID string

// ProgrammeLevelID identifies a curriculum stage within a programme.
type ProgrammeLevelID string

// ClassID identifies a time-bound class roster.
type ClassID string

// AcademicPeriodID identifies an academic enrollment period.
type AcademicPeriodID string

// UserID identifies a user account.
type UserID string

// SessionID identifies a server-side session.
type SessionID string

// SessionCredentialID identifies a hashed access or refresh credential.
type SessionCredentialID string

// RoleID identifies a role.
type RoleID string

// RoleBindingID identifies a scoped role assignment.
type RoleBindingID string

// AffiliationID identifies a user's institutional affiliation.
type AffiliationID string

// AcademicUnitMemberID identifies organizational membership.
type AcademicUnitMemberID string

// ClassMemberID identifies student class enrollment.
type ClassMemberID string

// ExternalIdentityID identifies a linked external-provider subject.
type ExternalIdentityID string

// PasswordCredentialID identifies a local password credential row.
type PasswordCredentialID string

// PersonalAccessTokenID identifies a personal access token.
type PersonalAccessTokenID string

// UserTokenID identifies a purpose-specific user token.
type UserTokenID string

// MFACredentialID identifies an encrypted MFA credential.
type MFACredentialID string

// MFARecoveryCodeID identifies a hashed MFA recovery code.
type MFARecoveryCodeID string

// AuditEventID identifies a durable audit event.
type AuditEventID string

// ExternalLoginStateID identifies a one-use external login transaction.
type ExternalLoginStateID string

// idLike is implemented by every entity-specific ID for shared validation.
type idLike interface {
	~string
}

// NewInstitutionID returns a freshly generated institution identifier.
func NewInstitutionID() InstitutionID { return InstitutionID(NewId()) }

// NewAcademicUnitID returns a freshly generated academic-unit identifier.
func NewAcademicUnitID() AcademicUnitID { return AcademicUnitID(NewId()) }

// NewProgrammeID returns a freshly generated programme identifier.
func NewProgrammeID() ProgrammeID { return ProgrammeID(NewId()) }

// NewProgrammeLevelID returns a freshly generated programme-level identifier.
func NewProgrammeLevelID() ProgrammeLevelID { return ProgrammeLevelID(NewId()) }

// NewClassID returns a freshly generated class identifier.
func NewClassID() ClassID { return ClassID(NewId()) }

// NewAcademicPeriodID returns a freshly generated academic-period identifier.
func NewAcademicPeriodID() AcademicPeriodID { return AcademicPeriodID(NewId()) }

// NewUserID returns a freshly generated user identifier.
func NewUserID() UserID { return UserID(NewId()) }

// NewSessionID returns a freshly generated session identifier.
func NewSessionID() SessionID { return SessionID(NewId()) }

// NewSessionCredentialID returns a freshly generated session-credential identifier.
func NewSessionCredentialID() SessionCredentialID { return SessionCredentialID(NewId()) }

// NewRoleID returns a freshly generated role identifier.
func NewRoleID() RoleID { return RoleID(NewId()) }

// NewRoleBindingID returns a freshly generated role-binding identifier.
func NewRoleBindingID() RoleBindingID { return RoleBindingID(NewId()) }

// NewAffiliationID returns a freshly generated affiliation identifier.
func NewAffiliationID() AffiliationID { return AffiliationID(NewId()) }

// NewAcademicUnitMemberID returns a freshly generated academic-unit-member identifier.
func NewAcademicUnitMemberID() AcademicUnitMemberID { return AcademicUnitMemberID(NewId()) }

// NewClassMemberID returns a freshly generated class-member identifier.
func NewClassMemberID() ClassMemberID { return ClassMemberID(NewId()) }

// NewExternalIdentityID returns a freshly generated external-identity identifier.
func NewExternalIdentityID() ExternalIdentityID { return ExternalIdentityID(NewId()) }

// NewPasswordCredentialID returns a freshly generated password-credential identifier.
func NewPasswordCredentialID() PasswordCredentialID { return PasswordCredentialID(NewId()) }

// NewPersonalAccessTokenID returns a freshly generated personal-access-token identifier.
func NewPersonalAccessTokenID() PersonalAccessTokenID { return PersonalAccessTokenID(NewId()) }

// NewUserTokenID returns a freshly generated user-token identifier.
func NewUserTokenID() UserTokenID { return UserTokenID(NewId()) }

// NewMFACredentialID returns a freshly generated MFA credential identifier.
func NewMFACredentialID() MFACredentialID { return MFACredentialID(NewId()) }

// NewMFARecoveryCodeID returns a freshly generated MFA recovery-code identifier.
func NewMFARecoveryCodeID() MFARecoveryCodeID { return MFARecoveryCodeID(NewId()) }

// NewAuditEventID returns a freshly generated audit-event identifier.
func NewAuditEventID() AuditEventID { return AuditEventID(NewId()) }

// NewExternalLoginStateID returns a freshly generated external-login-state identifier.
func NewExternalLoginStateID() ExternalLoginStateID { return ExternalLoginStateID(NewId()) }

// Parse helpers validate the shared wire/database representation.

func ParseInstitutionID(value string) (InstitutionID, error) {
	return parseID[InstitutionID](value, "institution_id")
}
func ParseAcademicUnitID(value string) (AcademicUnitID, error) {
	return parseID[AcademicUnitID](value, "academic_unit_id")
}
func ParseProgrammeID(value string) (ProgrammeID, error) {
	return parseID[ProgrammeID](value, "programme_id")
}
func ParseProgrammeLevelID(value string) (ProgrammeLevelID, error) {
	return parseID[ProgrammeLevelID](value, "programme_level_id")
}
func ParseClassID(value string) (ClassID, error) {
	return parseID[ClassID](value, "class_id")
}
func ParseAcademicPeriodID(value string) (AcademicPeriodID, error) {
	return parseID[AcademicPeriodID](value, "academic_period_id")
}
func ParseUserID(value string) (UserID, error) {
	return parseID[UserID](value, "user_id")
}
func ParseSessionID(value string) (SessionID, error) {
	return parseID[SessionID](value, "session_id")
}
func ParseSessionCredentialID(value string) (SessionCredentialID, error) {
	return parseID[SessionCredentialID](value, "session_credential_id")
}
func ParseRoleID(value string) (RoleID, error) {
	return parseID[RoleID](value, "role_id")
}
func ParseRoleBindingID(value string) (RoleBindingID, error) {
	return parseID[RoleBindingID](value, "role_binding_id")
}
func ParseAffiliationID(value string) (AffiliationID, error) {
	return parseID[AffiliationID](value, "affiliation_id")
}
func ParseAcademicUnitMemberID(value string) (AcademicUnitMemberID, error) {
	return parseID[AcademicUnitMemberID](value, "academic_unit_member_id")
}
func ParseClassMemberID(value string) (ClassMemberID, error) {
	return parseID[ClassMemberID](value, "class_member_id")
}
func ParseExternalIdentityID(value string) (ExternalIdentityID, error) {
	return parseID[ExternalIdentityID](value, "external_identity_id")
}
func ParsePasswordCredentialID(value string) (PasswordCredentialID, error) {
	return parseID[PasswordCredentialID](value, "password_credential_id")
}
func ParsePersonalAccessTokenID(value string) (PersonalAccessTokenID, error) {
	return parseID[PersonalAccessTokenID](value, "personal_access_token_id")
}
func ParseUserTokenID(value string) (UserTokenID, error) {
	return parseID[UserTokenID](value, "user_token_id")
}
func ParseMFACredentialID(value string) (MFACredentialID, error) {
	return parseID[MFACredentialID](value, "mfa_credential_id")
}
func ParseMFARecoveryCodeID(value string) (MFARecoveryCodeID, error) {
	return parseID[MFARecoveryCodeID](value, "mfa_recovery_code_id")
}
func ParseAuditEventID(value string) (AuditEventID, error) {
	return parseID[AuditEventID](value, "audit_event_id")
}
func ParseExternalLoginStateID(value string) (ExternalLoginStateID, error) {
	return parseID[ExternalLoginStateID](value, "external_login_state_id")
}

func parseID[T idLike](value, field string) (T, error) {
	var zero T
	if !IsValidId(value) {
		return zero, fmt.Errorf("model: invalid %s %q", field, value)
	}
	return T(value), nil
}

// IsZero reports whether the identifier is the empty zero value.
func (id InstitutionID) IsZero() bool         { return id == "" }
func (id AcademicUnitID) IsZero() bool        { return id == "" }
func (id ProgrammeID) IsZero() bool           { return id == "" }
func (id ProgrammeLevelID) IsZero() bool      { return id == "" }
func (id ClassID) IsZero() bool               { return id == "" }
func (id AcademicPeriodID) IsZero() bool      { return id == "" }
func (id UserID) IsZero() bool                { return id == "" }
func (id SessionID) IsZero() bool             { return id == "" }
func (id SessionCredentialID) IsZero() bool   { return id == "" }
func (id RoleID) IsZero() bool                { return id == "" }
func (id RoleBindingID) IsZero() bool         { return id == "" }
func (id AffiliationID) IsZero() bool         { return id == "" }
func (id AcademicUnitMemberID) IsZero() bool  { return id == "" }
func (id ClassMemberID) IsZero() bool         { return id == "" }
func (id ExternalIdentityID) IsZero() bool    { return id == "" }
func (id PasswordCredentialID) IsZero() bool  { return id == "" }
func (id PersonalAccessTokenID) IsZero() bool { return id == "" }
func (id UserTokenID) IsZero() bool           { return id == "" }
func (id MFACredentialID) IsZero() bool       { return id == "" }
func (id MFARecoveryCodeID) IsZero() bool     { return id == "" }
func (id AuditEventID) IsZero() bool          { return id == "" }
func (id ExternalLoginStateID) IsZero() bool  { return id == "" }

// IsValid reports whether the identifier is a canonical non-zero ID.
func (id InstitutionID) IsValid() bool         { return IsValidId(string(id)) }
func (id AcademicUnitID) IsValid() bool        { return IsValidId(string(id)) }
func (id ProgrammeID) IsValid() bool           { return IsValidId(string(id)) }
func (id ProgrammeLevelID) IsValid() bool      { return IsValidId(string(id)) }
func (id ClassID) IsValid() bool               { return IsValidId(string(id)) }
func (id AcademicPeriodID) IsValid() bool      { return IsValidId(string(id)) }
func (id UserID) IsValid() bool                { return IsValidId(string(id)) }
func (id SessionID) IsValid() bool             { return IsValidId(string(id)) }
func (id SessionCredentialID) IsValid() bool   { return IsValidId(string(id)) }
func (id RoleID) IsValid() bool                { return IsValidId(string(id)) }
func (id RoleBindingID) IsValid() bool         { return IsValidId(string(id)) }
func (id AffiliationID) IsValid() bool         { return IsValidId(string(id)) }
func (id AcademicUnitMemberID) IsValid() bool  { return IsValidId(string(id)) }
func (id ClassMemberID) IsValid() bool         { return IsValidId(string(id)) }
func (id ExternalIdentityID) IsValid() bool    { return IsValidId(string(id)) }
func (id PasswordCredentialID) IsValid() bool  { return IsValidId(string(id)) }
func (id PersonalAccessTokenID) IsValid() bool { return IsValidId(string(id)) }
func (id UserTokenID) IsValid() bool           { return IsValidId(string(id)) }
func (id MFACredentialID) IsValid() bool       { return IsValidId(string(id)) }
func (id MFARecoveryCodeID) IsValid() bool     { return IsValidId(string(id)) }
func (id AuditEventID) IsValid() bool          { return IsValidId(string(id)) }
func (id ExternalLoginStateID) IsValid() bool  { return IsValidId(string(id)) }

// String returns the wire/database representation.
func (id InstitutionID) String() string         { return string(id) }
func (id AcademicUnitID) String() string        { return string(id) }
func (id ProgrammeID) String() string           { return string(id) }
func (id ProgrammeLevelID) String() string      { return string(id) }
func (id ClassID) String() string               { return string(id) }
func (id AcademicPeriodID) String() string      { return string(id) }
func (id UserID) String() string                { return string(id) }
func (id SessionID) String() string             { return string(id) }
func (id SessionCredentialID) String() string   { return string(id) }
func (id RoleID) String() string                { return string(id) }
func (id RoleBindingID) String() string         { return string(id) }
func (id AffiliationID) String() string         { return string(id) }
func (id AcademicUnitMemberID) String() string  { return string(id) }
func (id ClassMemberID) String() string         { return string(id) }
func (id ExternalIdentityID) String() string    { return string(id) }
func (id PasswordCredentialID) String() string  { return string(id) }
func (id PersonalAccessTokenID) String() string { return string(id) }
func (id UserTokenID) String() string           { return string(id) }
func (id MFACredentialID) String() string       { return string(id) }
func (id MFARecoveryCodeID) String() string     { return string(id) }
func (id AuditEventID) String() string          { return string(id) }
func (id ExternalLoginStateID) String() string  { return string(id) }

// MarshalText encodes the ID for JSON/text codecs as its canonical string.
func (id InstitutionID) MarshalText() ([]byte, error)         { return marshalID(id) }
func (id AcademicUnitID) MarshalText() ([]byte, error)        { return marshalID(id) }
func (id ProgrammeID) MarshalText() ([]byte, error)           { return marshalID(id) }
func (id ProgrammeLevelID) MarshalText() ([]byte, error)      { return marshalID(id) }
func (id ClassID) MarshalText() ([]byte, error)               { return marshalID(id) }
func (id AcademicPeriodID) MarshalText() ([]byte, error)      { return marshalID(id) }
func (id UserID) MarshalText() ([]byte, error)                { return marshalID(id) }
func (id SessionID) MarshalText() ([]byte, error)             { return marshalID(id) }
func (id SessionCredentialID) MarshalText() ([]byte, error)   { return marshalID(id) }
func (id RoleID) MarshalText() ([]byte, error)                { return marshalID(id) }
func (id RoleBindingID) MarshalText() ([]byte, error)         { return marshalID(id) }
func (id AffiliationID) MarshalText() ([]byte, error)         { return marshalID(id) }
func (id AcademicUnitMemberID) MarshalText() ([]byte, error)  { return marshalID(id) }
func (id ClassMemberID) MarshalText() ([]byte, error)         { return marshalID(id) }
func (id ExternalIdentityID) MarshalText() ([]byte, error)    { return marshalID(id) }
func (id PasswordCredentialID) MarshalText() ([]byte, error)  { return marshalID(id) }
func (id PersonalAccessTokenID) MarshalText() ([]byte, error) { return marshalID(id) }
func (id UserTokenID) MarshalText() ([]byte, error)           { return marshalID(id) }
func (id MFACredentialID) MarshalText() ([]byte, error)       { return marshalID(id) }
func (id MFARecoveryCodeID) MarshalText() ([]byte, error)     { return marshalID(id) }
func (id AuditEventID) MarshalText() ([]byte, error)          { return marshalID(id) }
func (id ExternalLoginStateID) MarshalText() ([]byte, error)  { return marshalID(id) }

func marshalID[T idLike](id T) ([]byte, error) {
	value := string(id)
	if value != "" && !IsValidId(value) {
		return nil, fmt.Errorf("model: cannot marshal invalid id %q", value)
	}
	return []byte(value), nil
}

// UnmarshalText decodes a wire ID and validates it when non-empty.
func (id *InstitutionID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseInstitutionID)
}
func (id *AcademicUnitID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseAcademicUnitID)
}
func (id *ProgrammeID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseProgrammeID)
}
func (id *ProgrammeLevelID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseProgrammeLevelID)
}
func (id *ClassID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseClassID)
}
func (id *AcademicPeriodID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseAcademicPeriodID)
}
func (id *UserID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseUserID)
}
func (id *SessionID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseSessionID)
}
func (id *SessionCredentialID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseSessionCredentialID)
}
func (id *RoleID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseRoleID)
}
func (id *RoleBindingID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseRoleBindingID)
}
func (id *AffiliationID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseAffiliationID)
}
func (id *AcademicUnitMemberID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseAcademicUnitMemberID)
}
func (id *ClassMemberID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseClassMemberID)
}
func (id *ExternalIdentityID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseExternalIdentityID)
}
func (id *PasswordCredentialID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParsePasswordCredentialID)
}
func (id *PersonalAccessTokenID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParsePersonalAccessTokenID)
}
func (id *UserTokenID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseUserTokenID)
}
func (id *MFACredentialID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseMFACredentialID)
}
func (id *MFARecoveryCodeID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseMFARecoveryCodeID)
}
func (id *AuditEventID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseAuditEventID)
}
func (id *ExternalLoginStateID) UnmarshalText(data []byte) error {
	return unmarshalID(id, data, ParseExternalLoginStateID)
}

func unmarshalID[T idLike](target *T, data []byte, parse func(string) (T, error)) error {
	if target == nil {
		return fmt.Errorf("model: id unmarshal target is nil")
	}
	if len(data) == 0 {
		*target = T("")
		return nil
	}
	parsed, err := parse(string(data))
	if err != nil {
		return err
	}
	*target = parsed
	return nil
}

// Ensure typed IDs marshal as JSON strings, not objects.
var (
	_ json.Marshaler   = InstitutionID("")
	_ json.Unmarshaler = (*InstitutionID)(nil)
)

// MarshalJSON and UnmarshalJSON keep empty IDs as "" and reject invalid non-empty values.
func (id InstitutionID) MarshalJSON() ([]byte, error)         { return marshalIDJSON(id) }
func (id AcademicUnitID) MarshalJSON() ([]byte, error)        { return marshalIDJSON(id) }
func (id ProgrammeID) MarshalJSON() ([]byte, error)           { return marshalIDJSON(id) }
func (id ProgrammeLevelID) MarshalJSON() ([]byte, error)      { return marshalIDJSON(id) }
func (id ClassID) MarshalJSON() ([]byte, error)               { return marshalIDJSON(id) }
func (id AcademicPeriodID) MarshalJSON() ([]byte, error)      { return marshalIDJSON(id) }
func (id UserID) MarshalJSON() ([]byte, error)                { return marshalIDJSON(id) }
func (id SessionID) MarshalJSON() ([]byte, error)             { return marshalIDJSON(id) }
func (id SessionCredentialID) MarshalJSON() ([]byte, error)   { return marshalIDJSON(id) }
func (id RoleID) MarshalJSON() ([]byte, error)                { return marshalIDJSON(id) }
func (id RoleBindingID) MarshalJSON() ([]byte, error)         { return marshalIDJSON(id) }
func (id AffiliationID) MarshalJSON() ([]byte, error)         { return marshalIDJSON(id) }
func (id AcademicUnitMemberID) MarshalJSON() ([]byte, error)  { return marshalIDJSON(id) }
func (id ClassMemberID) MarshalJSON() ([]byte, error)         { return marshalIDJSON(id) }
func (id ExternalIdentityID) MarshalJSON() ([]byte, error)    { return marshalIDJSON(id) }
func (id PasswordCredentialID) MarshalJSON() ([]byte, error)  { return marshalIDJSON(id) }
func (id PersonalAccessTokenID) MarshalJSON() ([]byte, error) { return marshalIDJSON(id) }
func (id UserTokenID) MarshalJSON() ([]byte, error)           { return marshalIDJSON(id) }
func (id MFACredentialID) MarshalJSON() ([]byte, error)       { return marshalIDJSON(id) }
func (id MFARecoveryCodeID) MarshalJSON() ([]byte, error)     { return marshalIDJSON(id) }
func (id AuditEventID) MarshalJSON() ([]byte, error)          { return marshalIDJSON(id) }
func (id ExternalLoginStateID) MarshalJSON() ([]byte, error)  { return marshalIDJSON(id) }

func marshalIDJSON[T idLike](id T) ([]byte, error) {
	value := string(id)
	if value != "" && !IsValidId(value) {
		return nil, fmt.Errorf("model: cannot marshal invalid id %q", value)
	}
	return json.Marshal(value)
}

func (id *InstitutionID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseInstitutionID)
}
func (id *AcademicUnitID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseAcademicUnitID)
}
func (id *ProgrammeID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseProgrammeID)
}
func (id *ProgrammeLevelID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseProgrammeLevelID)
}
func (id *ClassID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseClassID)
}
func (id *AcademicPeriodID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseAcademicPeriodID)
}
func (id *UserID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseUserID)
}
func (id *SessionID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseSessionID)
}
func (id *SessionCredentialID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseSessionCredentialID)
}
func (id *RoleID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseRoleID)
}
func (id *RoleBindingID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseRoleBindingID)
}
func (id *AffiliationID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseAffiliationID)
}
func (id *AcademicUnitMemberID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseAcademicUnitMemberID)
}
func (id *ClassMemberID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseClassMemberID)
}
func (id *ExternalIdentityID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseExternalIdentityID)
}
func (id *PasswordCredentialID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParsePasswordCredentialID)
}
func (id *PersonalAccessTokenID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParsePersonalAccessTokenID)
}
func (id *UserTokenID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseUserTokenID)
}
func (id *MFACredentialID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseMFACredentialID)
}
func (id *MFARecoveryCodeID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseMFARecoveryCodeID)
}
func (id *AuditEventID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseAuditEventID)
}
func (id *ExternalLoginStateID) UnmarshalJSON(data []byte) error {
	return unmarshalIDJSON(id, data, ParseExternalLoginStateID)
}

func unmarshalIDJSON[T idLike](target *T, data []byte, parse func(string) (T, error)) error {
	if target == nil {
		return fmt.Errorf("model: id unmarshal target is nil")
	}
	if string(data) == "null" {
		*target = T("")
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		*target = T("")
		return nil
	}
	parsed, err := parse(raw)
	if err != nil {
		return err
	}
	*target = parsed
	return nil
}
