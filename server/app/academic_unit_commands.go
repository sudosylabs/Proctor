// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// CreateAcademicUnitCommand contains caller-controlled Academic Unit fields.
// Installation ownership and persistence lifecycle fields are not inputs.
type CreateAcademicUnitCommand struct {
	ParentID    string
	Name        string
	DisplayName string
	Description string
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

type academicUnitMutationAuditor interface {
	Begin(
		context.Context, Invocation, model.Action, model.Resource, string,
		map[string]any, map[string]any,
	) (string, error)
	Fail(context.Context, string, string) error
}

type academicUnitCommandEffects interface {
	Created(context.Context, string) error
	Updated(context.Context, string) error
	Archived(context.Context, string) error
}

type academicUnitEffectFailures interface {
	Report(context.Context, string, error)
}

type academicUnitAuditAdapter struct{ audit *AuditService }

func (a academicUnitAuditAdapter) Begin(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	event, appErr := a.audit.BeginCriticalAction(
		ctx, invocation.Principal(), action, resource,
		invocation.RequestMetadata(),
		map[string]any{"operation": operation, "value": value}, prior,
	)
	if appErr != nil {
		return "", fromLegacyAppError(appErr)
	}
	return event.Id, nil
}

func (a academicUnitAuditAdapter) Fail(
	ctx context.Context,
	auditID string,
	errorCode string,
) error {
	_, appErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, errorCode, nil,
	)
	return fromLegacyAppError(appErr)
}

type academicUnitRealtimeEffects struct{ realtime *RealtimeService }

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
	return fromLegacyAppError(e.realtime.Publish(ctx, &model.WebSocketEvent{
		Event:  event,
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			Id:   unitID,
		},
	}, model.ClusterSendBestEffort))
}

type academicUnitEffectReporter struct{ realtime *RealtimeService }

func (r academicUnitEffectReporter) Report(
	ctx context.Context,
	operation string,
	err error,
) {
	r.realtime.reportTransientFailure(ctx, operation, err)
}

type academicUnitCommandService struct {
	store          academicUnitCommandStore
	authorization  academicUnitReadAuthorizer
	audit          academicUnitMutationAuditor
	effects        academicUnitCommandEffects
	effectFailures academicUnitEffectFailures
	now            func() time.Time
	newID          func() string
}

func newAcademicUnitCommandService(
	persistence academicUnitCommandStore,
	authorization academicUnitReadAuthorizer,
	audit academicUnitMutationAuditor,
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
		action = model.ActionInstitutionManage
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
		authorized = model.Resource{Type: model.ResourceAcademicUnit, Id: parentID}
		if err := s.authorization.Authorize(ctx, invocation, action, authorized); err != nil {
			return nil, err
		}
		var err error
		institution, err = s.authorization.Installation(ctx)
		if err != nil {
			return nil, err
		}
	}

	candidate := &model.AcademicUnit{
		InstitutionId: institution.Id,
		ParentId:      parentID,
		Name:          command.Name,
		DisplayName:   command.DisplayName,
		Description:   command.Description,
	}
	candidate.PrepareCreate(s.newID(), s.now().UnixMilli())
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, NewError("academic_unit.invalid").
			WithFields(appErr.SafeFields()).
			Wrap(appErr)
	}
	auditID, err := s.audit.Begin(
		ctx, invocation, action, authorized, "create", candidate.Auditable(), nil,
	)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.AcademicUnitCreation{
		Unit: candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli(),
	})
	if err != nil {
		mapped := academicUnitReadError("academic_unit", err)
		mappedFailure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, mappedFailure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	// The unit and success audit are committed before transient fan-out.
	// Publication remains best effort so callers do not retry committed work.
	if err := s.effects.Created(ctx, saved.Id); err != nil {
		s.effectFailures.Report(ctx, "academic_unit_created", err)
	}
	return saved, nil
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: command.ID}
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
		candidate.ParentId = *command.ParentID
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
	candidate.PrepareUpdate(s.now().UnixMilli())
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, NewError("academic_unit.invalid").
			WithFields(appErr.SafeFields()).Wrap(appErr)
	}
	if command.ParentID != nil && candidate.ParentId != "" &&
		candidate.ParentId != current.ParentId {
		if err := s.authorization.Authorize(
			ctx, invocation, model.ActionAcademicUnitManage,
			model.Resource{Type: model.ResourceAcademicUnit, Id: candidate.ParentId},
		); err != nil {
			return nil, err
		}
	}
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionAcademicUnitManage, resource,
		"patch", candidate.Auditable(), current.Auditable(),
	)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateWithAudit(ctx, &store.AcademicUnitUpdate{
		Unit: &candidate, AuditEventID: auditID, AuditAt: s.now().UnixMilli(),
	})
	if err != nil {
		mapped := academicUnitReadError("academic_unit", err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	if err := s.effects.Updated(ctx, updated.Id); err != nil {
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
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: command.ID}
	if err := s.authorization.Authorize(
		ctx, invocation, model.ActionAcademicUnitManage, resource,
	); err != nil {
		return err
	}
	current, err := s.store.Get(ctx, command.ID)
	if err != nil {
		return academicUnitReadError("academic_unit", err)
	}
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionAcademicUnitManage, resource,
		"archive", map[string]any{"id": command.ID}, current.Auditable(),
	)
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	archived, err := s.store.ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
		ID: command.ID, ArchiveAt: at, AuditEventID: auditID, AuditAt: at,
	})
	if err != nil {
		mapped := academicUnitReadError("academic_unit", err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return auditErr
		}
		return mapped
	}
	if err := s.effects.Archived(ctx, archived.Id); err != nil {
		s.effectFailures.Report(ctx, "academic_unit_archived", err)
	}
	return nil
}
