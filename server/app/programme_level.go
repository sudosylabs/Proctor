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

// GetProgrammeLevelQuery names one Programme Level.
type GetProgrammeLevelQuery struct{ ID model.ProgrammeLevelID }
type ListProgrammeLevelsQuery struct {
	ProgrammeID model.ProgrammeID
	Query       string
	NamePageQuery
}
type CreateProgrammeLevelCommand struct {
	ProgrammeID model.ProgrammeID
	Name        string
	DisplayName string
	Description string
}
type UpdateProgrammeLevelCommand struct {
	ExpectedRevision *int64
	ID               model.ProgrammeLevelID
	Name             *string
	DisplayName      *string
	Description      *string
}
type ArchiveProgrammeLevelCommand struct {
	ExpectedRevision *int64
	ID               model.ProgrammeLevelID
}

type programmeLevelStore interface {
	Get(context.Context, model.ProgrammeLevelID) (*model.ProgrammeLevel, error)
	SearchByProgramme(context.Context, model.ProgrammeID, store.AcademicNameListOptions) ([]*model.ProgrammeLevel, error)
	Create(context.Context, *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error)
	Update(context.Context, *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error)
	Archive(context.Context, *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error)
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

func (s *programmeLevelService) GetProgrammeLevel(ctx context.Context, invocation Invocation, query GetProgrammeLevelQuery) (*model.ProgrammeLevel, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgrammeLevel, ID: id.String()}); err != nil {
		return nil, err
	}
	level, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, programmeLevelStoreErrors.Translate(err)
	}
	return level, nil
}

func (s *programmeLevelService) ListProgrammeLevels(ctx context.Context, invocation Invocation, query ListProgrammeLevelsQuery) (Page[*model.ProgrammeLevel], error) {
	programmeID := query.ProgrammeID
	if !programmeID.IsValid() {
		return Page[*model.ProgrammeLevel]{}, apperror.New("request.invalid").WithField("field", "programme_id")
	}
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.ProgrammeLevel]{}, err
	}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionProgrammeLevelView, model.Resource{Type: model.ResourceProgramme, ID: programmeID.String()}); err != nil {
		return Page[*model.ProgrammeLevel]{}, err
	}
	levels, err := s.store.SearchByProgramme(ctx, programmeID, store.AcademicNameListOptions{
		Term: strings.TrimSpace(query.Query), AfterName: query.AfterName, AfterID: query.AfterID, Limit: probe,
	})
	if err != nil {
		return Page[*model.ProgrammeLevel]{}, programmeLevelStoreErrors.Translate(err)
	}
	return newPage(levels, limit), nil
}

func (s *programmeLevelService) CreateProgrammeLevel(ctx context.Context, invocation Invocation, command CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	programmeID := command.ProgrammeID
	if !programmeID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "programme_id")
	}
	resource := model.Resource{Type: model.ResourceProgramme, ID: programmeID.String()}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeLevelManage, resource)
	if err != nil {
		return nil, err
	}
	levelID, err := model.ParseProgrammeLevelID(s.newID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "programme_level_id").Wrap(err)
	}
	programmeTypedID := programmeID
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
		programmeLevelStoreErrors.Translate,
	)
}

func (s *programmeLevelService) UpdateProgrammeLevel(ctx context.Context, invocation Invocation, command UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error) {
	current, scopeType, scopeID, err := s.levelForMutation(ctx, invocation, command.ID)
	if err != nil {
		return nil, err
	}
	resource := model.Resource{Type: model.ResourceProgrammeLevel, ID: current.ID.String()}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "programme_level.conflict"); err != nil {
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
			return s.store.Update(ctx, &store.ProgrammeLevelUpdate{
				Level: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeLevelStoreErrors.Translate,
	)
}

func (s *programmeLevelService) ArchiveProgrammeLevel(ctx context.Context, invocation Invocation, command ArchiveProgrammeLevelCommand) error {
	current, scopeType, scopeID, err := s.levelForMutation(ctx, invocation, command.ID)
	if err != nil {
		return err
	}
	if err := checkAcademicRevision(command.ExpectedRevision, current.Revision, "programme_level.conflict"); err != nil {
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
			return s.store.Archive(ctx, &store.ProgrammeLevelArchive{
				ExpectedRevision: current.Revision,
				ID:               current.ID, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		programmeLevelStoreErrors.Translate,
	)
	return err
}

func (s *programmeLevelService) levelForMutation(ctx context.Context, invocation Invocation, id model.ProgrammeLevelID) (*model.ProgrammeLevel, model.RoleScopeType, string, error) {
	if !id.IsValid() {
		return nil, "", "", apperror.New("request.invalid").WithField("field", "programme_level_id")
	}
	scopeType, scopeID, err := s.authorization.AuthorizeWithScope(ctx, invocation, model.ActionProgrammeLevelManage, model.Resource{Type: model.ResourceProgrammeLevel, ID: id.String()})
	if err != nil {
		return nil, "", "", err
	}
	level, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, "", "", programmeLevelStoreErrors.Translate(err)
	}
	return level, scopeType, scopeID, nil
}

var programmeLevelStoreErrors = apperror.AdministrationStoreTable("programme_level", "programme_level.conflict", "programme_level.invalid", nil)
