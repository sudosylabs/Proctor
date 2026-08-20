// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const examManagerMailLifetime = 72 * time.Hour

func (p *directMailPreparer) PrepareManagerMail(request examengine.ManagerMailPreparation) (*store.ExamManagerMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validExamManagerMailMeaning(request.TemplateKey, request.Relationship) || request.ActionAt.IsZero() {
		return nil, errors.New("Exam Manager mail input is invalid")
	}
	details := ExamManagerMailDetails{Title: request.ExamTitle, Relationship: string(request.Relationship), ActionAt: request.ActionAt}
	prepared, err := p.prepareRecipient(
		request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
		model.MailOccurrenceExamManagement, request.TemplateKey, "", request.ActionAt,
		request.ActionAt.Add(examManagerMailLifetime), model.JobTypeMailDeliver, nil, &details, nil,
	)
	if err != nil {
		return nil, err
	}
	return &store.ExamManagerMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}, nil
}

func validExamManagerMailMeaning(key model.MailTemplateKey, relationship examengine.ManagerMailRelationship) bool {
	switch key {
	case model.MailTemplateExamManagerAdded:
		return relationship == examengine.ManagerMailRelationshipManager
	case model.MailTemplateExamManagerRemoved:
		return relationship == examengine.ManagerMailRelationshipNoLongerManager
	case model.MailTemplateExamOwnershipTransferredToYou:
		return relationship == examengine.ManagerMailRelationshipOwner
	case model.MailTemplateExamOwnershipTransferredFromYou:
		return relationship == examengine.ManagerMailRelationshipManager
	default:
		return false
	}
}
