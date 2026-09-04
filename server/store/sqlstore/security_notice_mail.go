// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const securityNoticeLifetime = 24 * time.Hour

func validateSecurityNoticeMail(userID model.UserID, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, key model.MailTemplateKey, at int64) (string, error) {
	when := model.TimeFromMillis(at)
	if !userID.IsValid() || occurrence == nil || delivery == nil || job == nil ||
		occurrence.Kind != model.MailOccurrenceSecurityNotice || occurrence.TemplateKey != key || occurrence.ActorUserID != userID ||
		delivery.TargetUserID != userID || delivery.TemplateKey != key || job.Type != model.JobTypeMailDeliver ||
		!occurrence.CreatedAt.Equal(when) || delivery.Deadline.Sub(delivery.CreatedAt) != securityNoticeLifetime {
		return "", store.NewErrInvalidInput("security_notice", "mail", nil)
	}
	if err := validateRecoveryMail(occurrence, delivery, job); err != nil {
		return "", err
	}
	payloadKeyID, err := mailPayloadKeyID(delivery.EncryptedPayload)
	if err != nil {
		return "", store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
	}
	return payloadKeyID, nil
}

func insertSecurityNoticeMail(ctx context.Context, tx *sqlxTxWrapper, occurrence *model.MailOccurrence, delivery *model.MailDelivery, job *model.Job, payloadKeyID string) error {
	return insertRecoveryMail(ctx, tx, occurrence, delivery, job, payloadKeyID)
}
