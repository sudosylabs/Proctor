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

// GetProgrammeQuery names one Programme.
type GetProgrammeQuery struct{ ID model.ProgrammeID }
type ListProgrammesQuery struct {
	AcademicUnitID model.AcademicUnitID
	Query          string
	Limit          int
}
type CreateProgrammeCommand struct {
	AcademicUnitID model.AcademicUnitID
	Name           string
	DisplayName    string
	Description    string
}
type UpdateProgrammeCommand struct {
	ExpectedRevision *int64
	ID               model.ProgrammeID
	Name             *string
	DisplayName      *string
	Description      *string
}
type ArchiveProgrammeCommand struct {
	ExpectedRevision *int64
	ID               model.ProgrammeID
}

type programmeStore interface {
	Get(context.Context, model.ProgrammeID) (*model.Programme, error)
	SearchByAcademicUnit(context.Context, model.AcademicUnitID, string, int) ([]*model.Programme, error)
	Create(context.Context, *store.ProgrammeCreation) (*model.Programme, error)
	Update(context.Context, *store.ProgrammeUpdate) (*model.Programme, error)
	Archive(context.Context, *store.ProgrammeArchive) (*model.Programme, error)
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

func (s *programmeService) GetProgramme(ctx context.Context, invocation Invocation, query GetProgrammeQuery) (*model.Programme, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_id")
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: id.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeView, resource); err != nil {
		return nil, err
	}
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeStoreErrors.Translate(err)
	}
	return programme, nil
}

func (s *programmeService) ListProgrammes(ctx context.Context, invocation Invocation, query ListProgrammesQuery) ([]*model.Programme, error) {
	unitID := query.AcademicUnitID
	if !unitID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeView, model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}); err != nil {
		return nil, err
	}
	programmes, err := s.store.SearchByAcademicUnit(
		ctx, unitID, strings.TrimSpace(query.Query), normalizeAdministrationLimit(query.Limit),
	)
	if err != nil {
		return nil, programmeStoreErrors.Translate(err)
	}
	if programmes == nil {
		programmes = []*model.Programme{}
	}
	return programmes, nil
}

func (s *programmeService) CreateProgramme(ctx context.Context, invocation Invocation, command CreateProgrammeCommand) (*model.Programme, error) {
	unitID := command.AcademicUnitID
	if !unitID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeManage, resource)
	if err != nil {
		return nil, err
	}
	programmeID, err := model.ParseProgrammeID(s.newID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "programme_id").Wrap(err)
	}
	academicUnitID := unitID
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
		programmeStoreErrors.Translate,
	)
}

func (s *programmeService) UpdateProgramme(ctx context.Context, invocation Invocation, command UpdateProgrammeCommand) (*model.Programme, error) {
	current, scopeType, scopeID, err := s.getForMutation(ctx, invocation, command.ID)
	if err != nil {
		return nil, err
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "programme.conflict"); err != nil {
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
			return s.store.Update(ctx, &store.ProgrammeUpdate{
				Programme: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeStoreErrors.Translate,
	)
}

func (s *programmeService) ArchiveProgramme(ctx context.Context, invocation Invocation, command ArchiveProgrammeCommand) error {
	current, scopeType, scopeID, err := s.getForMutation(ctx, invocation, command.ID)
	if err != nil {
		return err
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "programme.conflict"); err != nil {
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
			return s.store.Archive(ctx, &store.ProgrammeArchive{
				ExpectedRevision: current.Revision,
				ID:               current.ID, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeStoreErrors.Translate,
	)
	return err
}

func (s *programmeService) getForMutation(ctx context.Context, invocation Invocation, id model.ProgrammeID) (*model.Programme, model.RoleScopeType, string, error) {
	if !id.IsValid() {
		return nil, "", "", apperror.New("request.invalid").WithField("field", "programme_id")
	}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeManage, model.Resource{Type: model.ResourceProgramme, ID: id.String()})
	if err != nil {
		return nil, "", "", err
	}
	programme, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, "", "", programmeStoreErrors.Translate(err)
	}
	return programme, scopeType, scopeID, nil
}

var programmeStoreErrors = apperror.AdministrationStoreTable("programme", "programme.conflict", "programme.invalid", nil)
