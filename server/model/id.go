// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"fmt"
)

//go:generate go run ./internal/idgen -source id.go -output id_gen.go

// Entity-specific identifier types share the opaque 26-character z-base-32
// representation but are not freely assignable to each other. Zero values are
// invalid; transport and persistence adapters perform textual conversion at
// their boundaries. Their repetitive constructors, parsers, and codec methods
// are generated from the explicit catalog in internal/idgen.

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

// ExamID identifies the stable authoring identity of an examination.
type ExamID string

// ExamRevisionID identifies one immutable published examination revision.
type ExamRevisionID string

// ExamSittingID identifies one scheduled delivery of an Exam Revision to one Class.
type ExamSittingID string

// ExamResourceID identifies one supporting file attached to an Exam Draft.
type ExamResourceID string

// ExamCorrectionResourceStageID identifies one purpose-bound pending resource
// upload for a single live Exam Sitting correction.
type ExamCorrectionResourceStageID string

// StarterWorkspaceEntryID identifies one logical file or directory in an Exam
// Draft's Starter Workspace.
type StarterWorkspaceEntryID string

// StarterWorkspaceObjectID identifies one staged or current Starter Workspace
// file object.
type StarterWorkspaceObjectID string

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

// FileEntryID identifies the stable logical identity of a managed file.
type FileEntryID string

// FileRevisionID identifies one immutable version of a managed file.
type FileRevisionID string

// FileRenditionID identifies one immutable representation of a file revision.
type FileRenditionID string

// UploadLeaseID identifies a bounded reservation for publishing a file revision.
type UploadLeaseID string

// idLike is implemented by every entity-specific ID for shared validation.
type idLike interface {
	~string
}

func parseID[T idLike](value, field string) (T, error) {
	var zero T
	if !IsValidId(value) {
		return zero, fmt.Errorf("model: invalid %s %q", field, value)
	}
	return T(value), nil
}

func marshalID[T idLike](id T) ([]byte, error) {
	value := string(id)
	if value != "" && !IsValidId(value) {
		return nil, fmt.Errorf("model: cannot marshal invalid id %q", value)
	}
	return []byte(value), nil
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

func marshalIDJSON[T idLike](id T) ([]byte, error) {
	value := string(id)
	if value != "" && !IsValidId(value) {
		return nil, fmt.Errorf("model: cannot marshal invalid id %q", value)
	}
	return json.Marshal(value)
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
