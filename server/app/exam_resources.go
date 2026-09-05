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
	"fmt"

	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamResourceRecord = store.ExamResourceRecord
type OpenedExamResource = examresource.Opened

type CreateExamResourceCommand = examresource.CreateCommand
type ReplaceExamResourceContentCommand = examresource.ReplaceContentCommand
type EditExamResourceMetadataCommand = examresource.EditMetadataCommand
type ReorderExamResourcesCommand = examresource.ReorderCommand
type RemoveExamResourceCommand = examresource.RemoveCommand

type ListExamResourcesQuery struct{ ExamID model.ExamID }
type OpenExamResourceQuery struct {
	ExamID     model.ExamID
	ResourceID model.ExamResourceID
}

type examResourceUseCases interface {
	Create(context.Context, examresource.Call, examresource.CreateCommand) (store.ExamResourceRecord, error)
	ReplaceContent(context.Context, examresource.Call, examresource.ReplaceContentCommand) (store.ExamResourceRecord, error)
	EditMetadata(context.Context, examresource.Call, examresource.EditMetadataCommand) (store.ExamResourceRecord, error)
	Reorder(context.Context, examresource.Call, examresource.ReorderCommand) ([]store.ExamResourceRecord, error)
	Remove(context.Context, examresource.Call, examresource.RemoveCommand) (store.ExamResourceRecord, error)
	List(context.Context, examresource.Call, model.ExamID) ([]store.ExamResourceRecord, error)
	Open(context.Context, examresource.Call, model.ExamID, model.ExamResourceID) (examresource.Opened, error)
}

func (a *App) CreateExamResource(ctx context.Context, invocation Invocation, c CreateExamResourceCommand) (ExamResourceRecord, error) {
	result, err := a.examResources.Create(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), c)
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) ReplaceExamResourceContent(ctx context.Context, invocation Invocation, c ReplaceExamResourceContentCommand) (ExamResourceRecord, error) {
	result, err := a.examResources.ReplaceContent(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), c)
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) EditExamResourceMetadata(ctx context.Context, invocation Invocation, c EditExamResourceMetadataCommand) (ExamResourceRecord, error) {
	result, err := a.examResources.EditMetadata(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), c)
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}

func (a *App) ReorderExamResources(ctx context.Context, invocation Invocation, c ReorderExamResourcesCommand) ([]ExamResourceRecord, error) {
	c.ResourceIDs = append([]model.ExamResourceID(nil), c.ResourceIDs...)
	result, err := a.examResources.Reorder(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), c)
	if err != nil {
		return nil, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) RemoveExamResource(ctx context.Context, invocation Invocation, c RemoveExamResourceCommand) (ExamResourceRecord, error) {
	result, err := a.examResources.Remove(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), c)
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) ListExamResources(ctx context.Context, invocation Invocation, q ListExamResourcesQuery) ([]ExamResourceRecord, error) {
	result, err := a.examResources.List(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), q.ExamID)
	if err != nil {
		return nil, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) OpenExamResource(ctx context.Context, invocation Invocation, q OpenExamResourceQuery) (OpenedExamResource, error) {
	result, err := a.examResources.Open(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), q.ExamID, q.ResourceID)
	if err != nil {
		return OpenedExamResource{}, examResourceError(err, true)
	}
	return result, nil
}

func examResourceError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examresource.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.resource.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.resource.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examResourceAuthorizationAdapter struct{ authorization *accessControlService }

func (a examResourceAuthorizationAdapter) Authorize(ctx context.Context, call examresource.Call, action model.Action, resource model.Resource) error {
	return a.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

type examResourceAuditAdapter struct{ audit mutationAuditAdapter }

func (a examResourceAuditAdapter) Begin(ctx context.Context, call examresource.Call, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource, scopeType, scopeID, operation, value, prior)
}
func (a examResourceAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return a.audit.Fail(ctx, id, code)
}

type examResourceRealtimeEffects struct{ realtime *realtimeService }

func (e examResourceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, _ model.ExamResourceID, draftRevision int64, _ string) error {
	event, err := apprealtime.NewExamDraftUpdatedEvent(examID, draftRevision)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}
func (e examResourceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examresource.Authorizer = examResourceAuthorizationAdapter{}
var _ examresource.Auditor = examResourceAuditAdapter{}
var _ examresource.Effects = examResourceRealtimeEffects{}
var _ examresource.EffectFailures = examResourceRealtimeEffects{}
