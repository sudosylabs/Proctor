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

type GetClassQuery struct {
	ID string
}

type ListClassesQuery struct {
	ProgrammeLevelID string
}

type SearchClassesQuery struct {
	AcademicUnitID string
	Query          string
	Limit          int
}

type CreateClassCommand struct {
	ProgrammeLevelID string
	AcademicPeriodID string
	Name             string
	DisplayName      string
	Description      string
}

type UpdateClassCommand struct {
	ID               string
	ProgrammeLevelID *string
	AcademicPeriodID *string
	Name             *string
	DisplayName      *string
	Description      *string
}

type ArchiveClassCommand struct {
	ID string
}

type classStore interface {
	Get(context.Context, string) (*model.Class, error)
	ListByProgrammeLevel(context.Context, string) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Class, error)
	GetAcademicUnitId(context.Context, string) (string, error)
	Create(context.Context, *store.ClassCreation) (*model.Class, error)
	UpdateWithAudit(context.Context, *store.ClassUpdate) (*model.Class, error)
	ArchiveWithAudit(context.Context, *store.ClassArchive) (*model.Class, error)
}
type classProgrammeLevelReader interface {
	Get(context.Context, string) (*model.ProgrammeLevel, error)
}
type classProgrammeReader interface {
	Get(context.Context, string) (*model.Programme, error)
}
type classAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type classService struct {
	store         classStore
	levels        classProgrammeLevelReader
	programmes    classProgrammeReader
	authorization classAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newClassService(persistence classStore, levels classProgrammeLevelReader, programmes classProgrammeReader, authorization classAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *classService {
	return &classService{store: persistence, levels: levels, programmes: programmes, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetClass(ctx context.Context, invocation Invocation, query GetClassQuery) (*model.Class, error) {
	return a.classes.Get(ctx, invocation, query)
}
func (s *classService) Get(ctx context.Context, invocation Invocation, query GetClassQuery) (*model.Class, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "class_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassView, model.Resource{Type: model.ResourceClass, ID: id}); err != nil {
		return nil, err
	}
	class, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classError(err)
	}
	return class, nil
}

func (a *App) ListClasses(ctx context.Context, invocation Invocation, query ListClassesQuery) ([]*model.Class, error) {
	return a.classes.List(ctx, invocation, query)
}
func (s *classService) List(ctx context.Context, invocation Invocation, query ListClassesQuery) ([]*model.Class, error) {
	levelID := strings.TrimSpace(query.ProgrammeLevelID)
	resource, err := s.programmeLevelResource(ctx, levelID)
	if err != nil {
		return nil, err
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitView, resource); err != nil {
		return nil, err
	}
	classes, err := s.store.ListByProgrammeLevel(ctx, levelID)
	if err != nil {
		return nil, classError(err)
	}
	if classes == nil {
		classes = []*model.Class{}
	}
	return classes, nil
}

func (a *App) SearchClasses(ctx context.Context, invocation Invocation, query SearchClassesQuery) ([]*model.Class, error) {
	return a.classes.Search(ctx, invocation, query)
}
func (s *classService) Search(ctx context.Context, invocation Invocation, query SearchClassesQuery) ([]*model.Class, error) {
	unitID := strings.TrimSpace(query.AcademicUnitID)
	if !model.IsValidId(unitID) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitView, resource); err != nil {
		return nil, err
	}
	classes, err := s.store.SearchByAcademicUnit(ctx, unitID, strings.TrimSpace(query.Query), normalizeAdministrationLimit(query.Limit))
	if err != nil {
		return nil, classError(err)
	}
	if classes == nil {
		classes = []*model.Class{}
	}
	return classes, nil
}

func (a *App) CreateClass(ctx context.Context, invocation Invocation, command CreateClassCommand) (*model.Class, error) {
	return a.classes.Create(ctx, invocation, command)
}
func (s *classService) Create(ctx context.Context, invocation Invocation, command CreateClassCommand) (*model.Class, error) {
	levelID := strings.TrimSpace(command.ProgrammeLevelID)
	resource, err := s.programmeLevelResource(ctx, levelID)
	if err != nil {
		return nil, err
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	programmeLevelID, err := model.ParseProgrammeLevelID(levelID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "programme_level_id").Wrap(err)
	}
	academicPeriodID, err := model.ParseAcademicPeriodID(strings.TrimSpace(command.AcademicPeriodID))
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id").Wrap(err)
	}
	candidate := &model.Class{
		ProgrammeLevelID: programmeLevelID,
		AcademicPeriodID: academicPeriodID,
		Name:             command.Name,
		DisplayName:      command.DisplayName,
		Description:      command.Description,
	}
	candidate.PrepareCreate(classID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("class.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitManage,
			Resource:   resource,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.store.Create(ctx, &store.ClassCreation{
				Class: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		classError,
	)
}

func (a *App) UpdateClass(ctx context.Context, invocation Invocation, command UpdateClassCommand) (*model.Class, error) {
	return a.classes.Update(ctx, invocation, command)
}
func (s *classService) Update(ctx context.Context, invocation Invocation, command UpdateClassCommand) (*model.Class, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "class_id")
	}
	unitID, err := s.store.GetAcademicUnitId(ctx, id)
	if err != nil {
		return nil, classError(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classError(err)
	}
	candidate := *current
	if command.ProgrammeLevelID != nil {
		levelID, err := model.ParseProgrammeLevelID(strings.TrimSpace(*command.ProgrammeLevelID))
		if err != nil {
			return nil, NewError("request.invalid").WithField("field", "programme_level_id").Wrap(err)
		}
		candidate.ProgrammeLevelID = levelID
	}
	if command.AcademicPeriodID != nil {
		periodID, err := model.ParseAcademicPeriodID(strings.TrimSpace(*command.AcademicPeriodID))
		if err != nil {
			return nil, NewError("request.invalid").WithField("field", "academic_period_id").Wrap(err)
		}
		candidate.AcademicPeriodID = periodID
	}
	if command.Name != nil {
		candidate.Name = *command.Name
	}
	if command.DisplayName != nil {
		candidate.DisplayName = *command.DisplayName
	}
	if command.Description != nil {
		candidate.Description = *command.Description
	}
	if candidate.ProgrammeLevelID != current.ProgrammeLevelID {
		destination, err := s.programmeLevelResource(ctx, candidate.ProgrammeLevelID.String())
		if err != nil {
			return nil, err
		}
		if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, destination); err != nil {
			return nil, err
		}
	}
	at := s.now()
	if !at.After(current.UpdatedAt) {
		at = current.UpdatedAt.Add(time.Millisecond)
	}
	candidate.PrepareUpdate(at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("class.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.store.UpdateWithAudit(ctx, &store.ClassUpdate{
				Class:                  &candidate,
				ExpectedAcademicUnitID: unitID,
				ExpectedRevision:       current.Revision,
				AuditEventID:           reference.ID,
				AuditAt:                reference.MutationAtMillis,
			})
		},
		classError,
	)
}

func (a *App) ArchiveClass(ctx context.Context, invocation Invocation, command ArchiveClassCommand) error {
	return a.classes.Archive(ctx, invocation, command)
}
func (s *classService) Archive(ctx context.Context, invocation Invocation, command ArchiveClassCommand) error {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return NewError("request.invalid").WithField("field", "class_id")
	}
	unitID, err := s.store.GetAcademicUnitId(ctx, id)
	if err != nil {
		return classError(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return classError(err)
	}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitManage,
			Resource:   resource,
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		func() time.Time {
			at := s.now()
			if !at.After(current.UpdatedAt) {
				return current.UpdatedAt.Add(time.Millisecond)
			}
			return at
		},
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.store.ArchiveWithAudit(ctx, &store.ClassArchive{
				ID:                     id,
				ExpectedAcademicUnitID: unitID,
				ExpectedRevision:       current.Revision,
				ArchiveAt:              reference.MutationAtMillis,
				AuditEventID:           reference.ID,
				AuditAt:                reference.MutationAtMillis,
			})
		},
		classError,
	)
	return err
}

func (s *classService) programmeLevelResource(ctx context.Context, id string) (model.Resource, error) {
	if !model.IsValidId(id) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "programme_level_id")
	}
	level, err := s.levels.Get(ctx, id)
	if err != nil {
		return model.Resource{}, programmeLevelError(err)
	}
	programme, err := s.programmes.Get(ctx, level.ProgrammeID.String())
	if err != nil {
		return model.Resource{}, programmeError(err)
	}
	return model.Resource{Type: model.ResourceAcademicUnit, ID: programme.AcademicUnitID.String()}, nil
}

func classError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "class").Wrap(err)
	case store.IsConflict(err):
		return NewError("class.conflict").WithField("resource", "class").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("class.invalid").WithField("resource", "class").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "class").Wrap(err)
	}
}
