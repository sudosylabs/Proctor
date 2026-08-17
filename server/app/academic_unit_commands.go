// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// CreateAcademicUnitCommand contains caller-controlled Academic Unit fields.
// Installation ownership and persistence lifecycle fields are not inputs.
type CreateAcademicUnitCommand struct {
	ParentID       string
	Name           string
	DisplayName    string
	Description    string
	IdempotencyKey string
}

type UpdateAcademicUnitCommand struct {
	ID          string
	ParentID    *string
	Name        *string
	DisplayName *string
	Description *string
}

type ArchiveAcademicUnitCommand struct {
	ID string
}

type academicUnitCommandStore interface {
	Create(context.Context, *store.AcademicUnitCreation) (*model.AcademicUnit, error)
	Get(context.Context, string) (*model.AcademicUnit, error)
	UpdateWithAudit(context.Context, *store.AcademicUnitUpdate) (*model.AcademicUnit, error)
	ArchiveWithAudit(context.Context, *store.AcademicUnitArchive) (*model.AcademicUnit, error)
}

type academicUnitCommandEffects interface {
	Created(context.Context, string) error
	Updated(context.Context, string) error
	Archived(context.Context, string) error
}

type academicUnitEffectFailures interface {
	Report(context.Context, string, error)
}

type academicUnitRealtimeEffects struct{ realtime *realtimeService }

func (e academicUnitRealtimeEffects) Created(
	ctx context.Context,
	unitID string,
) error {
	return e.publish(ctx, "academic_unit_created", unitID)
}

func (e academicUnitRealtimeEffects) Updated(
	ctx context.Context,
	unitID string,
) error {
	return e.publish(ctx, "academic_unit_updated", unitID)
}

func (e academicUnitRealtimeEffects) Archived(
	ctx context.Context,
	unitID string,
) error {
	return e.publish(ctx, "academic_unit_archived", unitID)
}

func (e academicUnitRealtimeEffects) publish(
	ctx context.Context,
	event string,
	unitID string,
) error {
	return e.realtime.Publish(ctx, apprealtime.RealtimeEvent{
		Name:   event,
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	})
}

type academicUnitEffectReporter struct{ realtime *realtimeService }

func (r academicUnitEffectReporter) Report(
	ctx context.Context,
	operation string,
	err error,
) {
	r.realtime.reportTransientFailure(ctx, operation, err)
}

type academicUnitCommandService struct {
	store          academicUnitCommandStore
	authorization  academicUnitAuthorizer
	audit          mutationAuditor
	effects        academicUnitCommandEffects
	effectFailures academicUnitEffectFailures
	now            func() time.Time
	newID          func() string
}

func newAcademicUnitCommandService(
	persistence academicUnitCommandStore,
	authorization academicUnitAuthorizer,
	audit mutationAuditor,
	effects academicUnitCommandEffects,
	effectFailures academicUnitEffectFailures,
	now func() time.Time,
	newID func() string,
) *academicUnitCommandService {
	return &academicUnitCommandService{
		store: persistence, authorization: authorization, audit: audit,
		effects: effects, effectFailures: effectFailures, now: now, newID: newID,
	}
}

// CreateAcademicUnit creates a root or child Academic Unit through one
// application-owned authorization and one atomic durable mutation.
func (a *App) CreateAcademicUnit(
	ctx context.Context,
	invocation Invocation,
	command CreateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	return a.academicUnitCommands.Create(ctx, invocation, command)
}

func (s *academicUnitCommandService) Create(
	ctx context.Context,
	invocation Invocation,
	command CreateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	parentID := command.ParentID
	var institution model.Resource
	var authorized model.Resource
	var action model.Action
	if parentID == "" {
		action = model.ActionAcademicUnitManage
		var err error
		institution, err = s.authorization.AuthorizeInstallation(ctx, invocation, action)
		if err != nil {
			return nil, err
		}
		authorized = institution
	} else {
		if !model.IsValidId(parentID) {
			return nil, NewError("request.invalid").WithField("field", "parent_id")
		}
		action = model.ActionAcademicUnitManage
		authorized = model.Resource{Type: model.ResourceAcademicUnit, ID: parentID}
		if err := s.authorization.Authorize(ctx, invocation, action, authorized); err != nil {
			return nil, err
		}
		var err error
		institution, err = s.authorization.Installation(ctx)
		if err != nil {
			return nil, err
		}
	}

	unitID, err := model.ParseAcademicUnitID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	institutionID, err := model.ParseInstitutionID(institution.ID)
	if err != nil {
		return nil, NewError("administration.unavailable").WithField("resource", "institution").Wrap(err)
	}
	var parentUnitID model.AcademicUnitID
	if parentID != "" {
		parentUnitID, err = model.ParseAcademicUnitID(parentID)
		if err != nil {
			return nil, NewError("request.invalid").WithField("field", "parent_id").Wrap(err)
		}
	}
	candidate := &model.AcademicUnit{
		InstitutionID: model.InstitutionID(institutionID),
		ParentID:      model.AcademicUnitID(parentUnitID),
		Name:          command.Name,
		DisplayName:   command.DisplayName,
		Description:   command.Description,
	}
	candidate.PrepareCreate(unitID, s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_unit.invalid", err)
	}
	idempotency, err := newCommandIdempotency(invocation, "academic_unit.create.v1", command.IdempotencyKey, struct {
		ParentID, Name, DisplayName, Description string
	}{command.ParentID, command.Name, command.DisplayName, command.Description})
	if err != nil {
		return nil, err
	}
	saved, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     action,
			Resource:   authorized,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.AcademicUnitCommandResult, error) {
			input := &store.AcademicUnitCreation{
				Unit: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			}
			if idempotency == nil {
				unit, createErr := s.store.Create(ctx, input)
				return &store.AcademicUnitCommandResult{Value: unit}, createErr
			}
			idempotentStore, ok := s.store.(interface {
				CreateIdempotently(context.Context, *store.AcademicUnitCreation, *store.CommandIdempotency) (*store.AcademicUnitCommandResult, error)
			})
			if !ok {
				return nil, errors.New("academic unit store does not support idempotent creation")
			}
			return idempotentStore.CreateIdempotently(ctx, input, idempotency)
		},
		func(err error) error {
			if mapped := idempotencyError(err); mapped != nil {
				return mapped
			}
			return academicUnitReadError("academic_unit", err)
		},
	)
	if err != nil {
		return nil, err
	}
	// The unit and success audit are committed before transient fan-out.
	// Publication remains best effort so callers do not retry committed work.
	if !saved.Replayed {
		if err := s.effects.Created(ctx, saved.Value.ID.String()); err != nil {
			s.effectFailures.Report(ctx, "academic_unit_created", err)
		}
	}
	return saved.Value, nil
}

func (a *App) UpdateAcademicUnit(
	ctx context.Context,
	invocation Invocation,
	command UpdateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	return a.academicUnitCommands.Update(ctx, invocation, command)
}

func (s *academicUnitCommandService) Update(
	ctx context.Context,
	invocation Invocation,
	command UpdateAcademicUnitCommand,
) (*model.AcademicUnit, error) {
	if !model.IsValidId(command.ID) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: command.ID}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitManage, resource,
	); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, command.ID)
	if err != nil {
		return nil, academicUnitReadError("academic_unit", err)
	}
	candidate := *current
	if command.ParentID != nil {
		parentID := strings.TrimSpace(*command.ParentID)
		if parentID == "" {
			candidate.ParentID = ""
		} else {
			parentUnitID, parseErr := model.ParseAcademicUnitID(parentID)
			if parseErr != nil {
				return nil, NewError("request.invalid").WithField("field", "parent_id").Wrap(parseErr)
			}
			candidate.ParentID = parentUnitID
		}
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
	candidate.PrepareUpdate(s.now())
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_unit.invalid", err)
	}
	if command.ParentID != nil && candidate.ParentID != current.ParentID {
		destination := model.Resource{Type: model.ResourceAcademicUnit, ID: candidate.ParentID.String()}
		if candidate.ParentID.IsZero() {
			destination = model.Resource{Type: model.ResourceInstitution, ID: current.InstitutionID.String()}
		}
		if err := s.authorization.Authorize(
			ctx, invocation, model.ActionAcademicUnitManage, destination,
		); err != nil {
			return nil, err
		}
	}
	updated, err := runAuditedMutation(
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
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicUnit, error) {
			return s.store.UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
				Unit: &candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		func(err error) error { return academicUnitReadError("academic_unit", err) },
	)
	if err != nil {
		return nil, err
	}
	if err := s.effects.Updated(ctx, updated.ID.String()); err != nil {
		s.effectFailures.Report(ctx, "academic_unit_updated", err)
	}
	return updated, nil
}

func (a *App) ArchiveAcademicUnit(
	ctx context.Context,
	invocation Invocation,
	command ArchiveAcademicUnitCommand,
) error {
	return a.academicUnitCommands.Archive(ctx, invocation, command)
}

func (s *academicUnitCommandService) Archive(
	ctx context.Context,
	invocation Invocation,
	command ArchiveAcademicUnitCommand,
) error {
	if !model.IsValidId(command.ID) {
		return NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: command.ID}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitManage, resource,
	); err != nil {
		return err
	}
	current, err := s.store.Get(ctx, command.ID)
	if err != nil {
		return academicUnitReadError("academic_unit", err)
	}
	archived, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitManage,
			Resource:   resource,
			Operation:  "archive",
			Value:      map[string]any{"id": command.ID},
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicUnit, error) {
			return s.store.ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
				ID: command.ID, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		func(err error) error { return academicUnitReadError("academic_unit", err) },
	)
	if err != nil {
		return err
	}
	if err := s.effects.Archived(ctx, archived.ID.String()); err != nil {
		s.effectFailures.Report(ctx, "academic_unit_archived", err)
	}
	return nil
}
