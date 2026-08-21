// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	MailRenderedPayloadMaximumBytes       = 1 << 20
	MailEncryptedPayloadMaximumBytes      = 2 << 20
	MailEncryptedFanoutBundleMaximumBytes = 4 << 20
	MailMaskedRecipientMaximumBytes       = 254
	MailMessageIDMaximumBytes             = 900
	MailMaximumAttempts                   = 8
)

type MailTemplateKey string
type MailOccurrenceKind string
type MailDeliveryState string

func (key MailTemplateKey) IsValid() bool {
	switch key {
	case MailTemplateSystemTest,
		MailTemplateIdentityVerifyEmail,
		MailTemplateIdentityEmailChangeVerifyNew,
		MailTemplateIdentityEmailChangeWarningOld,
		MailTemplateIdentityEmailVerifiedByAdmin,
		MailTemplateIdentityAccountDisabled,
		MailTemplateIdentityAccountEnabled,
		MailTemplateIdentitySessionsRevokedByAdmin,
		MailTemplateIdentityMFAEnabled,
		MailTemplateIdentityMFADisabled,
		MailTemplateIdentityMFARecoveryCodesRegenerated,
		MailTemplateIdentityPersonalAccessTokenCreated,
		MailTemplateIdentityPersonalAccessTokenEnabled,
		MailTemplateIdentityPersonalAccessTokenDisabled,
		MailTemplateIdentityPersonalAccessTokenRevoked,
		MailTemplateIdentityPasswordReset,
		MailTemplateIdentityPasswordChanged,
		MailTemplateAccessStudentClassInvitation,
		MailTemplateAccessTeacherAcademicUnitInvitation,
		MailTemplateAccessAcademicUnitRoleInvitation,
		MailTemplateAccessInstitutionRoleInvitation,
		MailTemplateAccessInvitationAccepted,
		MailTemplateAccessInvitationRevoked,
		MailTemplateAcademicClassEnrolled,
		MailTemplateAcademicClassEnrollmentEnded,
		MailTemplateAcademicClassTransferred,
		MailTemplateExamSittingScheduled,
		MailTemplateExamSittingRescheduled,
		MailTemplateExamSittingCancelled,
		MailTemplateExamSittingAssignmentRemoved,
		MailTemplateExamManagerAdded,
		MailTemplateExamManagerRemoved,
		MailTemplateExamOwnershipTransferredToYou,
		MailTemplateExamOwnershipTransferredFromYou,
		MailTemplateExamSubmissionReceived,
		MailTemplateExamSubmissionAutomaticallySealed,
		MailTemplateExamResultReleased:
		return true
	default:
		return false
	}
}
func (state MailDeliveryState) IsValid() bool { return validMailDeliveryState(state) }

const (
	MailTemplateSystemTest                          MailTemplateKey = "system.mail_test"
	MailTemplateIdentityVerifyEmail                 MailTemplateKey = "identity.verify_email"
	MailTemplateIdentityPasswordReset               MailTemplateKey = "identity.password_reset"
	MailTemplateIdentityPasswordChanged             MailTemplateKey = "identity.password_changed"
	MailTemplateIdentityEmailChangeWarningOld       MailTemplateKey = "identity.email_change_warning_old"
	MailTemplateIdentityEmailChangeVerifyNew        MailTemplateKey = "identity.email_change_verify_new"
	MailTemplateIdentityEmailVerifiedByAdmin        MailTemplateKey = "identity.email_verified_by_admin"
	MailTemplateIdentityAccountDisabled             MailTemplateKey = "identity.account_disabled"
	MailTemplateIdentityAccountEnabled              MailTemplateKey = "identity.account_enabled"
	MailTemplateIdentitySessionsRevokedByAdmin      MailTemplateKey = "identity.sessions_revoked_by_admin"
	MailTemplateIdentityMFAEnabled                  MailTemplateKey = "identity.mfa_enabled"
	MailTemplateIdentityMFADisabled                 MailTemplateKey = "identity.mfa_disabled"
	MailTemplateIdentityMFARecoveryCodesRegenerated MailTemplateKey = "identity.mfa_recovery_codes_regenerated"
	MailTemplateIdentityPersonalAccessTokenCreated  MailTemplateKey = "identity.personal_access_token_created"
	MailTemplateIdentityPersonalAccessTokenEnabled  MailTemplateKey = "identity.personal_access_token_enabled"
	MailTemplateIdentityPersonalAccessTokenDisabled MailTemplateKey = "identity.personal_access_token_disabled"
	MailTemplateIdentityPersonalAccessTokenRevoked  MailTemplateKey = "identity.personal_access_token_revoked"
	MailTemplateAccessStudentClassInvitation        MailTemplateKey = "access.student_class_invitation"
	MailTemplateAccessTeacherAcademicUnitInvitation MailTemplateKey = "access.teacher_academic_unit_invitation"
	MailTemplateAccessAcademicUnitRoleInvitation    MailTemplateKey = "access.academic_unit_role_invitation"
	MailTemplateAccessInstitutionRoleInvitation     MailTemplateKey = "access.institution_role_invitation"
	MailTemplateAccessInvitationAccepted            MailTemplateKey = "access.invitation_accepted"
	MailTemplateAccessInvitationRevoked             MailTemplateKey = "access.invitation_revoked"
	MailTemplateAcademicClassEnrolled               MailTemplateKey = "academic.class_enrolled"
	MailTemplateAcademicClassEnrollmentEnded        MailTemplateKey = "academic.class_enrollment_ended"
	MailTemplateAcademicClassTransferred            MailTemplateKey = "academic.class_transferred"
	MailTemplateExamSittingScheduled                MailTemplateKey = "exam.sitting_scheduled"
	MailTemplateExamSittingRescheduled              MailTemplateKey = "exam.sitting_rescheduled"
	MailTemplateExamSittingCancelled                MailTemplateKey = "exam.sitting_cancelled"
	MailTemplateExamSittingAssignmentRemoved        MailTemplateKey = "exam.sitting_assignment_removed"
	MailTemplateExamManagerAdded                    MailTemplateKey = "exam.manager_added"
	MailTemplateExamManagerRemoved                  MailTemplateKey = "exam.manager_removed"
	MailTemplateExamOwnershipTransferredToYou       MailTemplateKey = "exam.ownership_transferred_to_you"
	MailTemplateExamOwnershipTransferredFromYou     MailTemplateKey = "exam.ownership_transferred_from_you"
	MailTemplateExamSubmissionReceived              MailTemplateKey = "exam.submission_received"
	MailTemplateExamSubmissionAutomaticallySealed   MailTemplateKey = "exam.submission_automatically_sealed"
	MailTemplateExamResultReleased                  MailTemplateKey = "exam.result_released"

	MailOccurrenceOperatorTest           MailOccurrenceKind = "operator_test"
	MailOccurrenceAccountToken           MailOccurrenceKind = "account_token"
	MailOccurrenceSecurityNotice         MailOccurrenceKind = "security_notice"
	MailOccurrenceInvitation             MailOccurrenceKind = "invitation"
	MailOccurrenceAcademicAdministration MailOccurrenceKind = "academic_administration"
	MailOccurrenceSittingSchedule        MailOccurrenceKind = "sitting_schedule"
	MailOccurrenceExamManagement         MailOccurrenceKind = "exam_management"
	MailOccurrenceSubmissionReceipt      MailOccurrenceKind = "submission_receipt"
	MailOccurrenceResultRelease          MailOccurrenceKind = "result_release"

	MailDeliveryQueued     MailDeliveryState = "queued"
	MailDeliverySending    MailDeliveryState = "sending"
	MailDeliveryAccepted   MailDeliveryState = "accepted"
	MailDeliveryFailed     MailDeliveryState = "failed"
	MailDeliverySuppressed MailDeliveryState = "suppressed"
	MailDeliveryCanceled   MailDeliveryState = "canceled"

	MailDeliveryExpiredCode             = "mail.delivery.expired"
	MailDeliveryDisabledCode            = "mail.delivery.suppressed_disabled"
	MailDeliveryRecipientIneligibleCode = "mail.delivery.recipient_ineligible"
	MailDeliveryObsoleteCode            = "mail.delivery.obsolete"
	MailDeliveryCanceledCode            = "mail.delivery.canceled"
	MailDeliveryOperatorRetryCode       = "mail.operator.retry"
)

var (
	mailDigestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	mailMessageIDPattern  = regexp.MustCompile(`^<[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9.-]+>$`)
	mailPublicCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	mailMaskedPattern     = regexp.MustCompile(`^(?:\*{3}|[^*@\s]\*{1,3})@[^*@\s]+$`)
)

// MailOccurrence is the immutable durable identity of one logical notification.
type MailOccurrence struct {
	ID          MailOccurrenceID
	Kind        MailOccurrenceKind
	TemplateKey MailTemplateKey
	ActorUserID UserID
	CreatedAt   time.Time
}

func (o *MailOccurrence) Validate() error {
	if o == nil || !o.ID.IsValid() || !o.ActorUserID.IsValid() || o.CreatedAt.IsZero() ||
		!validMailOccurrenceMeaning(o.Kind, o.TemplateKey) {
		return errors.New("model: invalid mail occurrence")
	}
	return nil
}

func validMailOccurrenceMeaning(kind MailOccurrenceKind, key MailTemplateKey) bool {
	switch kind {
	case MailOccurrenceOperatorTest:
		return key == MailTemplateSystemTest
	case MailOccurrenceAccountToken:
		return key == MailTemplateIdentityVerifyEmail || key == MailTemplateIdentityPasswordReset || key == MailTemplateIdentityEmailChangeVerifyNew
	case MailOccurrenceSecurityNotice:
		return key == MailTemplateIdentityPasswordChanged || key == MailTemplateIdentityEmailChangeWarningOld || key == MailTemplateIdentityEmailVerifiedByAdmin ||
			key == MailTemplateIdentityAccountDisabled || key == MailTemplateIdentityAccountEnabled || key == MailTemplateIdentitySessionsRevokedByAdmin ||
			key == MailTemplateIdentityMFAEnabled || key == MailTemplateIdentityMFADisabled || key == MailTemplateIdentityMFARecoveryCodesRegenerated ||
			key == MailTemplateIdentityPersonalAccessTokenCreated || key == MailTemplateIdentityPersonalAccessTokenEnabled ||
			key == MailTemplateIdentityPersonalAccessTokenDisabled || key == MailTemplateIdentityPersonalAccessTokenRevoked
	case MailOccurrenceInvitation:
		return key == MailTemplateAccessStudentClassInvitation || key == MailTemplateAccessTeacherAcademicUnitInvitation ||
			key == MailTemplateAccessAcademicUnitRoleInvitation || key == MailTemplateAccessInstitutionRoleInvitation ||
			key == MailTemplateAccessInvitationAccepted || key == MailTemplateAccessInvitationRevoked
	case MailOccurrenceAcademicAdministration:
		return key == MailTemplateAcademicClassEnrolled || key == MailTemplateAcademicClassEnrollmentEnded ||
			key == MailTemplateAcademicClassTransferred
	case MailOccurrenceSittingSchedule:
		return key == MailTemplateExamSittingScheduled || key == MailTemplateExamSittingRescheduled ||
			key == MailTemplateExamSittingCancelled || key == MailTemplateExamSittingAssignmentRemoved
	case MailOccurrenceExamManagement:
		return key == MailTemplateExamManagerAdded || key == MailTemplateExamManagerRemoved ||
			key == MailTemplateExamOwnershipTransferredToYou || key == MailTemplateExamOwnershipTransferredFromYou
	case MailOccurrenceSubmissionReceipt:
		return key == MailTemplateExamSubmissionReceived || key == MailTemplateExamSubmissionAutomaticallySealed
	case MailOccurrenceResultRelease:
		return key == MailTemplateExamResultReleased
	default:
		return false
	}
}

// MailDelivery contains only bounded routing metadata plus an opaque encrypted
// frozen payload. Ciphertext must never be projected through logs, audits, or APIs.
type MailDelivery struct {
	ID                 MailDeliveryID
	OccurrenceID       MailOccurrenceID
	JobID              JobID
	TargetUserID       UserID
	TargetInvitationID InvitationID
	TemplateKey        MailTemplateKey
	TemplateDigest     string
	MaskedRecipient    string
	State              MailDeliveryState
	CreatedAt          time.Time
	UpdatedAt          time.Time
	MessageDate        time.Time
	Deadline           time.Time
	MessageID          string
	AttemptCount       int
	AcceptedAt         OptionalTime
	FailedAt           OptionalTime
	PublicFailureCode  string
	EncryptedPayload   json.RawMessage
	Revision           int64
}

// MailFanoutBundle is one encrypted, release-frozen render set shared by all
// recipients expanded from a bounded fan-out occurrence. The bundle identity
// equals its occurrence identity so it cannot be attached to another fact.
type MailFanoutBundle struct {
	ID               MailOccurrenceID
	EncryptedPayload json.RawMessage
	CreatedAt        time.Time
	Revision         int64
}

func (bundle *MailFanoutBundle) Validate() error {
	if bundle == nil || !bundle.ID.IsValid() || bundle.CreatedAt.IsZero() || bundle.Revision != 1 ||
		len(bundle.EncryptedPayload) == 0 || len(bundle.EncryptedPayload) > MailEncryptedFanoutBundleMaximumBytes ||
		!json.Valid(bundle.EncryptedPayload) {
		return errors.New("model: invalid mail fan-out bundle")
	}
	return nil
}

func (d *MailDelivery) Validate() error {
	if d == nil || !d.ID.IsValid() || !d.OccurrenceID.IsValid() || !d.JobID.IsValid() ||
		(d.TargetUserID.IsValid() == d.TargetInvitationID.IsValid()) || !d.TemplateKey.IsValid() ||
		!mailDigestPattern.MatchString(d.TemplateDigest) || d.MaskedRecipient == "" ||
		!validMaskedMailRecipient(d.MaskedRecipient) || len(d.MaskedRecipient) > MailMaskedRecipientMaximumBytes || !validMailDeliveryState(d.State) ||
		d.CreatedAt.IsZero() || d.UpdatedAt.Before(d.CreatedAt) || d.MessageDate.IsZero() ||
		!d.MessageDate.Equal(d.CreatedAt) ||
		!d.Deadline.After(d.CreatedAt) || !mailMessageIDPattern.MatchString(d.MessageID) ||
		len(d.MessageID) > MailMessageIDMaximumBytes || d.AttemptCount < 0 ||
		d.AttemptCount > MailMaximumAttempts || d.Revision <= 0 ||
		!validMailPublicCode(d.PublicFailureCode) {
		return errors.New("model: invalid mail delivery")
	}
	if len(d.EncryptedPayload) > MailEncryptedPayloadMaximumBytes ||
		(len(d.EncryptedPayload) > 0 && !json.Valid(d.EncryptedPayload)) {
		return errors.New("model: invalid encrypted mail payload")
	}
	terminalWithoutPayload := d.State == MailDeliveryAccepted || d.State == MailDeliverySuppressed || d.State == MailDeliveryCanceled
	if terminalWithoutPayload != (len(d.EncryptedPayload) == 0) {
		return errors.New("model: invalid mail payload lifecycle")
	}
	if (d.State == MailDeliveryAccepted) != d.AcceptedAt.Valid ||
		(d.State == MailDeliveryFailed) != d.FailedAt.Valid ||
		(d.AcceptedAt.Valid && d.AcceptedAt.Time.Before(d.CreatedAt)) ||
		(d.FailedAt.Valid && d.FailedAt.Time.Before(d.CreatedAt)) {
		return errors.New("model: invalid mail delivery lifecycle")
	}
	switch d.State {
	case MailDeliveryQueued:
		if (d.AttemptCount == 0) != (d.PublicFailureCode == "") {
			return errors.New("model: invalid queued mail delivery")
		}
	case MailDeliverySending:
		if d.AttemptCount == 0 || d.PublicFailureCode != "" {
			return errors.New("model: invalid sending mail delivery")
		}
	case MailDeliveryAccepted:
		if d.AttemptCount == 0 || d.PublicFailureCode != "" || !d.AcceptedAt.Time.Equal(d.UpdatedAt) {
			return errors.New("model: invalid accepted mail delivery")
		}
	case MailDeliveryFailed:
		if d.AttemptCount == 0 || d.PublicFailureCode == "" || !d.FailedAt.Time.Equal(d.UpdatedAt) {
			return errors.New("model: invalid failed mail delivery")
		}
	}
	return nil
}

func (d *MailDelivery) Start(at time.Time) (*MailDelivery, error) {
	if d == nil || (d.State != MailDeliveryQueued && d.State != MailDeliverySending) ||
		d.AttemptCount >= MailMaximumAttempts || TimeUTC(at).Before(d.UpdatedAt) || !TimeUTC(at).Before(d.Deadline) {
		return nil, errors.New("model: mail delivery cannot start")
	}
	result := d.Clone()
	result.State = MailDeliverySending
	result.UpdatedAt = TimeUTC(at)
	result.AttemptCount++
	result.PublicFailureCode = ""
	result.Revision++
	return result, result.Validate()
}

func (d *MailDelivery) Retry(publicCode string, at time.Time) (*MailDelivery, error) {
	if d == nil || d.State != MailDeliverySending || !validRequiredMailPublicCode(publicCode) ||
		TimeUTC(at).Before(d.UpdatedAt) || !TimeUTC(at).Before(d.Deadline) {
		return nil, errors.New("model: mail delivery cannot retry")
	}
	result := d.Clone()
	result.State = MailDeliveryQueued
	result.UpdatedAt = TimeUTC(at)
	result.PublicFailureCode = strings.TrimSpace(publicCode)
	result.Revision++
	return result, result.Validate()
}

func (d *MailDelivery) Fail(publicCode string, at time.Time) (*MailDelivery, error) {
	if d == nil || d.State != MailDeliverySending || !validRequiredMailPublicCode(publicCode) || TimeUTC(at).Before(d.UpdatedAt) {
		return nil, errors.New("model: mail delivery cannot fail")
	}
	result := d.Clone()
	result.State = MailDeliveryFailed
	result.UpdatedAt = TimeUTC(at)
	result.FailedAt = OptionalTimeFrom(at)
	result.PublicFailureCode = strings.TrimSpace(publicCode)
	result.Revision++
	return result, result.Validate()
}

func (d *MailDelivery) Accept(at time.Time) (*MailDelivery, error) {
	if d == nil || d.State != MailDeliverySending || TimeUTC(at).Before(d.UpdatedAt) {
		return nil, errors.New("model: mail delivery cannot be accepted")
	}
	result := d.Clone()
	result.State = MailDeliveryAccepted
	result.UpdatedAt = TimeUTC(at)
	result.AcceptedAt = OptionalTimeFrom(at)
	result.PublicFailureCode = ""
	result.EncryptedPayload = nil
	result.Revision++
	return result, result.Validate()
}

// Expire terminates unsent or retryable delivery work at its immutable
// deadline and destroys the recoverable payload in the same state change.
func (d *MailDelivery) Expire(at time.Time) (*MailDelivery, error) {
	at = TimeUTC(at)
	if d == nil || (d.State != MailDeliveryQueued && d.State != MailDeliverySending) ||
		at.Before(d.UpdatedAt) || at.Before(d.Deadline) {
		return nil, errors.New("model: mail delivery cannot expire")
	}
	result := d.Clone()
	result.State = MailDeliverySuppressed
	result.UpdatedAt = at
	result.PublicFailureCode = MailDeliveryExpiredCode
	result.EncryptedPayload = nil
	result.Revision++
	return result, result.Validate()
}

// Suppress terminates delivery work that must no longer be sent. It is valid
// for queued, currently claimed, or failed-but-still-recoverable work and
// destroys the encrypted payload in the same transition.
func (d *MailDelivery) Suppress(publicCode string, at time.Time) (*MailDelivery, error) {
	at = TimeUTC(at)
	if d == nil || (d.State != MailDeliveryQueued && d.State != MailDeliverySending && d.State != MailDeliveryFailed) ||
		(publicCode != MailDeliveryDisabledCode && publicCode != MailDeliveryRecipientIneligibleCode &&
			publicCode != MailDeliveryObsoleteCode && publicCode != MailDeliveryExpiredCode) ||
		at.Before(d.UpdatedAt) {
		return nil, errors.New("model: mail delivery cannot be suppressed")
	}
	result := d.Clone()
	result.State = MailDeliverySuppressed
	result.UpdatedAt = at
	result.AcceptedAt = OptionalTime{}
	result.FailedAt = OptionalTime{}
	result.PublicFailureCode = publicCode
	result.EncryptedPayload = nil
	result.Revision++
	return result, result.Validate()
}

// Cancel terminates queued delivery work and destroys the recoverable payload.
// It preserves the delivery identity and stable Message-ID for operator history.
func (d *MailDelivery) Cancel(at time.Time) (*MailDelivery, error) {
	at = TimeUTC(at)
	if d == nil || d.State != MailDeliveryQueued || at.Before(d.UpdatedAt) {
		return nil, errors.New("model: mail delivery cannot be canceled")
	}
	result := d.Clone()
	result.State = MailDeliveryCanceled
	result.UpdatedAt = at
	result.PublicFailureCode = MailDeliveryCanceledCode
	result.EncryptedPayload = nil
	result.Revision++
	return result, result.Validate()
}

// OperatorRetry requeues a failed delivery in place. The immutable recipient,
// payload and Message-ID remain frozen; the prior public failure stays in Job
// attempt history while the delivery records a closed operator-retry marker.
func (d *MailDelivery) OperatorRetry(at time.Time) (*MailDelivery, error) {
	at = TimeUTC(at)
	if d == nil || d.State != MailDeliveryFailed || d.AttemptCount >= MailMaximumAttempts ||
		at.Before(d.UpdatedAt) || !at.Before(d.Deadline) || len(d.EncryptedPayload) == 0 {
		return nil, errors.New("model: mail delivery cannot be retried by an operator")
	}
	result := d.Clone()
	result.State = MailDeliveryQueued
	result.UpdatedAt = at
	result.FailedAt = OptionalTime{}
	result.PublicFailureCode = MailDeliveryOperatorRetryCode
	result.Revision++
	return result, result.Validate()
}

func (d *MailDelivery) Clone() *MailDelivery {
	if d == nil {
		return nil
	}
	copy := *d
	copy.EncryptedPayload = append(json.RawMessage(nil), d.EncryptedPayload...)
	return &copy
}

// Auditable is deliberately payload-free and recipient-free.
func (d *MailDelivery) Auditable() map[string]any {
	if d == nil {
		return map[string]any{}
	}
	return map[string]any{"id": d.ID.String(), "occurrence_id": d.OccurrenceID.String(), "template_key": string(d.TemplateKey), "state": string(d.State)}
}

func validMailDeliveryState(state MailDeliveryState) bool {
	switch state {
	case MailDeliveryQueued, MailDeliverySending, MailDeliveryAccepted, MailDeliveryFailed, MailDeliverySuppressed, MailDeliveryCanceled:
		return true
	default:
		return false
	}
}

func validMailPublicCode(code string) bool {
	return code == "" || (len(code) <= 128 && code == strings.TrimSpace(code) && mailPublicCodePattern.MatchString(code))
}
func validRequiredMailPublicCode(code string) bool { return code != "" && validMailPublicCode(code) }

func validMaskedMailRecipient(value string) bool {
	return mailMaskedPattern.MatchString(value)
}

func IsMailDigest(value string) bool {
	if !mailDigestPattern.MatchString(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
