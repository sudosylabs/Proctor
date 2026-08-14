// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

type ExamView = examengine.View

type CreateExamCommand struct {
	AcademicUnitID       model.AcademicUnitID
	Title                string
	InstructionsMarkdown string
	IdempotencyKey       string
}

type GetExamQuery struct{ ExamID model.ExamID }

type examUseCases interface {
	Create(context.Context, examengine.Call, examengine.CreateCommand) (examengine.View, error)
	Get(context.Context, examengine.Call, model.ExamID) (examengine.View, error)
}

func (a *App) CreateExam(ctx context.Context, invocation Invocation, command CreateExamCommand) (ExamView, error) {
	if command.IdempotencyKey == "" {
		return ExamView{}, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, "exam.create.v1", command.IdempotencyKey, struct {
		AcademicUnitID       string `json:"academic_unit_id"`
		Title                string `json:"title"`
		InstructionsMarkdown string `json:"instructions_markdown"`
	}{command.AcademicUnitID.String(), command.Title, command.InstructionsMarkdown})
	if err != nil {
		return ExamView{}, err
	}
	view, err := a.exams.Create(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.CreateCommand{
		AcademicUnitID: command.AcademicUnitID, Title: command.Title,
		InstructionsMarkdown: command.InstructionsMarkdown, Idempotency: idempotency,
	})
	if err != nil {
		return ExamView{}, examError(err, false)
	}
	return view, nil
}

func (a *App) GetExam(ctx context.Context, invocation Invocation, query GetExamQuery) (ExamView, error) {
	view, err := a.exams.Get(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func examError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examengine.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examAuthorizationAdapter struct{ authorization *accessControlService }

func (a examAuthorizationAdapter) Authorize(ctx context.Context, call examengine.Call, action model.Action, resource model.Resource) error {
	return a.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

type examAuditAdapter struct{ audit mutationAuditor }

func (a examAuditAdapter) Begin(ctx context.Context, call examengine.Call, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (string, error) {
	return a.audit.Begin(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource, operation, value, prior)
}
func (a examAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return a.audit.Fail(ctx, id, code)
}

type examRealtimeEffects struct{ realtime *realtimeService }

func (e examRealtimeEffects) Created(ctx context.Context, examID model.ExamID) error {
	return e.realtime.Publish(ctx, apprealtime.RealtimeEvent{Name: "exam_created", Action: model.ActionExamView,
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}})
}
func (e examRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examengine.Authorizer = examAuthorizationAdapter{}
var _ examengine.Auditor = examAuditAdapter{}
var _ examengine.Effects = examRealtimeEffects{}
var _ examengine.EffectFailures = examRealtimeEffects{}
