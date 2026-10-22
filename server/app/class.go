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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type GetClassQuery struct {
	ID model.ClassID
}

type ListClassesQuery struct {
	ProgrammeLevelID model.ProgrammeLevelID
	NamePageQuery
}

type SearchClassesQuery struct {
	AcademicUnitID model.AcademicUnitID
	Query          string
	NamePageQuery
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
	ListByProgrammeLevel(context.Context, model.ProgrammeLevelID, store.AcademicNameListOptions) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, model.AcademicUnitID, store.AcademicNameListOptions) ([]*model.Class, error)
	GetAcademicUnitID(context.Context, model.ClassID) (model.AcademicUnitID, error)
	Create(context.Context, *store.ClassCreation) (*model.Class, error)
	Update(context.Context, *store.ClassUpdate) (*model.Class, error)
	Archive(context.Context, *store.ClassArchive) (*model.Class, error)
}

// classServiceDependencies are the collaborators of classService;
// newClassService validates them once and classService keeps them in deps.
type classServiceDependencies struct {
	Store         classStore
	Authorization scopedAcademicResourceAuthorizer
	Audit         mutationAuditor
	Now           func() time.Time
	NewID         func() string
}

type classService struct {
	deps classServiceDependencies
}

func newClassService(deps classServiceDependencies) *classService {
	return &classService{deps: deps}
}

func (s *classService) GetClass(ctx context.Context, invocation invocation.Invocation, query GetClassQuery) (*model.Class, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "class_id")
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassView, model.Resource{Type: model.ResourceClass, ID: id.String()}); err != nil {
		return nil, err
	}
	class, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
	}
	return class, nil
}

func (s *classService) ListClasses(ctx context.Context, invocation invocation.Invocation, query ListClassesQuery) (Page[*model.Class], error) {
	levelID := query.ProgrammeLevelID
	if !levelID.IsValid() {
		return Page[*model.Class]{}, apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.Class]{}, err
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: levelID.String()}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassView, resource); err != nil {
		return Page[*model.Class]{}, err
	}
	classes, err := s.deps.Store.ListByProgrammeLevel(ctx, levelID, store.AcademicNameListOptions{
		AfterName: query.AfterName, AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.Class]{}, classStoreErrors.Translate(err)
	}
	return newPage(classes, limit), nil
}

func (s *classService) SearchClasses(ctx context.Context, invocation invocation.Invocation, query SearchClassesQuery) (Page[*model.Class], error) {
	unitID := query.AcademicUnitID
	if !unitID.IsValid() {
		return Page[*model.Class]{}, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.Class]{}, err
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassView, resource); err != nil {
		return Page[*model.Class]{}, err
	}
	classes, err := s.deps.Store.SearchByAcademicUnit(ctx, unitID, store.AcademicNameListOptions{
		Term: strings.TrimSpace(query.Query), AfterName: query.AfterName, AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.Class]{}, classStoreErrors.Translate(err)
	}
	return newPage(classes, limit), nil
}

func (s *classService) CreateClass(ctx context.Context, invocation invocation.Invocation, command CreateClassCommand) (*model.Class, error) {
	levelID := command.ProgrammeLevelID
	if !levelID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: levelID.String()}
	scopeType, scopeID, err := s.deps.Authorization.AuthorizeWithScope(ctx, invocation, model.ActionClassManage, resource)
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(s.deps.NewID())
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
	candidate.PrepareCreate(classID, s.deps.Now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("class.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionClassManage,
			Resource:   resource,
			ScopeType:  scopeType,
			ScopeID:    scopeID,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.deps.Store.Create(ctx, &store.ClassCreation{
				Class: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		classStoreErrors.Translate,
	)
}

func (s *classService) UpdateClass(ctx context.Context, invocation invocation.Invocation, command UpdateClassCommand) (*model.Class, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: id.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassManage, resource); err != nil {
		return nil, err
	}
	unitID, err := s.deps.Store.GetAcademicUnitID(ctx, id)
	if err != nil {
		return nil, classStoreErrors.Translate(err)
	}
	current, err := s.deps.Store.Get(ctx, id)
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
		if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassManage, destination); err != nil {
			return nil, err
		}
	}
	at := s.deps.Now()
	if !at.After(current.UpdatedAt) {
		at = current.UpdatedAt.Add(time.Millisecond)
	}
	candidate.PrepareUpdate(at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("class.invalid", err)
	}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionClassManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.deps.Store.Update(ctx, &store.ClassUpdate{
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

func (s *classService) ArchiveClass(ctx context.Context, invocation invocation.Invocation, command ArchiveClassCommand) error {
	id := command.ID
	if !id.IsValid() {
		return apperror.New("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: id.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionClassManage, resource); err != nil {
		return err
	}
	unitID, err := s.deps.Store.GetAcademicUnitID(ctx, id)
	if err != nil {
		return classStoreErrors.Translate(err)
	}
	current, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return classStoreErrors.Translate(err)
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "class.conflict"); err != nil {
		return err
	}
	_, err = runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionClassManage,
			Resource:   resource,
			Operation:  "archive",
			Prior:      current.Auditable(),
		},
		func() time.Time {
			at := s.deps.Now()
			if !at.After(current.UpdatedAt) {
				return current.UpdatedAt.Add(time.Millisecond)
			}
			return at
		},
		func(ctx context.Context, reference mutationAttemptReference) (*model.Class, error) {
			return s.deps.Store.Archive(ctx, &store.ClassArchive{
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
