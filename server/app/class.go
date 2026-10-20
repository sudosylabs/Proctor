// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetClassQuery struct {
	ID model.ClassID
}

type ListClassesQuery struct {
	ProgrammeLevelID model.ProgrammeLevelID
}

type SearchClassesQuery struct {
	AcademicUnitID model.AcademicUnitID
	Query          string
	Limit          int
}

type CreateClassCommand struct {
	ProgrammeLevelID model.ProgrammeLevelID
	AcademicPeriodID string
	Name             string
	DisplayName      string
	Description      string
}

type UpdateClassCommand struct {
	ExpectedRevision *int64
	ID               model.ClassID
	ProgrammeLevelID *string
	AcademicPeriodID *string
	Name             *string
	DisplayName      *string
	Description      *string
}

type ArchiveClassCommand struct {
	ExpectedRevision *int64
	ID               model.ClassID
}

type classStore interface {
	Get(context.Context, model.ClassID) (*model.Class, error)
	ListByProgrammeLevel(context.Context, model.ProgrammeLevelID) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, model.AcademicUnitID, string, int) ([]*model.Class, error)
	GetAcademicUnitID(context.Context, model.ClassID) (model.AcademicUnitID, error)
	Create(context.Context, *store.ClassCreation) (*model.Class, error)
	UpdateWithAudit(context.Context, *store.ClassUpdate) (*model.Class, error)
	ArchiveWithAudit(context.Context, *store.ClassArchive) (*model.Class, error)
}
type classAuthorizer interface {
	scopedAcademicResourceAuthorizer
}

type classService struct {
	store         classStore
	authorization classAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newClassService(persistence classStore, authorization classAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *classService {
	return &classService{store: persistence, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetClass(ctx context.Context, invocation Invocation, query GetClassQuery) (*model.Class, error) {
	return a.classes.Get(ctx, invocation, query)
}
func (s *classService) Get(ctx context.Context, invocation Invocation, query GetClassQuery) (*model.Class, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "class_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassView, model.Resource{Type: model.ResourceClass, ID: id.String()}); err != nil {
		return nil, err
	}
	class, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
	}
	return class, nil
}

func (a *App) ListClasses(ctx context.Context, invocation Invocation, query ListClassesQuery) ([]*model.Class, error) {
	return a.classes.List(ctx, invocation, query)
}
func (s *classService) List(ctx context.Context, invocation Invocation, query ListClassesQuery) ([]*model.Class, error) {
	levelID := query.ProgrammeLevelID
	if !levelID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: levelID.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassView, resource); err != nil {
		return nil, err
	}
	classes, err := s.store.ListByProgrammeLevel(ctx, levelID)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
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
	unitID := query.AcademicUnitID
	if !unitID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassView, resource); err != nil {
		return nil, err
	}
	classes, err := s.store.SearchByAcademicUnit(ctx, unitID, strings.TrimSpace(query.Query), normalizeAdministrationLimit(query.Limit))
	if err != nil {
		return nil, classStoreErrors.Translate(err)
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
	levelID := command.ProgrammeLevelID
	if !levelID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: levelID.String()}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionClassManage, resource)
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(s.newID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	programmeLevelID := levelID
	academicPeriodID, err := model.ParseAcademicPeriodID(strings.TrimSpace(command.AcademicPeriodID))
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "academic_period_id").Wrap(err)
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
			Action:     model.ActionClassManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.store.Create(ctx, &store.ClassCreation{
				Class: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		classStoreErrors.Translate,
	)
}

func (a *App) UpdateClass(ctx context.Context, invocation Invocation, command UpdateClassCommand) (*model.Class, error) {
	return a.classes.Update(ctx, invocation, command)
}
func (s *classService) Update(ctx context.Context, invocation Invocation, command UpdateClassCommand) (*model.Class, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: id.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassManage, resource); err != nil {
		return nil, err
	}
	unitID, err := s.store.GetAcademicUnitID(ctx, id)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "class.conflict"); err != nil {
		return nil, err
	}
	candidate := *current
	if command.ProgrammeLevelID != nil {
		levelID, err := model.ParseProgrammeLevelID(strings.TrimSpace(*command.ProgrammeLevelID))
		if err != nil {
			return nil, apperror.New("request.invalid").WithField("field", "programme_level_id").Wrap(err)
		}
		candidate.ProgrammeLevelID = levelID
	}
	if command.AcademicPeriodID != nil {
		periodID, err := model.ParseAcademicPeriodID(strings.TrimSpace(*command.AcademicPeriodID))
		if err != nil {
			return nil, apperror.New("request.invalid").WithField("field", "academic_period_id").Wrap(err)
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
		destination := model.Resource{Type: model.ResourceProgrammeLevel, ID: candidate.ProgrammeLevelID.String()}
		if err := s.authorization.Authorize(ctx, invocation, model.ActionClassManage, destination); err != nil {
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
			Action:     model.ActionClassManage,
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
		classStoreErrors.Translate,
	)
}

func (a *App) ArchiveClass(ctx context.Context, invocation Invocation, command ArchiveClassCommand) error {
	return a.classes.Archive(ctx, invocation, command)
}
func (s *classService) Archive(ctx context.Context, invocation Invocation, command ArchiveClassCommand) error {
	id := command.ID
	if !id.IsValid() {
		return apperror.New("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: id.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionClassManage, resource); err != nil {
		return err
	}
	unitID, err := s.store.GetAcademicUnitID(ctx, id)
	if err != nil {
		return classStoreErrors.Translate(err)
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return classStoreErrors.Translate(err)
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "class.conflict"); err != nil {
		return err
	}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionClassManage,
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
		classStoreErrors.Translate,
	)
	return err
}

var classStoreErrors = apperror.AdministrationStoreTable("class", "class.conflict", "class.invalid", nil)
