// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type SittingScheduleMailDetails = appmail.SittingScheduleDetails
type SittingMailTemplateRenderer = appmail.SittingRenderer
type sittingMailPreparer = appmail.SittingComposer

type sittingScheduleMailPreparationAdapter struct {
	preparer  *sittingMailPreparer
	revisions store.ExamRevisionStore
	classes   store.ClassStore
}

func (adapter sittingScheduleMailPreparationAdapter) Prepare(ctx context.Context,
	request examsitting.ScheduleMailRequest,
) (*store.ExamSittingMailFanout, error) {
	if adapter.preparer == nil || adapter.revisions == nil || adapter.classes == nil {
		return nil, errors.New("sitting schedule mail preparation is unavailable")
	}
	revision, err := adapter.revisions.GetSummary(ctx, request.ExamID, request.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	class, err := adapter.classes.Get(ctx, request.ClassID.String())
	if err != nil {
		return nil, err
	}
	priorClassName := class.DisplayName
	if request.PriorClassID.IsValid() && request.PriorClassID != request.ClassID {
		priorClass, priorErr := adapter.classes.Get(ctx, request.PriorClassID.String())
		if priorErr != nil {
			return nil, priorErr
		}
		priorClassName = priorClass.DisplayName
	}
	sitting := &model.ExamSitting{ID: request.SittingID, ExamID: request.ExamID, ExamRevisionID: request.ExamRevisionID,
		ClassID: request.ClassID, ScheduledStartAt: request.StartsAt, ScheduledEndAt: request.EndsAt,
		State: model.ExamSittingScheduled, Revision: request.SittingRevision}
	if request.ChangeKind == store.ExamSittingMailCancelled {
		sitting.State = model.ExamSittingCanceled
		sitting.ReasonCode = model.ExamSittingReasonManagerCanceled
	}
	return adapter.preparer.Prepare(request.ActorUserID, sitting, request.ChangeKind, SittingScheduleMailDetails{
		ExamTitle: revision.Title, ClassDisplayName: class.DisplayName, PriorClassDisplayName: priorClassName,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt,
	})
}

func newSittingMailPreparer(renderer SittingMailTemplateRenderer, sender MailDeliverySender, sealer *secretseal.Sealer,
	now func() time.Time,
) (*sittingMailPreparer, error) {
	return appmail.NewSittingComposer(renderer, sender, sealer, now)
}
