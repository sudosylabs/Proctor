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

	"github.com/sudosylabs/proctor/server/app/exam/review"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examResultReleaseMailPreparationAdapter struct {
	preparer  *mail.Composer
	users     store.UserStore
	sittings  store.ExamSittingStore
	revisions store.ExamRevisionStore
}

func (a examResultReleaseMailPreparationAdapter) PrepareResultRelease(ctx context.Context,
	request review.ResultReleaseMailPreparation,
) (*review.PreparedResultReleaseMail, error) {
	if a.preparer == nil || a.users == nil || a.sittings == nil || a.revisions == nil ||
		!request.CandidateUserID.IsValid() || !request.ExamID.IsValid() || !request.SittingID.IsValid() ||
		!request.ReviewID.IsValid() || request.ReleasedAt.IsZero() {
		return nil, errors.New("result release mail dependencies or request are invalid")
	}
	recipient, err := a.users.Get(ctx, request.CandidateUserID)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.sittings.Get(ctx, request.ExamID, request.SittingID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.Validate() != nil ||
		snapshot.Sitting.ID != request.SittingID || snapshot.Sitting.ExamID != request.ExamID {
		return nil, errors.New("result release Sitting projection is inconsistent")
	}
	revision, err := a.revisions.GetSummary(ctx, request.ExamID, snapshot.Sitting.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil || revision.ID != snapshot.Sitting.ExamRevisionID || revision.ExamID != request.ExamID ||
		revision.Title == "" {
		return nil, errors.New("result release Exam revision projection is inconsistent")
	}
	occurrence, err := model.ResultReleaseOccurrenceID(request.ReviewID, request.ExpectedReviewRevision)
	if err != nil {
		return nil, err
	}
	prepared, err := a.preparer.PrepareResultReleaseMail(mail.ResultReleasePreparation{
		Recipient: recipient, OccurrenceID: occurrence,
		Details:    mail.ResultReleaseDetails{ExamTitle: revision.Title, ReleasedAt: request.ReleasedAt},
		ReleasedAt: request.ReleasedAt,
	})
	if err != nil {
		return nil, err
	}
	return &review.PreparedResultReleaseMail{Notice: &store.PreparedMail{Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, Job: prepared.Job}, ExpectedRecipientRevision: recipient.Revision}, nil
}
