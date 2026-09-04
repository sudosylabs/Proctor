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

	examreview "github.com/sudosylabs/proctor/server/app/exam/review"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examResultReleaseMailPreparationAdapter struct {
	preparer  *appmail.Composer
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
	prepared, err := adapter.preparer.PrepareResultReleaseMail(appmail.ResultReleasePreparation{
		Recipient: recipient, OccurrenceID: model.MailOccurrenceID(request.ReviewID.String()),
		Details:    appmail.ResultReleaseDetails{ExamTitle: revision.Title, ReleasedAt: request.ReleasedAt},
		ReleasedAt: request.ReleasedAt,
	})
	if err != nil {
		return nil, err
	}
	return &examreview.PreparedResultReleaseMail{Notice: &store.PreparedMail{Occurrence: prepared.Occurrence,
		Delivery: prepared.Delivery, Job: prepared.Job}, ExpectedRecipientRevision: recipient.Revision}, nil
}
