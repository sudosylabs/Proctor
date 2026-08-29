// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
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

func (effects examCollectionInvalidationEffects) CandidateActivityChangedForSitting(
	ctx context.Context,
	sittingID model.ExamSittingID,
) error {
	if effects.sittings == nil || effects.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	var after model.UserID
	var errs []error
	for {
		candidateIDs, err := effects.sittings.ListCandidateInvalidationTargetsBySitting(
			ctx, sittingID, after, examCollectionInvalidationPageSize,
		)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, candidateID := range candidateIDs {
			event, eventErr := apprealtime.NewCandidateExamActivityChangedEvent(candidateID)
			if eventErr != nil {
				errs = append(errs, eventErr)
				continue
			}
			errs = append(errs, effects.realtime.Publish(ctx, event))
		}
		if len(candidateIDs) < examCollectionInvalidationPageSize {
			return errors.Join(errs...)
		}
		after = candidateIDs[len(candidateIDs)-1]
	}
}

func (effects examCollectionInvalidationEffects) SittingBoardChanged(
	ctx context.Context,
	examID model.ExamID,
	sittingID model.ExamSittingID,
) error {
	if effects.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	event, err := apprealtime.NewManagerSittingBoardChangedEvent(examID, sittingID)
	if err != nil {
		return err
	}
	return effects.realtime.Publish(ctx, event)
}

func (effects examCollectionInvalidationEffects) ExamArchived(
	ctx context.Context,
	examID model.ExamID,
) error {
	if effects.sittings == nil || effects.realtime == nil {
		return errors.New("Exam collection invalidation dependencies are invalid")
	}
	var after model.ExamSittingID
	var errs []error
	for {
		targets, err := effects.sittings.ListInvalidationTargetsByExam(
			ctx, examID, after, examCollectionInvalidationPageSize,
		)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, target := range targets {
			errs = append(
				errs,
				effects.SittingBoardChanged(ctx, target.ExamID, target.SittingID),
				effects.CandidateActivityChangedForSitting(ctx, target.SittingID),
			)
		}
		if len(targets) < examCollectionInvalidationPageSize {
			return errors.Join(errs...)
		}
		after = targets[len(targets)-1].SittingID
	}
}
