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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type retentionPolicyInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

// retentionPolicyServiceDependencies are the collaborators of retentionPolicyService;
// newRetentionPolicyService validates them once and retentionPolicyService keeps them in deps.
type retentionPolicyServiceDependencies struct {
	Policies                store.RetentionPolicyStore
	Lifecycle               store.RetentionStore
	Institutions            retentionPolicyInstitutionStore
	Authorization           resourceAuthorizer
	Audit                   mutationAuditor
	RecentAuthenticationTTL time.Duration
	Now                     func() time.Time
}

type retentionPolicyService struct {
	deps retentionPolicyServiceDependencies
}

// ReplaceRetentionPolicyCommand completely replaces the
// Institution policy behind optimistic revision and idempotency fences.
type ReplaceRetentionPolicyCommand struct {
	ExpectedRevision int64
	Settings         model.RetentionPolicySettings
	IdempotencyKey   string
}

func newRetentionPolicyService(deps retentionPolicyServiceDependencies) (*retentionPolicyService, error) {
	if deps.Policies == nil || deps.Lifecycle == nil || deps.Institutions == nil || deps.Authorization == nil || deps.Audit == nil || deps.RecentAuthenticationTTL <= 0 || deps.Now == nil {
		return nil, errors.New("retention policy service dependencies are invalid")
	}
	return &retentionPolicyService{deps: deps}, nil
}

// GetRetentionPolicy returns the current administrative policy.
func (s *retentionPolicyService) GetRetentionPolicy(
	ctx context.Context,
	invocation invocation.Invocation,
) (*model.RetentionPolicy, error) {
	institution, _, err := s.authorize(ctx, invocation, model.ActionRetentionPolicyView)
	if err != nil {
		return nil, err
	}
	policy, err := s.deps.Policies.Get(ctx)
	if err != nil || policy == nil || policy.Validate() != nil || policy.InstitutionID != institution.ID {
		return nil, retentionPolicyError(err)
	}
	return policy.Clone(), nil
}

// ReplaceRetentionPolicy replaces the current policy.
func (s *retentionPolicyService) ReplaceRetentionPolicy(
	ctx context.Context,
	invocation invocation.Invocation,
	command ReplaceRetentionPolicyCommand,
) (*model.RetentionPolicy, error) {
	if err := requireStrongRecentSession(
		invocation.Principal(),
		s.deps.Now(),
		s.deps.RecentAuthenticationTTL,
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
		return nil, apperror.New("retention_policy.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, apperror.New("idempotency.key_required")
	}
	idempotency, err := idempotency.Prepare(
		invocation,
		"retention_policy.replace",
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
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRetentionPolicyManage,
			Resource:   resource,
			ScopeType:  model.RoleScopeInstitution,
			ScopeID:    institution.ID.String(),
			Operation:  "replace",
			Value: map[string]any{
				"expected_revision":                   command.ExpectedRevision,
				"submission_retention_days":           command.Settings.SubmissionRetentionDays,
				"integrity_retention_days":            command.Settings.IntegrityRetentionDays,
				"browser_activity_retention_days":     command.Settings.BrowserActivityRetentionDays,
				"security_operational_retention_days": command.Settings.SecurityOperationalRetentionDays,
				"audit_retention_days":                command.Settings.AuditRetentionDays,
				"export_retention_days":               command.Settings.ExportRetentionDays,
				"deletion_grace_days":                 command.Settings.DeletionGraceDays,
				"candidate_notices":                   command.Settings.CandidateNotices,
			},
		},
		s.deps.Now,
		func(
			ctx context.Context,
			reference mutationAttemptReference,
		) (*store.RetentionPolicyReplacementResult, error) {
			return s.deps.Policies.Replace(
				ctx,
				&store.RetentionPolicyReplacement{
					RetentionMutation: store.RetentionMutation{
						Principal: invocation.Principal(), RecentAuthenticationTTL: s.deps.RecentAuthenticationTTL,
						AuditEventID: reference.ID, AuditAt: reference.MutationAt,
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
		return nil, apperror.New("retention_policy.unavailable")
	}
	return stored.Policy.Clone(), nil
}

func (s *retentionPolicyService) authorize(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
) (*model.Institution, model.Resource, error) {
	institution, err := s.deps.Institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, model.Resource{}, retentionPolicyError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return nil, model.Resource{}, err
	}
	return institution, resource, nil
}

func retentionPolicyError(err error) error {
	var revision *store.ErrRetentionPolicyRevisionConflict
	if errors.As(err, &revision) {
		return apperror.New("retention_policy.revision_conflict").WithField(
			"current_revision",
			strconv.FormatInt(revision.CurrentRevision, 10),
		).Wrap(err)
	}
	return retentionPolicyStoreErrors.Translate(err)
}

var retentionPolicyStoreErrors = apperror.StoreTable{
	Rules:       []apperror.StoreRule{{Kind: apperror.StoreConflict, Entity: "authorization", Code: "authorization.denied"}},
	Unavailable: "retention_policy.unavailable",
}
