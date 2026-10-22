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
	"github.com/sudosylabs/proctor/server/store"
)

const (
	sittingMailReconciliationPageSize     = 200
	sittingMailReconciliationMaximumPages = 8
	sittingMailReconciliationInterval     = time.Minute
)

type sittingMailReconciliationStore interface {
	ListMailReconciliationDue(context.Context, store.ExamSittingMailReconciliationOptions) ([]store.ExamSittingMailReconciliationCandidate, error)
	ReconcileMail(context.Context, *store.ExamSittingMailReconciliation) (*store.ExamSittingMailFanoutSnapshot, error)
}

type sittingMailReconciliationPeriodicRunner struct {
	sittings sittingMailReconciliationStore
	mail     sitting.ScheduleMailPreparer
}

func (r sittingMailReconciliationPeriodicRunner) Run(ctx context.Context) error {
	if r.sittings == nil || r.mail == nil {
		return errors.New("Sitting mail reconciliation dependencies are unavailable")
	}
	options := store.ExamSittingMailReconciliationOptions{Limit: sittingMailReconciliationPageSize}
	for page := 0; page < sittingMailReconciliationMaximumPages; page++ {
		candidates, err := r.sittings.ListMailReconciliationDue(ctx, options)
		if err != nil {
			return err
		}
		for index := range candidates {
			candidate := candidates[index]
			if candidate.Sitting == nil {
				return errors.New("Sitting mail reconciliation returned an invalid candidate")
			}
			prepared, prepareErr := r.mail.Prepare(ctx, sitting.ScheduleMailRequest{
				ActorUserID: candidate.ActorUserID, ExamID: candidate.Sitting.ExamID,
				ExamRevisionID: candidate.Sitting.ExamRevisionID, SittingID: candidate.Sitting.ID,
				SittingRevision: candidate.Sitting.Revision, ClassID: candidate.Sitting.ClassID,
				PriorClassID: candidate.Sitting.ClassID, StartsAt: candidate.Sitting.ScheduledStartAt,
				EndsAt: candidate.Sitting.ScheduledEndAt, ChangeKind: store.ExamSittingMailReconciled,
			})
			if prepareErr != nil || prepared == nil {
				return errors.Join(errors.New("prepare Sitting mail reconciliation"), prepareErr)
			}
			if _, reconcileErr := r.sittings.ReconcileMail(ctx, &store.ExamSittingMailReconciliation{
				SittingID: candidate.Sitting.ID, ExpectedRevision: candidate.Sitting.Revision,
				ActorUserID: candidate.ActorUserID, Mail: prepared,
			}); reconcileErr != nil && !store.IsConflict(reconcileErr) && !store.IsNotFound(reconcileErr) {
				return reconcileErr
			}
		}
		if len(candidates) < options.Limit {
			return nil
		}
		last := candidates[len(candidates)-1].Sitting
		options.AfterScheduledStartAt, options.AfterSittingID = last.ScheduledStartAt, last.ID
	}
	return nil
}
