// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const submissionReceiptMailLifetime = 72 * time.Hour

type SubmissionReceiptMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      SubmissionReceiptMailDetails
	ActionAt     time.Time
}

func (p *directMailPreparer) PrepareSubmissionReceiptMail(request SubmissionReceiptMailPreparation) (*preparedDirectMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validSubmissionReceiptMailMeaning(request.TemplateKey, request.Details) || request.ActionAt.IsZero() {
		return nil, errors.New("Submission receipt mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "",
			request.OccurrenceID, model.MailOccurrenceSubmissionReceipt, request.TemplateKey, at,
			at.Add(submissionReceiptMailLifetime), model.JobTypeMailDeliver, model.MailDeliveryRecipientIneligibleCode)
	}
	details := request.Details
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceSubmissionReceipt,
		request.TemplateKey, "", at, at.Add(submissionReceiptMailLifetime), model.JobTypeMailDeliver,
		details)
}

func validSubmissionReceiptMailMeaning(key model.MailTemplateKey, details SubmissionReceiptMailDetails) bool {
	if details.ExamTitle == "" || !details.SittingID.IsValid() || !details.SubmissionID.IsValid() || details.SealedAt.IsZero() {
		return false
	}
	return key == model.MailTemplateExamSubmissionReceived || key == model.MailTemplateExamSubmissionAutomaticallySealed
}

type examSubmissionMailPreparationAdapter struct {
	preparer  *directMailPreparer
	users     store.UserStore
	sittings  store.ExamSittingStore
	revisions store.ExamRevisionStore
}

func (adapter examSubmissionMailPreparationAdapter) PrepareSubmissionReceipt(ctx context.Context,
	request examattempt.SubmissionMailPreparation,
) (*examattempt.PreparedSubmissionMail, error) {
	if adapter.preparer == nil || adapter.users == nil || adapter.sittings == nil || adapter.revisions == nil ||
		!request.CandidateUserID.IsValid() || !request.ExamID.IsValid() || !request.SittingID.IsValid() ||
		!request.SubmissionID.IsValid() || request.SealedAt.IsZero() {
		return nil, errors.New("Submission receipt dependencies or request are invalid")
	}
	recipient, err := adapter.users.Get(ctx, request.CandidateUserID.String())
	if err != nil {
		return nil, err
	}
	snapshot, err := adapter.sittings.Get(ctx, request.ExamID, request.SittingID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.Validate() != nil ||
		snapshot.Sitting.ID != request.SittingID || snapshot.Sitting.ExamID != request.ExamID {
		return nil, errors.New("Submission receipt Sitting projection is inconsistent")
	}
	revision, err := adapter.revisions.GetSummary(ctx, request.ExamID, snapshot.Sitting.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil || revision.ID != snapshot.Sitting.ExamRevisionID || revision.ExamID != request.ExamID || revision.Title == "" {
		return nil, errors.New("Submission receipt Exam revision projection is inconsistent")
	}
	key := model.MailTemplateExamSubmissionReceived
	if request.Automatic {
		key = model.MailTemplateExamSubmissionAutomaticallySealed
	}
	prepared, err := adapter.preparer.PrepareSubmissionReceiptMail(SubmissionReceiptMailPreparation{
		Recipient: recipient, OccurrenceID: model.MailOccurrenceID(request.SubmissionID.String()), TemplateKey: key,
		Details: SubmissionReceiptMailDetails{ExamTitle: revision.Title, SittingID: request.SittingID,
			SubmissionID: request.SubmissionID, SealedAt: request.SealedAt}, ActionAt: request.SealedAt,
	})
	if err != nil {
		return nil, err
	}
	return &examattempt.PreparedSubmissionMail{Notice: &store.PreparedMail{Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, Job: prepared.Job}, ExpectedRecipientRevision: recipient.Revision}, nil
}
