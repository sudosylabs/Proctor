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

type GetAcademicPeriodQuery struct{ ID string }
type ListAcademicPeriodsQuery struct {
	Query string
	Limit int
}
type CreateAcademicPeriodCommand struct {
	Name, DisplayName, Description string
	StartAt, EndAt                 int64
}
type UpdateAcademicPeriodCommand struct {
	ID                             string
	Name, DisplayName, Description *string
	StartAt, EndAt                 *int64
}
type ArchiveAcademicPeriodCommand struct{ ID string }

type academicPeriodStore interface {
	Get(context.Context, string) (*model.AcademicPeriod, error)
	ListByInstitution(context.Context, string) ([]*model.AcademicPeriod, error)
	SearchByInstitution(context.Context, string, string, int) ([]*model.AcademicPeriod, error)
	Create(context.Context, *store.AcademicPeriodCreation) (*model.AcademicPeriod, error)
	UpdateWithAudit(context.Context, *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error)
	ArchiveWithAudit(context.Context, *store.AcademicPeriodArchive) (*model.AcademicPeriod, error)
}

type academicPeriodAuthorizer interface {
	AuthorizeInstallation(context.Context, Invocation, model.Action) (model.Resource, error)
}

type academicPeriodService struct {
	store         academicPeriodStore
	authorization academicPeriodAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newAcademicPeriodService(persistence academicPeriodStore, authorization academicPeriodAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *academicPeriodService {
	return &academicPeriodService{store: persistence, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) GetAcademicPeriod(ctx context.Context, invocation Invocation, query GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Get(ctx, invocation, query)
}

func (s *academicPeriodService) Get(ctx context.Context, invocation Invocation, query GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	if _, err := s.authorization.AuthorizeInstallation(ctx, invocation, model.ActionInstitutionManage); err != nil {
		return nil, err
	}
	period, err := s.get(ctx, query.ID)
	if err != nil {
		return nil, err
	}
	return period, nil
}

func (a *App) ListAcademicPeriods(ctx context.Context, invocation Invocation, query ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error) {
	return a.academicPeriods.List(ctx, invocation, query)
}

func (s *academicPeriodService) List(ctx context.Context, invocation Invocation, query ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error) {
	resource, err := s.authorization.AuthorizeInstallation(ctx, invocation, model.ActionInstitutionManage)
	if err != nil {
		return nil, err
	}
	var periods []*model.AcademicPeriod
	if term := strings.TrimSpace(query.Query); term == "" {
		periods, err = s.store.ListByInstitution(ctx, resource.Id)
	} else {
		periods, err = s.store.SearchByInstitution(ctx, resource.Id, term, normalizeAdministrationLimit(query.Limit))
	}
	if err != nil {
		return nil, academicPeriodError(err)
	}
	if periods == nil {
		periods = []*model.AcademicPeriod{}
	}
	return periods, nil
}

func (a *App) CreateAcademicPeriod(ctx context.Context, invocation Invocation, command CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Create(ctx, invocation, command)
}

func (s *academicPeriodService) Create(ctx context.Context, invocation Invocation, command CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	resource, err := s.authorization.AuthorizeInstallation(ctx, invocation, model.ActionInstitutionManage)
	if err != nil {
		return nil, err
	}
	periodID, err := model.ParseAcademicPeriodID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id").Wrap(err)
	}
	institutionID, err := model.ParseInstitutionID(resource.Id)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "institution_id").Wrap(err)
	}
	candidate := &model.AcademicPeriod{
		InstitutionID: institutionID,
		Name:          command.Name,
		DisplayName:   command.DisplayName,
		Description:   command.Description,
		StartsAt:      model.TimeFromMillis(command.StartAt),
		EndsAt:        model.TimeFromMillis(command.EndAt),
	}
	candidate.PrepareCreate(periodID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionInstitutionManage, resource, "create", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
}

func (a *App) UpdateAcademicPeriod(ctx context.Context, invocation Invocation, command UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	return a.academicPeriods.Update(ctx, invocation, command)
}

func (s *academicPeriodService) Update(ctx context.Context, invocation Invocation, command UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	resource, err := s.authorization.AuthorizeInstallation(ctx, invocation, model.ActionInstitutionManage)
	if err != nil {
		return nil, err
	}
	current, err := s.get(ctx, command.ID)
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
	if command.StartAt != nil {
		candidate.StartsAt = model.TimeFromMillis(*command.StartAt)
	}
	if command.EndAt != nil {
		candidate.EndsAt = model.TimeFromMillis(*command.EndAt)
	}
	candidate.PrepareUpdate(s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_period.invalid", err)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionInstitutionManage, resource, "patch", candidate.Auditable(), current.Auditable())
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli()})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return updated, nil
}

func (a *App) ArchiveAcademicPeriod(ctx context.Context, invocation Invocation, command ArchiveAcademicPeriodCommand) error {
	return a.academicPeriods.Archive(ctx, invocation, command)
}

func (s *academicPeriodService) Archive(ctx context.Context, invocation Invocation, command ArchiveAcademicPeriodCommand) error {
	resource, err := s.authorization.AuthorizeInstallation(ctx, invocation, model.ActionInstitutionManage)
	if err != nil {
		return err
	}
	current, err := s.get(ctx, command.ID)
	if err != nil {
		return err
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionInstitutionManage, resource, "archive", nil, current.Auditable())
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	_, err = s.store.ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: current.ID.String(), ArchiveAt: at, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	return nil
}

func (s *academicPeriodService) get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	id = strings.TrimSpace(id)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_period_id")
	}
	period, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, academicPeriodError(err)
	}
	return period, nil
}

func (s *academicPeriodService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := academicPeriodError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
}

func academicPeriodError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "academic_period").Wrap(err)
	case store.IsConflict(err):
		return NewError("academic_period.conflict").WithField("resource", "academic_period").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("academic_period.invalid").WithField("resource", "academic_period").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "academic_period").Wrap(err)
	}
}
