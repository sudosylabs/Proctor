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

type programmeOwnerReader interface {
	Get(context.Context, string) (*model.Programme, error)
}

type programmeLevelService struct {
	store         programmeLevelStore
	programmes    programmeOwnerReader
	authorization academicUnitAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newProgrammeLevelService(
	persistence programmeLevelStore,
	programmes programmeOwnerReader,
	authorization academicUnitAuthorizer,
	audit mutationAuditor,
	now func() time.Time,
	newID func() string,
) *programmeLevelService {
	return &programmeLevelService{store: persistence, programmes: programmes, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetProgrammeLevel(ctx context.Context, invocation Invocation, query GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	return a.programmeLevels.Get(ctx, invocation, query)
}

func (s *programmeLevelService) Get(ctx context.Context, invocation Invocation, query GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	level, _, err := s.levelAndProgramme(ctx, query.ID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, invocation, model.ActionAcademicUnitView, level.ProgrammeID.String()); err != nil {
		return nil, err
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
	if err := s.authorize(ctx, invocation, model.ActionAcademicUnitView, programmeID); err != nil {
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
	resource, err := s.authorizedProgramme(ctx, invocation, model.ActionAcademicUnitManage, programmeID)
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
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "create", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.ProgrammeLevelCreation{Level: candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
}

func (a *App) UpdateProgrammeLevel(ctx context.Context, invocation Invocation, command UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	return a.programmeLevels.Update(ctx, invocation, command)
}

func (s *programmeLevelService) Update(ctx context.Context, invocation Invocation, command UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	current, programme, err := s.levelAndProgramme(ctx, command.ID)
	if err != nil {
		return nil, err
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: programme.AcademicUnitID.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
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
		return nil, domainInvalid("programme_level.invalid", err)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "patch", candidate.Auditable(), current.Auditable())
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.ProgrammeLevelUpdate{Level: &candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return updated, nil
}

func (a *App) ArchiveProgrammeLevel(ctx context.Context, invocation Invocation, command ArchiveProgrammeLevelCommand) error {
	return a.programmeLevels.Archive(ctx, invocation, command)
}

func (s *programmeLevelService) Archive(ctx context.Context, invocation Invocation, command ArchiveProgrammeLevelCommand) error {
	current, programme, err := s.levelAndProgramme(ctx, command.ID)
	if err != nil {
		return err
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: programme.AcademicUnitID.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return err
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "archive", nil, current.Auditable())
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	_, err = s.store.ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: current.ID.String(), ArchiveAt: at, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	return nil
}

func (s *programmeLevelService) levelAndProgramme(ctx context.Context, id string) (*model.ProgrammeLevel, *model.Programme, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, nil, NewError("request.invalid").WithField("field", "programme_level_id")
	}
	level, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, nil, programmeLevelError(err)
	}
	programme, err := s.programmes.Get(ctx, level.ProgrammeID.String())
	if err != nil {
		return nil, nil, programmeError(err)
	}
	return level, programme, nil
}

func (s *programmeLevelService) authorizedProgramme(ctx context.Context, invocation Invocation, action model.Action, id string) (model.Resource, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "programme_id")
	}
	programme, err := s.programmes.Get(ctx, id)
	if err != nil {
		return model.Resource{}, programmeError(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: programme.AcademicUnitID.String()}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (s *programmeLevelService) authorize(ctx context.Context, invocation Invocation, action model.Action, programmeID string) error {
	_, err := s.authorizedProgramme(ctx, invocation, action, programmeID)
	return err
}

func (s *programmeLevelService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := programmeLevelError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
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
