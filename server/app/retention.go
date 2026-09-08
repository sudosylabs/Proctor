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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type CreateRetentionPreviewCommand struct {
	ExpectedPolicyRevision int64
	IdempotencyKey         string
}

type ChangeRetentionControlCommand struct {
	ExpectedRevision       int64
	ExpectedPolicyRevision int64
	PreviewID              model.RetentionPreviewID
	State                  model.RetentionControlState
	IdempotencyKey         string
}

type RetentionRecordPage struct {
	PolicyRevision int64
	AsOf           time.Time
	Items          []RetentionRecordView
	HasMore        bool
}

type RetentionRecordView struct {
	Record      model.RetentionRecord
	Eligibility model.RetentionEligibility
	Retirement  *model.RetentionRetirement
}

func (a *App) GetRetentionControl(ctx context.Context, invocation Invocation) (*model.RetentionControl, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.ReadControl(ctx, invocation)
}

func (s *retentionPolicyService) ReadControl(ctx context.Context, invocation Invocation) (*model.RetentionControl, error) {
	inst, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	control, err := s.lifecycle.GetControl(ctx)
	if err != nil {
		return nil, retentionError(err)
	}
	if control.Validate() != nil || control.InstitutionID != inst.ID {
		return nil, NewError("retention.unavailable")
	}
	return control, nil
}

func (a *App) GetRetentionPreview(ctx context.Context, invocation Invocation, id model.RetentionPreviewID) (*model.RetentionPreview, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.ReadPreview(ctx, invocation, id)
}

func (s *retentionPolicyService) ReadPreview(ctx context.Context, invocation Invocation, id model.RetentionPreviewID) (*model.RetentionPreview, error) {
	inst, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	if !id.IsValid() {
		return nil, NewError("retention.invalid")
	}
	preview, err := s.lifecycle.GetPreview(ctx, id)
	if err != nil {
		return nil, retentionError(err)
	}
	if preview.Validate() != nil || preview.InstitutionID != inst.ID {
		return nil, NewError("retention.unavailable")
	}
	return preview, nil
}

func (a *App) CreateRetentionPreview(ctx context.Context, invocation Invocation, command CreateRetentionPreviewCommand) (*model.RetentionPreview, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.CreatePreview(ctx, invocation, command)
}

func (s *retentionPolicyService) CreatePreview(ctx context.Context, invocation Invocation, command CreateRetentionPreviewCommand) (*model.RetentionPreview, error) {
	inst, resource, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	if command.ExpectedPolicyRevision < 1 {
		return nil, NewError("retention.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, store.RetentionPreviewOperation, command.IdempotencyKey,
		struct {
			InstitutionID  model.InstitutionID
			PolicyRevision int64
		}{inst.ID, command.ExpectedPolicyRevision})
	if err != nil {
		return nil, err
	}
	preview, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionRetentionPolicyView, Resource: resource, ScopeType: model.RoleScopeInstitution,
		ScopeID: inst.ID.String(), Operation: "preview", Value: map[string]any{"policy_revision": command.ExpectedPolicyRevision}}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RetentionPreview, error) {
			return s.lifecycle.CreatePreview(ctx, &store.RetentionPreviewCreation{
				RetentionMutation: store.RetentionMutation{Principal: invocation.Principal(), AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis},
				PreviewID:         model.NewRetentionPreviewID(), ExpectedPolicyRevision: command.ExpectedPolicyRevision}, idempotency)
		}, retentionError)
	if err != nil {
		return nil, err
	}
	if preview.Validate() != nil || preview.InstitutionID != inst.ID {
		return nil, NewError("retention.unavailable")
	}
	return preview, nil
}

func (a *App) ChangeRetentionControl(ctx context.Context, invocation Invocation, command ChangeRetentionControlCommand) (*model.RetentionControl, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.ChangeControl(ctx, invocation, command)
}

func (s *retentionPolicyService) ChangeControl(ctx context.Context, invocation Invocation, command ChangeRetentionControlCommand) (*model.RetentionControl, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	inst, resource, err := s.authorize(ctx, invocation, model.ActionRetentionCleanupManage)
	if err != nil {
		return nil, err
	}
	if command.ExpectedRevision < 1 || command.ExpectedPolicyRevision < 1 ||
		(command.State != model.RetentionControlEnabled && command.State != model.RetentionControlPaused) ||
		(command.State == model.RetentionControlEnabled && !command.PreviewID.IsValid()) ||
		(command.State == model.RetentionControlPaused && !command.PreviewID.IsZero()) {
		return nil, NewError("retention.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, store.RetentionControlOperation, command.IdempotencyKey,
		struct {
			InstitutionID  model.InstitutionID
			Revision       int64
			PolicyRevision int64
			State          model.RetentionControlState
			PreviewID      model.RetentionPreviewID
		}{
			inst.ID, command.ExpectedRevision, command.ExpectedPolicyRevision, command.State, command.PreviewID})
	if err != nil {
		return nil, err
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionRetentionCleanupManage, Resource: resource, ScopeType: model.RoleScopeInstitution,
		ScopeID: inst.ID.String(), Operation: "control", Value: map[string]any{"policy_revision": command.ExpectedPolicyRevision,
			"control_revision": command.ExpectedRevision, "state": string(command.State)}}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.RetentionControlResult, error) {
			return s.lifecycle.ChangeControl(ctx, &store.RetentionControlChange{
				RetentionMutation: store.RetentionMutation{Principal: invocation.Principal(), AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, RecentAuthenticationTTL: s.recentAuthenticationTTL},
				ExpectedRevision:  command.ExpectedRevision, ExpectedPolicyRevision: command.ExpectedPolicyRevision, State: command.State, PreviewID: command.PreviewID}, idempotency)
		}, retentionError)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Control.Validate() != nil || result.Control.InstitutionID != inst.ID {
		return nil, NewError("retention.unavailable")
	}
	return result.Control, nil
}

func (a *App) ListRetentionRecords(ctx context.Context, invocation Invocation, after model.SubmissionID, limit int) (*RetentionRecordPage, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention.unavailable")
	}
	return a.retentionPolicy.ListRecords(ctx, invocation, after, limit)
}

func (s *retentionPolicyService) ListRecords(ctx context.Context, invocation Invocation, after model.SubmissionID, limit int) (*RetentionRecordPage, error) {
	if _, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView); err != nil {
		return nil, err
	}
	if limit < 1 || limit > model.RetentionMaximumPageSize || !after.IsZero() && !after.IsValid() {
		return nil, NewError("retention.invalid")
	}
	stored, err := s.lifecycle.ListRecords(ctx, store.RetentionRecordListOptions{AfterSubmissionID: after, Limit: limit})
	if err != nil {
		return nil, retentionError(err)
	}
	if stored == nil || stored.PolicyRevision < 1 || stored.AsOf.IsZero() || len(stored.Items) > 4*limit {
		return nil, NewError("retention.unavailable")
	}
	page := &RetentionRecordPage{PolicyRevision: stored.PolicyRevision, AsOf: stored.AsOf, Items: make([]RetentionRecordView, len(stored.Items)), HasMore: stored.HasMore}
	for i, item := range stored.Items {
		page.Items[i] = RetentionRecordView{Record: item.Record, Eligibility: item.Eligibility, Retirement: item.Retirement}
	}
	return page, nil
}

func retentionError(err error) error {
	var invalid *store.ErrInvalidInput
	var conflict *store.ErrConflict
	var missing *store.ErrNotFound
	var policy *store.ErrRetentionPolicyRevisionConflict
	switch {
	case idempotencyError(err) != nil:
		return idempotencyError(err)
	case errors.As(err, &policy):
		return retentionPolicyError(err)
	case errors.As(err, &missing):
		return NewError("resource.not_found").Wrap(err)
	case errors.As(err, &invalid):
		return NewError("retention.invalid").Wrap(err)
	case errors.As(err, &conflict):
		if conflict.Resource == "authorization" {
			return NewError("authorization.denied").Wrap(err)
		}
		return NewError("retention.conflict").Wrap(err)
	default:
		return NewError("retention.unavailable").Wrap(err)
	}
}
