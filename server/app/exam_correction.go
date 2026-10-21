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

	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSittingCorrectionResourceTarget = store.ExamCorrectionResourceStageTarget

const (
	ExamSittingCorrectionResourceAddition    = store.ExamCorrectionResourceAddition
	ExamSittingCorrectionResourceReplacement = store.ExamCorrectionResourceReplacement
)

type StageExamSittingCorrectionResourceContentCommand = examcorrection.StageResourceContentCommand

type ExamSittingCorrectionResourceStage = examcorrection.ResourceStage

type ExamSittingCorrectionInstructions = examcorrection.OptionalInstructions
type ExamSittingCorrectionBrowserPolicy = examcorrection.OptionalBrowserPolicy
type ExamSittingCorrectionResourceManifestItem = examcorrection.ResourceManifestItem
type ApplyExamSittingCorrectionCommand = examcorrection.ApplyCommand

type ExamSittingCorrectionResult = examcorrection.AppliedCorrection

type examCorrectionRealtimeEffects struct {
	realtime    *realtimeService
	collections examCollectionInvalidationEffects
}

func (e examCorrectionRealtimeEffects) Corrected(ctx context.Context, result examcorrection.Result) error {
	managerEvent, err := apprealtime.NewExamSittingContentCorrectedEvent(result.ExamID, result.SittingID, result.PreviousRevisionID, result.RevisionID, result.SittingRevision, result.EffectiveAt)
	if err != nil {
		return err
	}
	candidateEvent, err := apprealtime.NewCandidateExamSittingContentCorrectedEvent(result.ExamID, result.SittingID, result.PreviousRevisionID, result.RevisionID, result.SittingRevision, result.EffectiveAt)
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

var _ examcorrection.Authorizer = (*accessControlService)(nil)
var _ examcorrection.Auditor = mutationAuditAdapter{}
var _ examcorrection.Effects = examCorrectionRealtimeEffects{}
var _ examcorrection.EffectFailures = examCorrectionRealtimeEffects{}
