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

	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const examCollectionInvalidationPageSize = 200

type examCollectionInvalidationStore interface {
	ListInvalidationTargetsByExam(context.Context, model.ExamID, model.ExamSittingID, int) ([]store.ExamSittingInvalidationTarget, error)
	ListCandidateInvalidationTargetsBySitting(context.Context, model.ExamSittingID, model.UserID, int) ([]model.UserID, error)
}

type examCollectionInvalidationEffects struct {
	sittings examCollectionInvalidationStore
	realtime *realtimeService
}

func (e examCollectionInvalidationEffects) CandidateActivityChangedForSitting(
	ctx context.Context,
	sittingID model.ExamSittingID,
) error {
	if e.sittings == nil || e.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	var after model.UserID
	var errs []error
	for {
		candidateIDs, err := e.sittings.ListCandidateInvalidationTargetsBySitting(
			ctx, sittingID, after, examCollectionInvalidationPageSize,
		)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, candidateID := range candidateIDs {
			event, eventErr := realtime.NewCandidateExamActivityChangedEvent(candidateID)
			if eventErr != nil {
				errs = append(errs, eventErr)
				continue
			}
			errs = append(errs, e.realtime.PublishRealtimeEvent(ctx, event))
		}
		if len(candidateIDs) < examCollectionInvalidationPageSize {
			return errors.Join(errs...)
		}
		after = candidateIDs[len(candidateIDs)-1]
	}
}

func (e examCollectionInvalidationEffects) SittingBoardChanged(
	ctx context.Context,
	examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	if e.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	event, err := realtime.NewManagerSittingBoardChangedEvent(examID, sittingID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

func (e examCollectionInvalidationEffects) ExamArchived(
	ctx context.Context,
	examID model.ExamID,
) error {
	if e.sittings == nil || e.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	var after model.ExamSittingID
	var errs []error
	for {
		targets, err := e.sittings.ListInvalidationTargetsByExam(
			ctx, examID, after, examCollectionInvalidationPageSize,
		)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, target := range targets {
			errs = append(
				errs,
				e.SittingBoardChanged(ctx, target.ExamID, target.SittingID),
				e.CandidateActivityChangedForSitting(ctx, target.SittingID),
			)
		}
		if len(targets) < examCollectionInvalidationPageSize {
			return errors.Join(errs...)
		}
		after = targets[len(targets)-1].SittingID
	}
}
