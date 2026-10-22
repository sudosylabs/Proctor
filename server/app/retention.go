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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

func (s *retentionPolicyService) GetRetentionControl(ctx context.Context, invocation invocation.Invocation) (*model.RetentionControl, error) {
	inst, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	control, err := s.deps.Lifecycle.GetControl(ctx)
	if err != nil {
		return nil, retentionError(err)
	}
	if control.Validate() != nil || control.InstitutionID != inst.ID {
		return nil, apperror.New("retention.unavailable")
	}
	return control, nil
}

func (s *retentionPolicyService) GetRetentionPreview(ctx context.Context, invocation invocation.Invocation, id model.RetentionPreviewID) (*model.RetentionPreview, error) {
	inst, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	if !id.IsValid() {
		return nil, apperror.New("retention.invalid")
	}
	preview, err := s.deps.Lifecycle.GetPreview(ctx, id)
	if err != nil {
		return nil, retentionError(err)
	}
	if preview.Validate() != nil || preview.InstitutionID != inst.ID {
		return nil, apperror.New("retention.unavailable")
	}
	return preview, nil
}

func (s *retentionPolicyService) CreateRetentionPreview(ctx context.Context, invocation invocation.Invocation, command CreateRetentionPreviewCommand) (*model.RetentionPreview, error) {
	inst, resource, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	if command.ExpectedPolicyRevision < 1 {
		return nil, apperror.New("retention.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, apperror.New("idempotency.key_required")
	}
	idempotency, err := idempotency.Prepare(invocation, store.RetentionPreviewOperation, command.IdempotencyKey,
		struct {
			InstitutionID  model.InstitutionID
			PolicyRevision int64
		}{inst.ID, command.ExpectedPolicyRevision})
	if err != nil {
		return nil, err
	}
	preview, err := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionRetentionPolicyView, Resource: resource, ScopeType: model.RoleScopeInstitution,
		ScopeID: inst.ID.String(), Operation: "preview", Value: map[string]any{"policy_revision": command.ExpectedPolicyRevision}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RetentionPreview, error) {
			return s.deps.Lifecycle.CreatePreview(ctx, &store.RetentionPreviewCreation{
				RetentionMutation: store.RetentionMutation{Principal: invocation.Principal(), AuditEventID: reference.ID, AuditAt: reference.MutationAt},
				PreviewID:         model.NewRetentionPreviewID(), ExpectedPolicyRevision: command.ExpectedPolicyRevision}, idempotency)
		}, retentionError)
	if err != nil {
		return nil, err
	}
	if preview.Validate() != nil || preview.InstitutionID != inst.ID {
		return nil, apperror.New("retention.unavailable")
	}
	return preview, nil
}

func (s *retentionPolicyService) ChangeRetentionControl(ctx context.Context, invocation invocation.Invocation, command ChangeRetentionControlCommand) (*model.RetentionControl, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
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
		return nil, apperror.New("retention.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, apperror.New("idempotency.key_required")
	}
	idempotency, err := idempotency.Prepare(invocation, store.RetentionControlOperation, command.IdempotencyKey,
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
	result, err := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionRetentionCleanupManage, Resource: resource, ScopeType: model.RoleScopeInstitution,
		ScopeID: inst.ID.String(), Operation: "control", Value: map[string]any{"policy_revision": command.ExpectedPolicyRevision,
			"control_revision": command.ExpectedRevision, "state": string(command.State)}}, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.RetentionControlResult, error) {
			return s.deps.Lifecycle.ChangeControl(ctx, &store.RetentionControlChange{
				RetentionMutation: store.RetentionMutation{Principal: invocation.Principal(), AuditEventID: reference.ID, AuditAt: reference.MutationAt, RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL},
				ExpectedRevision:  command.ExpectedRevision, ExpectedPolicyRevision: command.ExpectedPolicyRevision, State: command.State, PreviewID: command.PreviewID}, idempotency)
		}, retentionError)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Control.Validate() != nil || result.Control.InstitutionID != inst.ID {
		return nil, apperror.New("retention.unavailable")
	}
	return result.Control, nil
}

func (s *retentionPolicyService) ListRetentionRecords(ctx context.Context, invocation invocation.Invocation, after model.SubmissionID, limit int) (*RetentionRecordPage, error) {
	if _, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView); err != nil {
		return nil, err
	}
	if limit < 1 || limit > model.RetentionMaximumPageSize || !after.IsZero() && !after.IsValid() {
		return nil, apperror.New("retention.invalid")
	}
	stored, err := s.deps.Lifecycle.ListRecords(ctx, store.RetentionRecordListOptions{AfterSubmissionID: after, Limit: limit})
	if err != nil {
		return nil, retentionError(err)
	}
	if stored == nil || stored.PolicyRevision < 1 || stored.AsOf.IsZero() || len(stored.Items) > 4*limit {
		return nil, apperror.New("retention.unavailable")
	}
	page := &RetentionRecordPage{PolicyRevision: stored.PolicyRevision, AsOf: stored.AsOf, Items: make([]RetentionRecordView, len(stored.Items)), HasMore: stored.HasMore}
	for i, item := range stored.Items {
		page.Items[i] = RetentionRecordView{Record: item.Record, Eligibility: item.Eligibility, Retirement: item.Retirement}
	}
	return page, nil
}

func retentionError(err error) error {
	var policy *store.ErrRetentionPolicyRevisionConflict
	if errors.As(err, &policy) {
		return retentionPolicyError(err)
	}
	return retentionStoreErrors.Translate(err)
}

var retentionStoreErrors = apperror.StoreTable{
	Rules:       []apperror.StoreRule{{Kind: apperror.StoreConflict, Entity: "authorization", Code: "authorization.denied"}},
	NotFound:    "resource.not_found",
	Conflict:    "retention.conflict",
	Invalid:     "retention.invalid",
	Unavailable: "retention.unavailable",
}
