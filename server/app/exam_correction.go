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

// ExamSittingCorrectionResourceTarget says whether staged correction content
// adds a new Exam Resource or replaces one already present in the correction's
// base Exam Revision.
type ExamSittingCorrectionResourceTarget = store.ExamCorrectionResourceStageTarget

// Correction resource stage targets: add a new Exam Resource or replace an
// existing one.
const (
	ExamSittingCorrectionResourceAddition    = store.ExamCorrectionResourceAddition
	ExamSittingCorrectionResourceReplacement = store.ExamCorrectionResourceReplacement
)

// StageExamSittingCorrectionResourceContentCommand uploads Exam Resource
// content for a later live correction of an Exam Sitting against
// BaseRevisionID, verified against Size and ExpectedSHA256; IdempotencyKey
// makes it replayable.
type StageExamSittingCorrectionResourceContentCommand = examcorrection.StageResourceContentCommand

// ExamSittingCorrectionResourceStage is staged correction content, referenced
// by StageID from a correction's resource manifest until ExpiresAt.
type ExamSittingCorrectionResourceStage = examcorrection.ResourceStage

// ExamSittingCorrectionInstructions is optional replacement Exam Instructions
// for a correction; Present false leaves them unchanged.
type ExamSittingCorrectionInstructions = examcorrection.OptionalInstructions

// ExamSittingCorrectionBrowserPolicy is an optional replacement Browser Policy
// for a correction; Present false leaves it unchanged.
type ExamSittingCorrectionBrowserPolicy = examcorrection.OptionalBrowserPolicy

// ExamSittingCorrectionResourceManifestItem is one Exam Resource in a
// correction's resulting resource manifest, optionally bound to staged
// content.
type ExamSittingCorrectionResourceManifestItem = examcorrection.ResourceManifestItem

// ApplyExamSittingCorrectionCommand applies a live correction to an Exam
// Sitting, publishing a corrected Exam Revision for its candidates. It is
// fenced by ExpectedSittingRevision and ExpectedCurrentRevisionID and replayed
// by IdempotencyKey; PrivateReason is never shown to candidates.
type ApplyExamSittingCorrectionCommand = examcorrection.ApplyCommand

// ExamSittingCorrectionResult is the public result of applying a live
// correction to an Exam Sitting.
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
