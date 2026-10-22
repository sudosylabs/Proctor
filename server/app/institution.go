// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetInstitutionQuery struct{}

type UpdateInstitutionCommand struct {
	Name         *string
	DisplayName  *string
	Description  *string
	ExamCapacity *model.ExamCapacityPolicy
}

type institutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
	Update(context.Context, *store.InstitutionUpdate) (*model.Institution, error)
}

// institutionServiceDependencies are the collaborators of institutionService;
// newInstitutionService validates them once and institutionService keeps them in deps.
type institutionServiceDependencies struct {
	Store         institutionStore
	Authorization resourceAuthorizer
	Audit         mutationAuditor
	Now           func() time.Time
}

type institutionService struct {
	deps institutionServiceDependencies
}

func newInstitutionService(deps institutionServiceDependencies) *institutionService {
	return &institutionService{deps: deps}
}

func (s *institutionService) GetInstitution(
	ctx context.Context,
	invocation invocation.Invocation,
	_ GetInstitutionQuery,
) (*model.Institution, error) {
	institution, err := s.deps.Store.GetSingleton(ctx)
	if err != nil {
		return nil, institutionStoreErrors.Translate(err)
	}
	if err := s.deps.Authorization.Authorize(
		ctx, invocation, model.ActionInstitutionManage,
		model.Resource{Type: model.ResourceInstitution, ID: institution.ResourceID()},
	); err != nil {
		return nil, err
	}
	return institution, nil
}

func (s *institutionService) UpdateInstitution(
	ctx context.Context,
	invocation invocation.Invocation,
	command UpdateInstitutionCommand,
) (*model.Institution, error) {
	current, err := s.deps.Store.GetSingleton(ctx)
	if err != nil {
		return nil, institutionStoreErrors.Translate(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: current.ResourceID()}
	if err := s.deps.Authorization.Authorize(
		ctx, invocation, model.ActionInstitutionManage, resource,
	); err != nil {
		return nil, err
	}
	candidate := *current
	if command.Name != nil {
		candidate.Name = *command.Name
	}
	if command.DisplayName != nil {
		candidate.DisplayName = *command.DisplayName
	}
	if command.Description != nil {
		candidate.Description = *command.Description
	}
	if command.ExamCapacity != nil {
		candidate.ExamCapacity = *command.ExamCapacity
	}
	candidate.PrepareUpdate(s.deps.Now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("institution.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionInstitutionManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Institution, error) {
			return s.deps.Store.Update(ctx, &store.InstitutionUpdate{
				Institution: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAt,
			})
		},
		institutionStoreErrors.Translate,
	)
}

var institutionStoreErrors = apperror.AdministrationStoreTable("institution", "institution.conflict", "institution.invalid", nil)
