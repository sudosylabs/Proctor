// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package templates

import "sort"

// Key identifies one closed transactional-mail template family.
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
	IdentityEmailVerifiedByAdmin, IdentityAccountDisabled, IdentityAccountEnabled,
	IdentitySessionsRevokedByAdmin, IdentityMFAEnabled, IdentityMFADisabled,
	IdentityMFARecoveryCodesRegenerated, IdentityPersonalAccessTokenCreated,
	IdentityPersonalAccessTokenEnabled, IdentityPersonalAccessTokenDisabled,
	IdentityPersonalAccessTokenRevoked, AcademicClassEnrolled,
	AcademicClassEnrollmentEnded, AcademicClassTransferred,
	AcademicAcademicUnitAssigned, AcademicAcademicUnitAssignmentEnded,
	AuthorizationScopedRoleAssigned, AuthorizationScopedRoleEnded,
	AuthorizationInstitutionRoleAssigned, AuthorizationInstitutionRoleEnded,
	AccessStudentClassInvitation, AccessTeacherAcademicUnitInvitation,
	AccessAcademicUnitRoleInvitation, AccessInstitutionRoleInvitation,
	AccessInvitationAccepted, AccessInvitationRevoked, ExamManagerAdded,
	ExamManagerRemoved, ExamOwnershipTransferredToYou,
	ExamOwnershipTransferredFromYou, ExamSittingScheduled,
	ExamSittingRescheduled, ExamSittingCancelled, ExamSittingAssignmentRemoved,
	ExamSubmissionReceived, ExamSubmissionAutomaticallySealed,
	ExamResultReleased, SystemMailTest,
}

// AllKeys returns the closed mail-template catalog in lexical order.
func AllKeys() []Key {
	keys := append([]Key(nil), allKeys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
