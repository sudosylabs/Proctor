// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	examreview "github.com/sudosylabs/proctor/server/app/exam/review"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const resultReleaseMailLifetime = 72 * time.Hour

type ResultReleaseDirectMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Details      ResultReleaseMailDetails
	ReleasedAt   time.Time
}

func (p *directMailPreparer) PrepareResultReleaseMail(
	request ResultReleaseDirectMailPreparation,
) (*preparedDirectMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		request.Details.ExamTitle == "" || request.Details.ReleasedAt.IsZero() || request.ReleasedAt.IsZero() ||
		!request.Details.ReleasedAt.Equal(request.ReleasedAt) {
		return nil, errors.New("result release mail input is invalid")
	}
	at := model.TimeUTC(request.ReleasedAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "",
			request.OccurrenceID, model.MailOccurrenceResultRelease, model.MailTemplateExamResultReleased, at,
			at.Add(resultReleaseMailLifetime), model.JobTypeMailDeliver, model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceResultRelease,
		model.MailTemplateExamResultReleased, "", at, at.Add(resultReleaseMailLifetime), model.JobTypeMailDeliver,
		request.Details)
}

type examResultReleaseMailPreparationAdapter struct {
	preparer  *directMailPreparer
	users     store.UserStore
	sittings  store.ExamSittingStore
	revisions store.ExamRevisionStore
}

func (adapter examResultReleaseMailPreparationAdapter) PrepareResultRelease(ctx context.Context,
	request examreview.ResultReleaseMailPreparation,
) (*examreview.PreparedResultReleaseMail, error) {
	if adapter.preparer == nil || adapter.users == nil || adapter.sittings == nil || adapter.revisions == nil ||
		!request.CandidateUserID.IsValid() || !request.ExamID.IsValid() || !request.SittingID.IsValid() ||
		!request.ReviewID.IsValid() || request.ReleasedAt.IsZero() {
		return nil, errors.New("result release mail dependencies or request are invalid")
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
		return nil, errors.New("result release Sitting projection is inconsistent")
	}
	revision, err := adapter.revisions.GetSummary(ctx, request.ExamID, snapshot.Sitting.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil || revision.ID != snapshot.Sitting.ExamRevisionID || revision.ExamID != request.ExamID ||
		revision.Title == "" {
		return nil, errors.New("result release Exam revision projection is inconsistent")
	}
	prepared, err := adapter.preparer.PrepareResultReleaseMail(ResultReleaseDirectMailPreparation{
		Recipient: recipient, OccurrenceID: model.MailOccurrenceID(request.ReviewID.String()),
		Details:    ResultReleaseMailDetails{ExamTitle: revision.Title, ReleasedAt: request.ReleasedAt},
		ReleasedAt: request.ReleasedAt,
	})
	if err != nil {
		return nil, err
	}
	return &examreview.PreparedResultReleaseMail{Notice: &store.PreparedMail{Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, Job: prepared.Job}, ExpectedRecipientRevision: recipient.Revision}, nil
}
