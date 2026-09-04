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
	"time"

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type sittingScheduleMailPreparationAdapter struct {
	preparer  *appmail.SittingComposer
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
	return adapter.preparer.Prepare(request.ActorUserID, sitting, request.ChangeKind, appmail.SittingScheduleDetails{
		ExamTitle: revision.Title, ClassDisplayName: class.DisplayName, PriorClassDisplayName: priorClassName,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt,
	})
}

func newSittingMailPreparer(renderer appmail.SittingRenderer, sender appmail.Sender, sealer *secretseal.Sealer,
	now func() time.Time,
) (*appmail.SittingComposer, error) {
	return appmail.NewSittingComposer(renderer, sender, sealer, now)
}
