// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

type idSpec struct {
	typeName           string
	fieldName          string
	constructorSubject string
}

// entityIDs is the explicit source of truth for generated typed-ID mechanics.
// Type declarations and their domain documentation remain handwritten in
// model/id.go so adding an identifier is still a deliberate domain decision.
var entityIDs = []idSpec{
	{typeName: "InstitutionID", fieldName: "institution_id", constructorSubject: "institution"},
	{typeName: "AcademicUnitID", fieldName: "academic_unit_id", constructorSubject: "academic-unit"},
	{typeName: "ProgrammeID", fieldName: "programme_id", constructorSubject: "programme"},
	{typeName: "ProgrammeLevelID", fieldName: "programme_level_id", constructorSubject: "programme-level"},
	{typeName: "ClassID", fieldName: "class_id", constructorSubject: "class"},
	{typeName: "AcademicPeriodID", fieldName: "academic_period_id", constructorSubject: "academic-period"},
	{typeName: "ExamID", fieldName: "exam_id", constructorSubject: "exam"},
	{typeName: "ExamRevisionID", fieldName: "exam_revision_id", constructorSubject: "exam-revision"},
	{typeName: "ExamResourceID", fieldName: "exam_resource_id", constructorSubject: "exam-resource"},
	{typeName: "StarterWorkspaceEntryID", fieldName: "starter_workspace_entry_id", constructorSubject: "starter-workspace-entry"},
	{typeName: "StarterWorkspaceObjectID", fieldName: "starter_workspace_object_id", constructorSubject: "starter-workspace-object"},
	{typeName: "UserID", fieldName: "user_id", constructorSubject: "user"},
	{typeName: "SessionID", fieldName: "session_id", constructorSubject: "session"},
	{typeName: "SessionCredentialID", fieldName: "session_credential_id", constructorSubject: "session-credential"},
	{typeName: "RoleID", fieldName: "role_id", constructorSubject: "role"},
	{typeName: "RoleBindingID", fieldName: "role_binding_id", constructorSubject: "role-binding"},
	{typeName: "AffiliationID", fieldName: "affiliation_id", constructorSubject: "affiliation"},
	{typeName: "AcademicUnitMemberID", fieldName: "academic_unit_member_id", constructorSubject: "academic-unit-member"},
	{typeName: "ClassMemberID", fieldName: "class_member_id", constructorSubject: "class-member"},
	{typeName: "ExternalIdentityID", fieldName: "external_identity_id", constructorSubject: "external-identity"},
	{typeName: "PasswordCredentialID", fieldName: "password_credential_id", constructorSubject: "password-credential"},
	{typeName: "PersonalAccessTokenID", fieldName: "personal_access_token_id", constructorSubject: "personal-access-token"},
	{typeName: "UserTokenID", fieldName: "user_token_id", constructorSubject: "user-token"},
	{typeName: "MFACredentialID", fieldName: "mfa_credential_id", constructorSubject: "MFA credential"},
	{typeName: "MFARecoveryCodeID", fieldName: "mfa_recovery_code_id", constructorSubject: "MFA recovery-code"},
	{typeName: "AuditEventID", fieldName: "audit_event_id", constructorSubject: "audit-event"},
	{typeName: "ExternalLoginStateID", fieldName: "external_login_state_id", constructorSubject: "external-login-state"},
	{typeName: "FileEntryID", fieldName: "file_entry_id", constructorSubject: "file-entry"},
	{typeName: "FileRevisionID", fieldName: "file_revision_id", constructorSubject: "file-revision"},
	{typeName: "FileRenditionID", fieldName: "file_rendition_id", constructorSubject: "file-rendition"},
	{typeName: "UploadLeaseID", fieldName: "upload_lease_id", constructorSubject: "upload-lease"},
}
