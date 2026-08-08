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
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassView, model.Resource{Type: model.ResourceClass, Id: id}); err != nil {
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}
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
	candidate := &model.Class{ProgrammeLevelId: levelID, AcademicPeriodId: strings.TrimSpace(command.AcademicPeriodID), Name: command.Name, DisplayName: command.DisplayName, Description: command.Description}
	candidate.PrepareCreate(s.newID(), s.now().UnixMilli())
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, domainInvalid("class.invalid", appErr)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "create", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.ClassCreation{Class: candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classError(err)
	}
	candidate := *current
	if command.ProgrammeLevelID != nil {
		candidate.ProgrammeLevelId = strings.TrimSpace(*command.ProgrammeLevelID)
	}
	if command.AcademicPeriodID != nil {
		candidate.AcademicPeriodId = strings.TrimSpace(*command.AcademicPeriodID)
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
	if candidate.ProgrammeLevelId != current.ProgrammeLevelId {
		destination, err := s.programmeLevelResource(ctx, candidate.ProgrammeLevelId)
		if err != nil {
			return nil, err
		}
		if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, destination); err != nil {
			return nil, err
		}
	}
	updateAt := s.now().UnixMilli()
	if updateAt <= current.UpdateAt {
		updateAt = current.UpdateAt + 1
	}
	candidate.PrepareUpdate(updateAt)
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, domainInvalid("class.invalid", appErr)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "patch", candidate.Auditable(), current.Auditable())
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.ClassUpdate{
		Class:                  &candidate,
		ExpectedAcademicUnitID: unitID,
		ExpectedRevision:       current.Revision,
		AuditEventID:           auditID,
		AuditAt:                s.now().UnixMilli(),
	})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return updated, nil
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return classError(err)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "archive", nil, current.Auditable())
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	if at <= current.UpdateAt {
		at = current.UpdateAt + 1
	}
	_, err = s.store.ArchiveWithAudit(ctx, &store.ClassArchive{
		ID:                     id,
		ExpectedAcademicUnitID: unitID,
		ExpectedRevision:       current.Revision,
		ArchiveAt:              at,
		AuditEventID:           auditID,
		AuditAt:                at,
	})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	return nil
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
	return model.Resource{Type: model.ResourceAcademicUnit, Id: programme.AcademicUnitID.String()}, nil
}

func (s *classService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := classError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
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
