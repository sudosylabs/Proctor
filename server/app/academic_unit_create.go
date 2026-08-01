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

type academicUnitCreator interface {
	Create(context.Context, *store.AcademicUnitCreation) (*model.AcademicUnit, error)
}

type academicUnitMutationAuditor interface {
	Begin(
		context.Context, Invocation, model.Action, model.Resource, string,
		map[string]any, map[string]any,
	) (string, error)
	Fail(context.Context, string, string) error
}

type academicUnitCreationEffects interface {
	Created(context.Context, string) error
}

type academicUnitEffectFailures interface {
	Report(context.Context, error)
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
	return fromLegacyAppError(e.realtime.Publish(ctx, &model.WebSocketEvent{
		Event:  "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			Id:   unitID,
		},
	}, model.ClusterSendBestEffort))
}

type academicUnitEffectReporter struct{ realtime *RealtimeService }

func (r academicUnitEffectReporter) Report(ctx context.Context, err error) {
	r.realtime.reportTransientFailure(ctx, "academic_unit_created", err)
}

type academicUnitCommandService struct {
	creator        academicUnitCreator
	authorization  academicUnitReadAuthorizer
	audit          academicUnitMutationAuditor
	effects        academicUnitCreationEffects
	effectFailures academicUnitEffectFailures
	now            func() time.Time
	newID          func() string
}

func newAcademicUnitCommandService(
	creator academicUnitCreator,
	authorization academicUnitReadAuthorizer,
	audit academicUnitMutationAuditor,
	effects academicUnitCreationEffects,
	effectFailures academicUnitEffectFailures,
	now func() time.Time,
	newID func() string,
) *academicUnitCommandService {
	return &academicUnitCommandService{
		creator: creator, authorization: authorization, audit: audit,
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
	saved, err := s.creator.Create(ctx, &store.AcademicUnitCreation{
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
		s.effectFailures.Report(ctx, err)
	}
	return saved, nil
}
