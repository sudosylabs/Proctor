// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetProgrammeLevelQuery struct{ ID string }
type ListProgrammeLevelsQuery struct {
	ProgrammeID string
	Query       string
	Limit       int
}
type CreateProgrammeLevelCommand struct {
	ProgrammeID string
	Name        string
	DisplayName string
	Description string
}
type UpdateProgrammeLevelCommand struct {
	ID          string
	Name        *string
	DisplayName *string
	Description *string
}
type ArchiveProgrammeLevelCommand struct{ ID string }

type programmeLevelStore interface {
	Get(context.Context, string) (*model.ProgrammeLevel, error)
	ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error)
	SearchByProgramme(context.Context, string, string, int) ([]*model.ProgrammeLevel, error)
	Create(context.Context, *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error)
	UpdateWithAudit(context.Context, *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error)
	ArchiveWithAudit(context.Context, *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error)
}

type programmeLevelService struct {
	store         programmeLevelStore
	authorization scopedAcademicResourceAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newProgrammeLevelService(
	persistence programmeLevelStore,
	authorization scopedAcademicResourceAuthorizer,
	audit mutationAuditor,
	now func() time.Time,
	newID func() string,
) *programmeLevelService {
	return &programmeLevelService{store: persistence, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetProgrammeLevel(ctx context.Context, invocation Invocation, query GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	return a.programmeLevels.Get(ctx, invocation, query)
}

func (s *programmeLevelService) Get(ctx context.Context, invocation Invocation, query GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "programme_level_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgrammeLevel, ID: id}); err != nil {
		return nil, err
	}
	level, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeLevelError(err)
	}
	return level, nil
}

func (a *App) ListProgrammeLevels(ctx context.Context, invocation Invocation, query ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error) {
	return a.programmeLevels.List(ctx, invocation, query)
}

func (s *programmeLevelService) List(ctx context.Context, invocation Invocation, query ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error) {
	programmeID := strings.TrimSpace(query.ProgrammeID)
	if !model.IsValidId(programmeID) {
		return nil, NewError("request.invalid").WithField("field", "programme_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgramme, ID: programmeID}); err != nil {
		return nil, err
	}
	var levels []*model.ProgrammeLevel
	var err error
	if term := strings.TrimSpace(query.Query); term == "" {
		levels, err = s.store.ListByProgramme(ctx, programmeID)
	} else {
		levels, err = s.store.SearchByProgramme(ctx, programmeID, term, normalizeAdministrationLimit(query.Limit))
	}
	if err != nil {
		return nil, programmeLevelError(err)
	}
	if levels == nil {
		levels = []*model.ProgrammeLevel{}
	}
	return levels, nil
}

func (a *App) CreateProgrammeLevel(ctx context.Context, invocation Invocation, command CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	return a.programmeLevels.Create(ctx, invocation, command)
}

func (s *programmeLevelService) Create(ctx context.Context, invocation Invocation, command CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	programmeID := strings.TrimSpace(command.ProgrammeID)
	if !model.IsValidId(programmeID) {
		return nil, NewError("request.invalid").WithField("field", "programme_id")
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: programmeID}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeLevelManage, resource)
	if err != nil {
		return nil, err
	}
	levelID, err := model.ParseProgrammeLevelID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "programme_level_id").Wrap(err)
	}
	programmeTypedID, err := model.ParseProgrammeID(programmeID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "programme_id").Wrap(err)
	}
	candidate := &model.ProgrammeLevel{
		ProgrammeID: programmeTypedID,
		Name:        command.Name,
		DisplayName: command.DisplayName,
		Description: command.Description,
	}
	candidate.PrepareCreate(levelID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("programme_level.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeLevelManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.ProgrammeLevel, error) {
			return s.store.Create(ctx, &store.ProgrammeLevelCreation{
				Level: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeLevelError,
	)
}

func (a *App) UpdateProgrammeLevel(ctx context.Context, invocation Invocation, command UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	return a.programmeLevels.Update(ctx, invocation, command)
}

func (s *programmeLevelService) Update(ctx context.Context, invocation Invocation, command UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	current, scopeType, scopeID, err := s.levelForMutation(ctx, invocation, command.ID)
	if err != nil {
		return nil, err
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: current.ID.String()}
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
		return nil, domainInvalid("programme_level.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeLevelManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.ProgrammeLevel, error) {
			return s.store.UpdateWithAudit(ctx, &store.ProgrammeLevelUpdate{
				Level: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeLevelError,
	)
}

func (a *App) ArchiveProgrammeLevel(ctx context.Context, invocation Invocation, command ArchiveProgrammeLevelCommand) error {
	return a.programmeLevels.Archive(ctx, invocation, command)
}

func (s *programmeLevelService) Archive(ctx context.Context, invocation Invocation, command ArchiveProgrammeLevelCommand) error {
	current, scopeType, scopeID, err := s.levelForMutation(ctx, invocation, command.ID)
	if err != nil {
		return err
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: current.ID.String()}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionProgrammeLevelManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.ProgrammeLevel, error) {
			return s.store.ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{
				ID: current.ID.String(), ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeLevelError,
	)
	return err
}

func (s *programmeLevelService) levelForMutation(ctx context.Context, invocation Invocation, id string) (*model.ProgrammeLevel, model.RoleScopeType, string, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, "", "", NewError("request.invalid").WithField("field", "programme_level_id")
	}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeLevelManage, model.Resource{Type: model.ResourceProgrammeLevel, ID: id})
	if err != nil {
		return nil, "", "", err
	}
	level, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, "", "", programmeLevelError(err)
	}
	return level, scopeType, scopeID, nil
}

func programmeLevelError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "programme_level").Wrap(err)
	case store.IsConflict(err):
		return NewError("programme_level.conflict").WithField("resource", "programme_level").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("programme_level.invalid").WithField("resource", "programme_level").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "programme_level").Wrap(err)
	}
}
