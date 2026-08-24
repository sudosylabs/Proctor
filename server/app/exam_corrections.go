// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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

type StageExamSittingCorrectionResourceContentCommand struct {
	ExamID         model.ExamID
	SittingID      model.ExamSittingID
	BaseRevisionID model.ExamRevisionID
	Target         ExamSittingCorrectionResourceTarget
	ResourceID     model.ExamResourceID
	MediaType      model.ExamResourceMediaType
	Body           io.Reader
	Size           int64
	ExpectedSHA256 string
	IdempotencyKey string
}

type ExamSittingCorrectionResourceStage struct {
	StageID    model.ExamCorrectionResourceStageID
	ResourceID model.ExamResourceID
	MediaType  model.ExamResourceMediaType
	Size       int64
	SHA256     string
	ExpiresAt  time.Time
}

type ExamSittingCorrectionInstructions struct {
	Present  bool
	Markdown string
}

type ExamSittingCorrectionResourceManifestItem struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	StageID             model.ExamCorrectionResourceStageID
}

type ApplyExamSittingCorrectionCommand struct {
	ExamID                    model.ExamID
	SittingID                 model.ExamSittingID
	ExpectedSittingRevision   int64
	ExpectedCurrentRevisionID model.ExamRevisionID
	Instructions              ExamSittingCorrectionInstructions
	Resources                 []ExamSittingCorrectionResourceManifestItem
	PrivateReason             string
	IdempotencyKey            string
}

type ExamSittingCorrectionResult struct {
	ExamID             model.ExamID
	SittingID          model.ExamSittingID
	PreviousRevisionID model.ExamRevisionID
	RevisionID         model.ExamRevisionID
	RevisionNumber     int64
	SittingState       model.ExamSittingState
	SittingRevision    int64
	EffectiveAt        time.Time
}

type examCorrectionUseCases interface {
	StageResourceContent(context.Context, examcorrection.Call, examcorrection.StageResourceContentCommand) (examcorrection.ResourceStage, error)
	Apply(context.Context, examcorrection.Call, examcorrection.ApplyCommand) (examcorrection.Result, error)
}

func (a *App) StageExamSittingCorrectionResourceContent(ctx context.Context, invocation Invocation, command StageExamSittingCorrectionResourceContentCommand) (ExamSittingCorrectionResourceStage, error) {
	result, err := a.examCorrections.StageResourceContent(ctx, examcorrection.NewCall(invocation.Principal(), invocation.RequestMetadata()), examcorrection.StageResourceContentCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, BaseRevisionID: command.BaseRevisionID, Target: command.Target,
		ResourceID: command.ResourceID, MediaType: command.MediaType, Body: command.Body, Size: command.Size,
		ExpectedSHA256: command.ExpectedSHA256, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingCorrectionResourceStage{}, examCorrectionError(err, true)
	}
	return ExamSittingCorrectionResourceStage{StageID: result.StageID, ResourceID: result.ResourceID, MediaType: result.MediaType, Size: result.Size, SHA256: result.SHA256, ExpiresAt: result.ExpiresAt}, nil
}

func (a *App) ApplyExamSittingCorrection(ctx context.Context, invocation Invocation, command ApplyExamSittingCorrectionCommand) (ExamSittingCorrectionResult, error) {
	childResources := make([]examcorrection.ResourceManifestItem, len(command.Resources))
	for index, item := range command.Resources {
		childResources[index] = examcorrection.ResourceManifestItem{ResourceID: item.ResourceID, DisplayName: item.DisplayName, DescriptionMarkdown: item.DescriptionMarkdown, StageID: item.StageID}
	}
	result, err := a.examCorrections.Apply(ctx, examcorrection.NewCall(invocation.Principal(), invocation.RequestMetadata()), examcorrection.ApplyCommand{
		ExamID: command.ExamID, SittingID: command.SittingID, ExpectedSittingRevision: command.ExpectedSittingRevision,
		ExpectedCurrentRevisionID: command.ExpectedCurrentRevisionID, Instructions: examcorrection.OptionalInstructions{Present: command.Instructions.Present, Markdown: command.Instructions.Markdown},
		Resources: childResources, PrivateReason: command.PrivateReason, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamSittingCorrectionResult{}, examCorrectionError(err, true)
	}
	return ExamSittingCorrectionResult{ExamID: result.ExamID, SittingID: result.SittingID, PreviousRevisionID: result.PreviousRevisionID, RevisionID: result.RevisionID, RevisionNumber: result.RevisionNumber, SittingState: result.SittingState, SittingRevision: result.SittingRevision, EffectiveAt: result.EffectiveAt}, nil
}

func examCorrectionError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examcorrection.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.sitting.correction.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.sitting.correction.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examCorrectionAuthorizationAdapter struct{ authorization *accessControlService }

func (a examCorrectionAuthorizationAdapter) Authorize(ctx context.Context, call examcorrection.Call, action model.Action, resource model.Resource) error {
	return a.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

type examCorrectionAuditAdapter struct{ audit mutationAuditAdapter }

func (a examCorrectionAuditAdapter) Begin(ctx context.Context, call examcorrection.Call, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource, scopeType, scopeID, operation, value, prior)
}
func (a examCorrectionAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return a.audit.Fail(ctx, id, code)
}

type examCorrectionRealtimeEffects struct{ realtime *realtimeService }

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
	)
}
func (e examCorrectionRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examcorrection.Authorizer = examCorrectionAuthorizationAdapter{}
var _ examcorrection.Auditor = examCorrectionAuditAdapter{}
var _ examcorrection.Effects = examCorrectionRealtimeEffects{}
var _ examcorrection.EffectFailures = examCorrectionRealtimeEffects{}
