// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examSubmissionMailPreparationAdapter struct {
	preparer  *appmail.Composer
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
	switch request.Provenance {
	case model.ExamSubmissionCandidateSubmitted:
	case model.ExamSubmissionManagerEndedAttempt:
		key = model.MailTemplateExamSubmissionManagerEnded
	case model.ExamSubmissionSittingClosed:
		key = model.MailTemplateExamSubmissionAutomaticallySealed
	default:
		return nil, errors.New("Submission receipt provenance is invalid")
	}
	prepared, err := adapter.preparer.PrepareSubmissionReceiptMail(appmail.SubmissionReceiptPreparation{
		Recipient: recipient, OccurrenceID: model.MailOccurrenceID(request.SubmissionID.String()), TemplateKey: key,
		Details: appmail.SubmissionReceiptDetails{ExamTitle: revision.Title, SittingID: request.SittingID,
			SubmissionID: request.SubmissionID, SealedAt: request.SealedAt}, ActionAt: request.SealedAt,
	})
	if err != nil {
		return nil, err
	}
	return &examattempt.PreparedSubmissionMail{Notice: &store.PreparedMail{Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, Job: prepared.Job}, ExpectedRecipientRevision: recipient.Revision}, nil
}
