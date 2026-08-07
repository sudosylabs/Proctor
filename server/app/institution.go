// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	Name        *string
	DisplayName *string
	Description *string
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
		model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
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
	resource := model.Resource{Type: model.ResourceInstitution, Id: current.Id}
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
	candidate.PrepareUpdate(s.now().UnixMilli())
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, domainInvalid("institution.invalid", appErr)
	}
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionInstitutionManage, resource,
		"patch", candidate.Auditable(), current.Auditable(),
	)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.InstitutionUpdate{
		Institution: &candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli(),
	})
	if err != nil {
		mapped := institutionError(err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	return updated, nil
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
