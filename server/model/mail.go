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
	MailRenderedPayloadMaximumBytes  = 1 << 20
	MailEncryptedPayloadMaximumBytes = 2 << 20
	MailMaskedRecipientMaximumBytes  = 254
	MailMessageIDMaximumBytes        = 900
	MailMaximumAttempts              = 8
)

type MailTemplateKey string
type MailOccurrenceKind string
type MailDeliveryState string

const (
	MailTemplateSystemTest     MailTemplateKey    = "system.mail_test"
	MailOccurrenceOperatorTest MailOccurrenceKind = "operator_test"

	MailDeliveryQueued     MailDeliveryState = "queued"
	MailDeliverySending    MailDeliveryState = "sending"
	MailDeliveryAccepted   MailDeliveryState = "accepted"
	MailDeliveryFailed     MailDeliveryState = "failed"
	MailDeliverySuppressed MailDeliveryState = "suppressed"
	MailDeliveryCanceled   MailDeliveryState = "canceled"

	MailDeliveryExpiredCode = "mail.delivery.expired"
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
	if o == nil || !o.ID.IsValid() || o.Kind != MailOccurrenceOperatorTest ||
		o.TemplateKey != MailTemplateSystemTest || !o.ActorUserID.IsValid() || o.CreatedAt.IsZero() {
		return errors.New("model: invalid mail occurrence")
	}
	return nil
}

// MailDelivery contains only bounded routing metadata plus an opaque encrypted
// frozen payload. Ciphertext must never be projected through logs, audits, or APIs.
type MailDelivery struct {
	ID                MailDeliveryID
	OccurrenceID      MailOccurrenceID
	JobID             JobID
	TargetUserID      UserID
	TemplateKey       MailTemplateKey
	TemplateDigest    string
	MaskedRecipient   string
	State             MailDeliveryState
	CreatedAt         time.Time
	UpdatedAt         time.Time
	MessageDate       time.Time
	Deadline          time.Time
	MessageID         string
	AttemptCount      int
	AcceptedAt        OptionalTime
	FailedAt          OptionalTime
	PublicFailureCode string
	EncryptedPayload  json.RawMessage
	Revision          int64
}

func (d *MailDelivery) Validate() error {
	if d == nil || !d.ID.IsValid() || !d.OccurrenceID.IsValid() || !d.JobID.IsValid() ||
		!d.TargetUserID.IsValid() || d.TemplateKey != MailTemplateSystemTest ||
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
