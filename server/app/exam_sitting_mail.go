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

	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type sittingScheduleMailPreparationAdapter struct {
	preparer  *mail.SittingComposer
	revisions store.ExamRevisionStore
	classes   store.ClassStore
}

func (a sittingScheduleMailPreparationAdapter) Prepare(ctx context.Context,
	request sitting.ScheduleMailRequest,
) (*store.ExamSittingMailFanout, error) {
	if a.preparer == nil || a.revisions == nil || a.classes == nil {
		return nil, errors.New("sitting schedule mail preparation is unavailable")
	}
	revision, err := a.revisions.GetSummary(ctx, request.ExamID, request.ExamRevisionID)
	if err != nil {
		return nil, err
	}
	class, err := a.classes.Get(ctx, request.ClassID)
	if err != nil {
		return nil, err
	}
	priorClassName := class.DisplayName
	if request.PriorClassID.IsValid() && request.PriorClassID != request.ClassID {
		priorClass, priorErr := a.classes.Get(ctx, request.PriorClassID)
		if priorErr != nil {
			return nil, priorErr
		}
		priorClassName = priorClass.DisplayName
	}
	sitting := &model.ExamSitting{ID: request.SittingID, ExamID: request.ExamID, ExamRevisionID: request.ExamRevisionID,
		ClassID: request.ClassID, ScheduledStartAt: request.StartsAt, ScheduledEndAt: request.EndsAt,
		State: model.ExamSittingScheduled, Revision: request.SittingRevision}
	if request.ChangeKind == store.ExamSittingMailCanceled {
		sitting.State = model.ExamSittingCanceled
		sitting.ReasonCode = model.ExamSittingReasonManagerCanceled
	}
	return a.preparer.Prepare(request.ActorUserID, sitting, request.ChangeKind, mail.SittingScheduleDetails{
		ExamTitle: revision.Title, ClassDisplayName: class.DisplayName, PriorClassDisplayName: priorClassName,
		StartsAt: request.StartsAt, EndsAt: request.EndsAt,
	})
}

func newSittingMailPreparer(renderer mail.SittingRenderer, sender mail.Sender, sealer *secretseal.Sealer,
	now func() time.Time,
) (*mail.SittingComposer, error) {
	return mail.NewSittingComposer(mail.SittingComposerDependencies{Renderer: renderer, Sender: sender, Sealer: sealer, Now: now})
}
