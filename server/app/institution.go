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

type GetInstitutionQuery struct{}

type UpdateInstitutionCommand struct {
	Name         *string
	DisplayName  *string
	Description  *string
	ExamCapacity *model.ExamCapacityPolicy
}

type institutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
	UpdateWithAudit(context.Context, *store.InstitutionUpdate) (*model.Institution, error)
}

type institutionAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type institutionService struct {
	store         institutionStore
	authorization institutionAuthorizer
	audit         mutationAuditor
	now           func() time.Time
}

func newInstitutionService(
	persistence institutionStore,
	authorization institutionAuthorizer,
	audit mutationAuditor,
	now func() time.Time,
) *institutionService {
	return &institutionService{
		store: persistence, authorization: authorization, audit: audit, now: now,
	}
}

func (a *App) GetInstitution(
	ctx context.Context,
	invocation Invocation,
	_ GetInstitutionQuery,
) (*model.Institution, error) {
	return a.institutions.Get(ctx, invocation)
}

func (s *institutionService) Get(
	ctx context.Context,
	invocation Invocation,
) (*model.Institution, error) {
	institution, err := s.store.GetSingleton(ctx)
	if err != nil {
		return nil, institutionError(err)
	}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionInstitutionManage,
		model.Resource{Type: model.ResourceInstitution, ID: institution.ResourceID()},
	); err != nil {
		return nil, err
	}
	return institution, nil
}

func (a *App) UpdateInstitution(
	ctx context.Context,
	invocation Invocation,
	command UpdateInstitutionCommand,
) (*model.Institution, error) {
	return a.institutions.Update(ctx, invocation, command)
}

func (s *institutionService) Update(
	ctx context.Context,
	invocation Invocation,
	command UpdateInstitutionCommand,
) (*model.Institution, error) {
	current, err := s.store.GetSingleton(ctx)
	if err != nil {
		return nil, institutionError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: current.ResourceID()}
	if err := s.authorization.Authorize(
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
	candidate.PrepareUpdate(s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("institution.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionInstitutionManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Institution, error) {
			return s.store.UpdateWithAudit(ctx, &store.InstitutionUpdate{
				Institution: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		institutionError,
	)
}

func institutionError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "institution").Wrap(err)
	case store.IsConflict(err):
		return NewError("institution.conflict").WithField("resource", "institution").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("institution.invalid").WithField("resource", "institution").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "institution").Wrap(err)
	}
}
