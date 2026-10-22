// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSittingMailReconciliationPeriodicRunnerPreparesOneBoundedFanout(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)
	persistence := &sittingMailReconciliationStoreFake{candidates: []store.ExamSittingMailReconciliationCandidate{{
		Sitting: sitting, ActorUserID: model.NewUserID(),
	}}}
	mail := &sittingMailReconciliationPreparerFake{prepared: &store.ExamSittingMailFanout{}}
	err = (sittingMailReconciliationPeriodicRunner{sittings: persistence, mail: mail}).Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, persistence.reconciled, "reconciliation=%#v request=%#v", persistence.reconciled, mail.request)
	require.Equal(t, sitting.ID, persistence.reconciled.SittingID, "reconciliation=%#v request=%#v", persistence.reconciled, mail.request)
	require.Equal(t, store.ExamSittingMailReconciled, mail.request.ChangeKind, "reconciliation=%#v request=%#v", persistence.reconciled, mail.request)
	require.Equal(t, sitting.Revision, mail.request.SittingRevision, "reconciliation=%#v request=%#v", persistence.reconciled, mail.request)
}

type sittingMailReconciliationStoreFake struct {
	candidates []store.ExamSittingMailReconciliationCandidate
	reconciled *store.ExamSittingMailReconciliation
}

func (f *sittingMailReconciliationStoreFake) ListMailReconciliationDue(context.Context,
	store.ExamSittingMailReconciliationOptions,
) ([]store.ExamSittingMailReconciliationCandidate, error) {
	result := f.candidates
	f.candidates = nil
	return result, nil
}

func (f *sittingMailReconciliationStoreFake) ReconcileMail(_ context.Context,
	input *store.ExamSittingMailReconciliation,
) (*store.ExamSittingMailFanoutSnapshot, error) {
	f.reconciled = input
	return &store.ExamSittingMailFanoutSnapshot{}, nil
}

type sittingMailReconciliationPreparerFake struct {
	request  sitting.ScheduleMailRequest
	prepared *store.ExamSittingMailFanout
}

func (f *sittingMailReconciliationPreparerFake) Prepare(_ context.Context,
	request sitting.ScheduleMailRequest,
) (*store.ExamSittingMailFanout, error) {
	f.request = request
	return f.prepared, nil
}
