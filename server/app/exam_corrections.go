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
	"slices"
	"time"

	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
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

type ExamSittingCorrectionResult struct {
	ExamID               model.ExamID
	SittingID            model.ExamSittingID
	PreviousRevisionID   model.ExamRevisionID
	RevisionID           model.ExamRevisionID
	RevisionNumber       int64
	SittingState         model.ExamSittingState
	SittingRevision      int64
	EffectiveAt          time.Time
	AffectedCapabilities []model.CandidateCapability
}

type examCorrectionUseCases interface {
	StageResourceContent(context.Context, Invocation, examcorrection.StageResourceContentCommand) (examcorrection.ResourceStage, error)
	Apply(context.Context, Invocation, examcorrection.ApplyCommand) (examcorrection.Result, error)
}

func (a *App) StageExamSittingCorrectionResourceContent(ctx context.Context, invocation Invocation, command StageExamSittingCorrectionResourceContentCommand) (ExamSittingCorrectionResourceStage, error) {
	result, err := a.examCorrections.StageResourceContent(ctx, invocation, command)
	if err != nil {
		return ExamSittingCorrectionResourceStage{}, examCorrectionError(err, true)
	}
	return result, nil
}

func (a *App) ApplyExamSittingCorrection(ctx context.Context, invocation Invocation, command ApplyExamSittingCorrectionCommand) (ExamSittingCorrectionResult, error) {
	childResources := make([]examcorrection.ResourceManifestItem, len(command.Resources))
	copy(childResources, command.Resources)
	command.Resources = childResources
	command.AffectedCapabilities = slices.Clone(command.AffectedCapabilities)
	result, err := a.examCorrections.Apply(ctx, invocation, command)
	if err != nil {
		return ExamSittingCorrectionResult{}, examCorrectionError(err, true)
	}
	return ExamSittingCorrectionResult{ExamID: result.ExamID, SittingID: result.SittingID, PreviousRevisionID: result.PreviousRevisionID, RevisionID: result.RevisionID, RevisionNumber: result.RevisionNumber, SittingState: result.SittingState, SittingRevision: result.SittingRevision, EffectiveAt: result.EffectiveAt, AffectedCapabilities: slices.Clone(result.AffectedCapabilities)}, nil
}

func examCorrectionError(err error, conceal bool) error {
	return presentExamChildError(err, conceal, "exam.sitting.correction.unavailable", "exam.sitting.correction.not_found")
}

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
		e.realtime.Publish(ctx, managerEvent),
		e.realtime.Publish(ctx, candidateEvent),
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
