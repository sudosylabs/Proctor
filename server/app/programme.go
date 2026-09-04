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
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetProgrammeQuery struct{ ID string }
type ListProgrammesQuery struct {
	AcademicUnitID string
	Query          string
	Limit          int
}
type CreateProgrammeCommand struct {
	AcademicUnitID string
	Name           string
	DisplayName    string
	Description    string
}
type UpdateProgrammeCommand struct {
	ID          string
	Name        *string
	DisplayName *string
	Description *string
}
type ArchiveProgrammeCommand struct{ ID string }

type programmeStore interface {
	Get(context.Context, string) (*model.Programme, error)
	ListByAcademicUnit(context.Context, string) ([]*model.Programme, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Programme, error)
	Create(context.Context, *store.ProgrammeCreation) (*model.Programme, error)
	UpdateWithAudit(context.Context, *store.ProgrammeUpdate) (*model.Programme, error)
	ArchiveWithAudit(context.Context, *store.ProgrammeArchive) (*model.Programme, error)
}

type programmeService struct {
	store         programmeStore
	authorization scopedAcademicResourceAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newProgrammeService(
	persistence programmeStore,
	authorization scopedAcademicResourceAuthorizer,
	audit mutationAuditor,
	now func() time.Time,
	newID func() string,
) *programmeService {
	return &programmeService{
		store: persistence, authorization: authorization, audit: audit,
		now: now, newID: newID,
	}
}

func (a *App) GetProgramme(ctx context.Context, invocation Invocation, query GetProgrammeQuery) (*model.Programme, error) {
	return a.programmes.Get(ctx, invocation, query)
}

func (s *programmeService) Get(ctx context.Context, invocation Invocation, query GetProgrammeQuery) (*model.Programme, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "programme_id")
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: id}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeView, resource); err != nil {
		return nil, err
	}
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeError(err)
	}
	return programme, nil
}

func (a *App) ListProgrammes(ctx context.Context, invocation Invocation, query ListProgrammesQuery) ([]*model.Programme, error) {
	return a.programmes.List(ctx, invocation, query)
}

func (s *programmeService) List(ctx context.Context, invocation Invocation, query ListProgrammesQuery) ([]*model.Programme, error) {
	unitID := strings.TrimSpace(query.AcademicUnitID)
	if !model.IsValidId(unitID) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeView, model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}); err != nil {
		return nil, err
	}
	var programmes []*model.Programme
	var err error
	if term := strings.TrimSpace(query.Query); term == "" {
		programmes, err = s.store.ListByAcademicUnit(ctx, unitID)
	} else {
		programmes, err = s.store.SearchByAcademicUnit(ctx, unitID, term, normalizeAdministrationLimit(query.Limit))
	}
	if err != nil {
		return nil, programmeError(err)
	}
	if programmes == nil {
		programmes = []*model.Programme{}
	}
	return programmes, nil
}

func (a *App) CreateProgramme(ctx context.Context, invocation Invocation, command CreateProgrammeCommand) (*model.Programme, error) {
	return a.programmes.Create(ctx, invocation, command)
}

func (s *programmeService) Create(ctx context.Context, invocation Invocation, command CreateProgrammeCommand) (*model.Programme, error) {
	unitID := strings.TrimSpace(command.AcademicUnitID)
	if !model.IsValidId(unitID) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeManage, resource)
	if err != nil {
		return nil, err
	}
	programmeID, err := model.ParseProgrammeID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "programme_id").Wrap(err)
	}
	academicUnitID, err := model.ParseAcademicUnitID(unitID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	candidate := &model.Programme{
		AcademicUnitID: academicUnitID,
		Name:           command.Name,
		DisplayName:    command.DisplayName,
		Description:    command.Description,
	}
	candidate.PrepareCreate(programmeID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("programme.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Programme, error) {
			return s.store.Create(ctx, &store.ProgrammeCreation{
				Programme: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeError,
	)
}

func (a *App) UpdateProgramme(ctx context.Context, invocation Invocation, command UpdateProgrammeCommand) (*model.Programme, error) {
	return a.programmes.Update(ctx, invocation, command)
}

func (s *programmeService) Update(ctx context.Context, invocation Invocation, command UpdateProgrammeCommand) (*model.Programme, error) {
	current, scopeType, scopeID, err := s.getForMutation(ctx, invocation, command.ID)
	if err != nil {
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
	candidate.PrepareUpdate(s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("programme.invalid", err)
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: current.ID.String()}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Programme, error) {
			return s.store.UpdateWithAudit(ctx, &store.ProgrammeUpdate{
				Programme: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeError,
	)
}

func (a *App) ArchiveProgramme(ctx context.Context, invocation Invocation, command ArchiveProgrammeCommand) error {
	return a.programmes.Archive(ctx, invocation, command)
}

func (s *programmeService) Archive(ctx context.Context, invocation Invocation, command ArchiveProgrammeCommand) error {
	current, scopeType, scopeID, err := s.getForMutation(ctx, invocation, command.ID)
	if err != nil {
		return err
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: current.ID.String()}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Programme, error) {
			return s.store.ArchiveWithAudit(ctx, &store.ProgrammeArchive{
				ID: current.ID.String(), ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeError,
	)
	return err
}

func (s *programmeService) getForMutation(ctx context.Context, invocation Invocation, id string) (*model.Programme, model.RoleScopeType, string, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, "", "", NewError("request.invalid").WithField("field", "programme_id")
	}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeManage, model.Resource{Type: model.ResourceProgramme, ID: id})
	if err != nil {
		return nil, "", "", err
	}
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, "", "", programmeError(err)
	}
	return programme, scopeType, scopeID, nil
}

func programmeError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "programme").Wrap(err)
	case store.IsConflict(err):
		return NewError("programme.conflict").WithField("resource", "programme").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("programme.invalid").WithField("resource", "programme").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "programme").Wrap(err)
	}
}
