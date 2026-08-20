// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type personalAccessTokenSecurityNoticePreparation struct {
	Recipient          *model.User
	TemplateKey        model.MailTemplateKey
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

type personalAccessTokenSecurityNoticeMailPreparer interface {
	PreparePersonalAccessTokenSecurityNotice(personalAccessTokenSecurityNoticePreparation) (*preparedDirectMail, error)
}

func (p *directMailPreparer) PreparePersonalAccessTokenSecurityNotice(
	request personalAccessTokenSecurityNoticePreparation,
) (*preparedDirectMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.Recipient.IsActive() ||
		!isPersonalAccessTokenSecurityNoticeTemplate(request.TemplateKey) {
		return nil, errors.New("personal access token mail template is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	details := PersonalAccessTokenMailDetails{
		Description: request.Description, ExpiresAt: request.ExpiresAt,
		ActionAt: at, ActionCount: request.ActionCount,
		AcademicUnitScoped: request.AcademicUnitScoped,
	}
	return p.prepareRecipient(
		request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, request.TemplateKey, "", at,
		at.Add(securityNoticeDeliveryLifetime), model.JobTypeMailDeliver, &details,
	)
}

func isPersonalAccessTokenSecurityNoticeTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateIdentityPersonalAccessTokenCreated,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked:
		return true
	default:
		return false
	}
}
