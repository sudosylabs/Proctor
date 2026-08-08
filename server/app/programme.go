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
	authorization academicUnitAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newProgrammeService(
	persistence programmeStore,
	authorization academicUnitAuthorizer,
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
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeError(err)
	}
	if err := s.authorize(ctx, invocation, model.ActionAcademicUnitView, programme.AcademicUnitID.String()); err != nil {
		return nil, err
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
	if err := s.authorize(ctx, invocation, model.ActionAcademicUnitView, unitID); err != nil {
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
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
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "create", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.ProgrammeCreation{Programme: candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
}

func (a *App) UpdateProgramme(ctx context.Context, invocation Invocation, command UpdateProgrammeCommand) (*model.Programme, error) {
	return a.programmes.Update(ctx, invocation, command)
}

func (s *programmeService) Update(ctx context.Context, invocation Invocation, command UpdateProgrammeCommand) (*model.Programme, error) {
	current, err := s.getForMutation(ctx, invocation, command.ID)
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: current.AcademicUnitID.String()}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "patch", candidate.Auditable(), current.Auditable())
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.ProgrammeUpdate{Programme: &candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return updated, nil
}

func (a *App) ArchiveProgramme(ctx context.Context, invocation Invocation, command ArchiveProgrammeCommand) error {
	return a.programmes.Archive(ctx, invocation, command)
}

func (s *programmeService) Archive(ctx context.Context, invocation Invocation, command ArchiveProgrammeCommand) error {
	current, err := s.getForMutation(ctx, invocation, command.ID)
	if err != nil {
		return err
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: current.AcademicUnitID.String()}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "archive", nil, current.Auditable())
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	_, err = s.store.ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: current.ID.String(), ArchiveAt: at, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	return nil
}

func (s *programmeService) getForMutation(ctx context.Context, invocation Invocation, id string) (*model.Programme, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "programme_id")
	}
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeError(err)
	}
	if err := s.authorize(ctx, invocation, model.ActionAcademicUnitManage, programme.AcademicUnitID.String()); err != nil {
		return nil, err
	}
	return programme, nil
}

func (s *programmeService) authorize(ctx context.Context, invocation Invocation, action model.Action, unitID string) error {
	return s.authorization.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceAcademicUnit, Id: unitID})
}

func (s *programmeService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := programmeError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
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
