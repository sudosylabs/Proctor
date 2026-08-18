// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const securityNoticeDeliveryLifetime = 24 * time.Hour

// securityNoticePreparation is the complete application-owned request for an
// ordinary historical security notice. Consumers cannot accidentally select a
// credential Job class, occurrence kind, identifier, or arbitrary deadline.
type securityNoticePreparation struct {
	Recipient   *model.User
	TemplateKey model.MailTemplateKey
	At          time.Time
}

// securityNoticeMailPreparer is the exact capability account and Session
// administration consume. Mail enablement is reflected in the prepared
// terminal lifecycle; these mutations never branch on it.
type securityNoticeMailPreparer interface {
	PrepareSecurityNotice(securityNoticePreparation) (*preparedDirectMail, error)
}

func (p *directMailPreparer) PrepareSecurityNotice(request securityNoticePreparation) (*preparedDirectMail, error) {
	return p.PrepareDirect(DirectMailPreparation{
		Recipient: request.Recipient, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceSecurityNotice, TemplateKey: request.TemplateKey,
		At: request.At, Deadline: request.At.Add(securityNoticeDeliveryLifetime),
		JobType: model.JobTypeMailDeliver,
	})
}
