// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const classTransitionMailLifetime = 72 * time.Hour

type ClassTransitionMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      ClassTransitionMailDetails
	ActionAt     time.Time
}

type classTransitionMailPreparer interface {
	PrepareClassTransition(ClassTransitionMailPreparation) (*preparedDirectMail, error)
}

func (p *directMailPreparer) PrepareClassTransition(request ClassTransitionMailPreparation) (*preparedDirectMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil ||
		!request.OccurrenceID.IsValid() || !validClassTransitionMailMeaning(request.TemplateKey, request.Details) ||
		request.ActionAt.IsZero() {
		return nil, errors.New("Class transition mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	details := request.Details
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailOccurrenceAcademicAdministration, request.TemplateKey, at, at.Add(classTransitionMailLifetime),
			model.JobTypeMailDeliver, model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		model.MailOccurrenceAcademicAdministration, request.TemplateKey, "", at,
		at.Add(classTransitionMailLifetime), model.JobTypeMailDeliver, nil, nil, &details)
}

func validClassTransitionMailMeaning(key model.MailTemplateKey, details ClassTransitionMailDetails) bool {
	if details.ClassDisplayName == "" || details.StartsAt.IsZero() ||
		(!details.EndsAt.IsZero() && !details.StartsAt.Before(details.EndsAt)) {
		return false
	}
	switch key {
	case model.MailTemplateAcademicClassEnrolled:
		return details.PreviousClassDisplayName == ""
	case model.MailTemplateAcademicClassEnrollmentEnded:
		return details.PreviousClassDisplayName == "" && !details.EndsAt.IsZero()
	case model.MailTemplateAcademicClassTransferred:
		return details.PreviousClassDisplayName != ""
	default:
		return false
	}
}
