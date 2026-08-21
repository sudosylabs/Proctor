// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const resultReleaseDeliveryLifetime = 72 * time.Hour

func validateResultReleaseMail(prepared *store.PreparedMail, recipient *model.User,
	reviewID model.SubmissionReviewID, at time.Time,
) (string, error) {
	if prepared == nil || prepared.Occurrence == nil || prepared.Delivery == nil || prepared.Job == nil ||
		recipient == nil || recipient.Validate() != nil || !reviewID.IsValid() || at.IsZero() ||
		prepared.Occurrence.ID != model.MailOccurrenceID(reviewID.String()) ||
		prepared.Occurrence.Kind != model.MailOccurrenceResultRelease ||
		prepared.Occurrence.TemplateKey != model.MailTemplateExamResultReleased ||
		prepared.Occurrence.ActorUserID != recipient.ID || prepared.Delivery.TargetUserID != recipient.ID ||
		prepared.Delivery.TargetInvitationID.IsValid() ||
		prepared.Delivery.TemplateKey != model.MailTemplateExamResultReleased ||
		prepared.Job.Type != model.JobTypeMailDeliver || !prepared.Occurrence.CreatedAt.Equal(at) ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != resultReleaseDeliveryLifetime {
		return "", store.NewErrInvalidInput("submission_review", "result_release_notice", nil)
	}
	ineligible := prepared.Delivery.State == model.MailDeliverySuppressed &&
		prepared.Delivery.PublicFailureCode == model.MailDeliveryRecipientIneligibleCode
	if recipient.IsActive() == ineligible {
		return "", store.NewErrConflict("submission_review", "result_release_recipient_changed", nil)
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

func insertResultReleaseMail(ctx context.Context, tx *sqlxTxWrapper, prepared *store.PreparedMail,
	payloadKeyID string,
) error {
	return insertRecoveryMail(ctx, tx, prepared.Occurrence, prepared.Delivery, prepared.Job, payloadKeyID)
}
