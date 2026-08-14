// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamResourceRecord = store.ExamResourceRecord
type OpenedExamResource = examresource.Opened

type CreateExamResourceCommand struct {
	ExamID                           model.ExamID
	ExpectedDraftRevision            int64
	DisplayName, DescriptionMarkdown string
	MediaType                        model.ExamResourceMediaType
	Body                             io.Reader
	Size                             int64
	ExpectedSHA256, IdempotencyKey   string
}
type ReplaceExamResourceContentCommand struct {
	ExamID                         model.ExamID
	ResourceID                     model.ExamResourceID
	ExpectedDraftRevision          int64
	MediaType                      model.ExamResourceMediaType
	Body                           io.Reader
	Size                           int64
	ExpectedSHA256, IdempotencyKey string
}
type EditExamResourceMetadataCommand struct {
	ExamID                model.ExamID
	ResourceID            model.ExamResourceID
	ExpectedDraftRevision int64
	DisplayName           *string
	DescriptionMarkdown   *string
	IdempotencyKey        string
}
type ReorderExamResourcesCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	ResourceIDs           []model.ExamResourceID
	IdempotencyKey        string
}
type RemoveExamResourceCommand struct {
	ExamID                model.ExamID
	ResourceID            model.ExamResourceID
	ExpectedDraftRevision int64
	IdempotencyKey        string
}
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
	idempotency, err := newExamResourceIdempotency(invocation, "exam.resource.add.v1", c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, "", strings.TrimSpace(c.DisplayName), c.DescriptionMarkdown, c.MediaType, c.Size, c.ExpectedSHA256, nil)
	if err != nil {
		return ExamResourceRecord{}, err
	}
	result, err := a.examResources.Create(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), examresource.CreateCommand{ExamID: c.ExamID, ExpectedDraftRevision: c.ExpectedDraftRevision, DisplayName: c.DisplayName, DescriptionMarkdown: c.DescriptionMarkdown, MediaType: c.MediaType, Body: c.Body, Size: c.Size, ExpectedSHA256: c.ExpectedSHA256, Idempotency: idempotency})
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) ReplaceExamResourceContent(ctx context.Context, invocation Invocation, c ReplaceExamResourceContentCommand) (ExamResourceRecord, error) {
	idempotency, err := newExamResourceIdempotency(invocation, "exam.resource.content.replace.v1", c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, c.ResourceID.String(), "", "", c.MediaType, c.Size, c.ExpectedSHA256, nil)
	if err != nil {
		return ExamResourceRecord{}, err
	}
	result, err := a.examResources.ReplaceContent(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), examresource.ReplaceContentCommand{ExamID: c.ExamID, ResourceID: c.ResourceID, ExpectedDraftRevision: c.ExpectedDraftRevision, MediaType: c.MediaType, Body: c.Body, Size: c.Size, ExpectedSHA256: c.ExpectedSHA256, Idempotency: idempotency})
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) EditExamResourceMetadata(ctx context.Context, invocation Invocation, c EditExamResourceMetadataCommand) (ExamResourceRecord, error) {
	idempotency, err := newExamResourceMetadataIdempotency(invocation, c)
	if err != nil {
		return ExamResourceRecord{}, err
	}
	result, err := a.examResources.EditMetadata(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), examresource.EditMetadataCommand{ExamID: c.ExamID, ResourceID: c.ResourceID, ExpectedDraftRevision: c.ExpectedDraftRevision, DisplayName: c.DisplayName, DescriptionMarkdown: c.DescriptionMarkdown, Idempotency: idempotency})
	if err != nil {
		return ExamResourceRecord{}, examResourceError(err, true)
	}
	return result, nil
}

func newExamResourceMetadataIdempotency(invocation Invocation, c EditExamResourceMetadataCommand) (*store.CommandIdempotency, error) {
	var normalizedName *string
	if c.DisplayName != nil {
		value := strings.TrimSpace(*c.DisplayName)
		normalizedName = &value
	}
	return newCommandIdempotency(invocation, "exam.resource.metadata.edit.v1", c.IdempotencyKey, struct {
		ExamID                string  `json:"exam_id"`
		ExpectedDraftRevision int64   `json:"expected_draft_revision"`
		ResourceID            string  `json:"resource_id"`
		DisplayName           *string `json:"display_name"`
		DescriptionMarkdown   *string `json:"description_markdown"`
	}{c.ExamID.String(), c.ExpectedDraftRevision, c.ResourceID.String(), normalizedName, c.DescriptionMarkdown})
}
func (a *App) ReorderExamResources(ctx context.Context, invocation Invocation, c ReorderExamResourcesCommand) ([]ExamResourceRecord, error) {
	ids := make([]string, len(c.ResourceIDs))
	for i, id := range c.ResourceIDs {
		ids[i] = id.String()
	}
	idempotency, err := newExamResourceIdempotency(invocation, "exam.resource.reorder.v1", c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, "", "", "", "", 0, "", ids)
	if err != nil {
		return nil, err
	}
	result, err := a.examResources.Reorder(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), examresource.ReorderCommand{ExamID: c.ExamID, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceIDs: append([]model.ExamResourceID(nil), c.ResourceIDs...), Idempotency: idempotency})
	if err != nil {
		return nil, examResourceError(err, true)
	}
	return result, nil
}
func (a *App) RemoveExamResource(ctx context.Context, invocation Invocation, c RemoveExamResourceCommand) (ExamResourceRecord, error) {
	idempotency, err := newExamResourceIdempotency(invocation, "exam.resource.remove.v1", c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, c.ResourceID.String(), "", "", "", 0, "", nil)
	if err != nil {
		return ExamResourceRecord{}, err
	}
	result, err := a.examResources.Remove(ctx, examresource.NewCall(invocation.Principal(), invocation.RequestMetadata()), examresource.RemoveCommand{ExamID: c.ExamID, ResourceID: c.ResourceID, ExpectedDraftRevision: c.ExpectedDraftRevision, Idempotency: idempotency})
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

func newExamResourceIdempotency(invocation Invocation, operation, key string, examID model.ExamID, revision int64, resourceID, name, description string, media model.ExamResourceMediaType, size int64, sha string, ids []string) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, NewError("idempotency.key_required")
	}
	return newCommandIdempotency(invocation, operation, key, struct {
		ExamID                string   `json:"exam_id"`
		ExpectedDraftRevision int64    `json:"expected_draft_revision"`
		ResourceID            string   `json:"resource_id,omitempty"`
		DisplayName           string   `json:"display_name,omitempty"`
		DescriptionMarkdown   string   `json:"description_markdown,omitempty"`
		MediaType             string   `json:"media_type,omitempty"`
		Size                  int64    `json:"size,omitempty"`
		SHA256                string   `json:"sha256,omitempty"`
		ResourceIDs           []string `json:"resource_ids,omitempty"`
	}{examID.String(), revision, resourceID, name, description, string(media), size, sha, ids})
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
