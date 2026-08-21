// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const examSubmissionReceiptMailLifetime = 72 * time.Hour

func validateExamSubmissionReceiptMail(prepared *store.PreparedMail, recipient *model.User,
	submissionID model.SubmissionID, at int64, key model.MailTemplateKey,
) (string, error) {
	when := model.TimeFromMillis(at)
	if prepared == nil || prepared.Occurrence == nil || prepared.Delivery == nil || prepared.Job == nil ||
		recipient == nil || recipient.Validate() != nil || !submissionID.IsValid() ||
		prepared.Occurrence.ID != model.MailOccurrenceID(submissionID.String()) ||
		prepared.Occurrence.Kind != model.MailOccurrenceSubmissionReceipt || prepared.Occurrence.TemplateKey != key ||
		prepared.Occurrence.ActorUserID != recipient.ID || prepared.Delivery.TargetUserID != recipient.ID ||
		prepared.Delivery.TargetInvitationID.IsValid() || prepared.Delivery.TemplateKey != key ||
		prepared.Job.Type != model.JobTypeMailDeliver || !prepared.Occurrence.CreatedAt.Equal(when) ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != examSubmissionReceiptMailLifetime {
		return "", store.NewErrInvalidInput("exam_submission", "receipt_notice", nil)
	}
	ineligible := prepared.Delivery.State == model.MailDeliverySuppressed &&
		prepared.Delivery.PublicFailureCode == model.MailDeliveryRecipientIneligibleCode
	if recipient.IsActive() == ineligible {
		return "", store.NewErrConflict("exam_submission", "receipt_recipient_changed", nil)
	}
	if err := validateRecoveryMail(prepared.Occurrence, prepared.Delivery, prepared.Job); err != nil {
		return "", err
	}
	payloadKeyID, err := mailPayloadKeyID(prepared.Delivery.EncryptedPayload)
	if err != nil {
		return "", store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return payloadKeyID, nil
}

func insertExamSubmissionReceiptMail(ctx context.Context, tx *sqlxTxWrapper, prepared *store.PreparedMail,
	payloadKeyID string,
) error {
	return insertRecoveryMail(ctx, tx, prepared.Occurrence, prepared.Delivery, prepared.Job, payloadKeyID)
}
