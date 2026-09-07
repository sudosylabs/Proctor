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
	"strconv"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type retentionPolicyInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type retentionPolicyAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type retentionPolicyService struct {
	policies                store.RetentionPolicyStore
	lifecycle               store.RetentionStore
	institutions            retentionPolicyInstitutionStore
	authorization           retentionPolicyAuthorizer
	audit                   mutationAuditor
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
}

// ReplaceRetentionPolicyCommand completely replaces the
// Institution policy behind optimistic revision and idempotency fences.
type ReplaceRetentionPolicyCommand struct {
	ExpectedRevision int64
	Settings         model.RetentionPolicySettings
	IdempotencyKey   string
}

func newRetentionPolicyService(
	policies store.RetentionPolicyStore,
	lifecycle store.RetentionStore,
	institutions retentionPolicyInstitutionStore,
	authorization retentionPolicyAuthorizer,
	audit mutationAuditor,
	recentAuthenticationTTL time.Duration,
	now func() time.Time,
) (*retentionPolicyService, error) {
	if policies == nil || lifecycle == nil || institutions == nil || authorization == nil || audit == nil || recentAuthenticationTTL <= 0 || now == nil {
		return nil, errors.New("retention policy service dependencies are invalid")
	}
	return &retentionPolicyService{policies: policies, lifecycle: lifecycle, institutions: institutions, authorization: authorization,
		audit: audit, recentAuthenticationTTL: recentAuthenticationTTL, now: now}, nil
}

// GetRetentionPolicy returns the current administrative policy.
func (a *App) GetRetentionPolicy(
	ctx context.Context,
	invocation Invocation,
) (*model.RetentionPolicy, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention_policy.unavailable")
	}
	return a.retentionPolicy.Read(ctx, invocation)
}

// ReplaceRetentionPolicy replaces the current policy.
func (a *App) ReplaceRetentionPolicy(
	ctx context.Context,
	invocation Invocation,
	command ReplaceRetentionPolicyCommand,
) (*model.RetentionPolicy, error) {
	if a == nil || a.retentionPolicy == nil {
		return nil, NewError("retention_policy.unavailable")
	}
	return a.retentionPolicy.Replace(ctx, invocation, command)
}

func (s *retentionPolicyService) Read(
	ctx context.Context,
	invocation Invocation,
) (*model.RetentionPolicy, error) {
	institution, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	policy, err := s.policies.Get(ctx)
	if err != nil || policy == nil || policy.Validate() != nil || policy.InstitutionID != institution.ID {
		return nil, retentionPolicyError(err)
	}
	return policy.Clone(), nil
}

func (s *retentionPolicyService) Replace(
	ctx context.Context,
	invocation Invocation,
	command ReplaceRetentionPolicyCommand,
) (*model.RetentionPolicy, error) {
	if err := requireStrongRecentSession(
		invocation.Principal(),
		s.now(),
		s.recentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	institution, resource, err := s.authorize(
		ctx,
		invocation,
		model.ActionRetentionPolicyManage,
	)
	if err != nil {
		return nil, err
	}
	if command.ExpectedRevision < 1 || command.Settings.Validate() != nil {
		return nil, NewError("retention_policy.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(
		invocation,
		"retention_policy.replace.v1",
		command.IdempotencyKey,
		struct {
			InstitutionID    model.InstitutionID           `json:"institution_id"`
			ExpectedRevision int64                         `json:"expected_revision"`
			Settings         model.RetentionPolicySettings `json:"settings"`
		}{
			InstitutionID:    institution.ID,
			ExpectedRevision: command.ExpectedRevision,
			Settings:         command.Settings,
		},
	)
	if err != nil {
		return nil, err
	}
	stored, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRetentionPolicyManage,
			Resource:   resource,
			ScopeType:  model.RoleScopeInstitution,
			ScopeID:    institution.ID.String(),
			Operation:  "replace",
			Value: map[string]any{
				"expected_revision":         command.ExpectedRevision,
				"submission_retention_days": command.Settings.SubmissionRetentionDays,
				"integrity_retention_days":  command.Settings.IntegrityRetentionDays,
				"audit_retention_days":      command.Settings.AuditRetentionDays,
				"export_retention_days":     command.Settings.ExportRetentionDays,
				"deletion_grace_days":       command.Settings.DeletionGraceDays,
				"candidate_notices":         command.Settings.CandidateNotices,
			},
		},
		s.now,
		func(
			ctx context.Context,
			reference mutationAttemptReference,
		) (*store.RetentionPolicyReplacementResult, error) {
			return s.policies.Replace(
				ctx,
				&store.RetentionPolicyReplacement{
					RetentionMutation: store.RetentionMutation{
						Principal: invocation.Principal(), RecentAuthenticationTTL: s.recentAuthenticationTTL,
						AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
					},
					ExpectedRevision: command.ExpectedRevision,
					Settings:         command.Settings,
				},
				idempotency,
			)
		},
		retentionPolicyError,
	)
	if err != nil {
		return nil, err
	}
	if stored == nil || stored.Policy == nil || stored.Policy.Validate() != nil || stored.Policy.InstitutionID != institution.ID {
		return nil, NewError("retention_policy.unavailable")
	}
	return stored.Policy.Clone(), nil
}

func (s *retentionPolicyService) authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) (*model.Institution, model.Resource, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, model.Resource{}, retentionPolicyError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return nil, model.Resource{}, err
	}
	return institution, resource, nil
}

func retentionPolicyError(err error) error {
	var revision *store.ErrRetentionPolicyRevisionConflict
	var conflict *store.ErrConflict
	switch {
	case errors.As(err, &revision):
		return NewError("retention_policy.revision_conflict").WithField(
			"current_revision",
			strconv.FormatInt(revision.CurrentRevision, 10),
		).Wrap(err)
	case errors.As(err, &conflict) && conflict.Resource == "authorization":
		return NewError("authorization.denied").Wrap(err)
	case idempotencyError(err) != nil:
		return idempotencyError(err)
	default:
		return NewError("retention_policy.unavailable").Wrap(err)
	}
}
