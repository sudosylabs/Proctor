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

	"github.com/sudosylabs/proctor/server/app/exam/correction"
	"github.com/sudosylabs/proctor/server/app/realtime"
)

type examCorrectionRealtimeEffects struct {
	realtime    *realtimeService
	collections examCollectionInvalidationEffects
}

func (e examCorrectionRealtimeEffects) Corrected(ctx context.Context, result correction.Result) error {
	managerEvent, err := realtime.NewExamSittingContentCorrectedEvent(result.ExamID, result.SittingID, result.PreviousRevisionID, result.RevisionID, result.SittingRevision, result.EffectiveAt)
	if err != nil {
		return err
	}
	candidateEvent, err := realtime.NewCandidateExamSittingContentCorrectedEvent(result.ExamID, result.SittingID, result.PreviousRevisionID, result.RevisionID, result.SittingRevision, result.EffectiveAt)
	if err != nil {
		return err
	}
	return errors.Join(
		e.realtime.PublishRealtimeEvent(ctx, managerEvent),
		e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
		e.collections.SittingBoardChanged(ctx, result.ExamID, result.SittingID),
		e.collections.CandidateActivityChangedForSitting(ctx, result.SittingID),
	)
}
func (e examCorrectionRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ correction.Authorizer = (*accessControlService)(nil)
var _ correction.Auditor = mutationAuditAdapter{}
var _ correction.Effects = examCorrectionRealtimeEffects{}
var _ correction.EffectFailures = examCorrectionRealtimeEffects{}
