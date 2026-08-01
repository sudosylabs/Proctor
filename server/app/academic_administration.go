// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Application-owned academic administration follows Mattermost's pattern:
// authorize at the use-case boundary, keep persistence behind per-model
// stores, and surround every durable mutation with an authoritative audit.

package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const defaultAdministrationListLimit = 100

func (a *App) programmeAcademicUnit(ctx context.Context, programmeID string) (*model.Programme, string, *model.AppError) {
	programme, err := a.Store().Programme().Get(ctx, programmeID)
	if err != nil {
		return nil, "", administrationError("programmeAcademicUnit", "programme", err)
	}
	return programme, programme.AcademicUnitId, nil
}

func (a *App) programmeLevelAcademicUnit(
	ctx context.Context,
	levelID string,
) (*model.ProgrammeLevel, string, *model.AppError) {
	level, err := a.Store().ProgrammeLevel().Get(ctx, levelID)
	if err != nil {
		return nil, "", administrationError("programmeLevelAcademicUnit", "programme_level", err)
	}
	_, unitID, appErr := a.programmeAcademicUnit(ctx, level.ProgrammeId)
	return level, unitID, appErr
}

func (a *App) GetProgramme(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) (*model.Programme, *model.AppError) {
	programme, unitID, appErr := a.programmeAcademicUnit(ctx, id)
	if appErr != nil {
		return nil, appErr
	}
	if _, appErr = a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	return programme, nil
}

func (a *App) ListProgrammes(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, unitID, query string, limit int) ([]*model.Programme, *model.AppError) {
	if _, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	var programmes []*model.Programme
	var err error
	if strings.TrimSpace(query) == "" {
		programmes, err = a.Store().Programme().ListByAcademicUnit(ctx, unitID)
	} else {
		programmes, err = a.Store().Programme().SearchByAcademicUnit(
			ctx, unitID, strings.TrimSpace(query), normalizeAdministrationLimit(limit),
		)
	}
	if err != nil {
		return nil, administrationError("ListProgrammes", "programme", err)
	}
	return programmes, nil
}

func (a *App) CreateProgramme(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, programme *model.Programme) (*model.Programme, *model.AppError) {
	if programme == nil {
		return nil, invalidAdministrationRequest("CreateProgramme", "programme")
	}
	candidate := *programme
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, candidate.AcademicUnitId, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"CreateProgramme", "programme", candidate.Auditable(),
		func() (*model.Programme, error) { return a.Store().Programme().Save(ctx, &candidate) },
	)
}

func (a *App) PatchProgramme(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string, patch *model.ProgrammePatch) (*model.Programme, *model.AppError) {
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchProgramme", "patch")
	}
	current, unitID, appErr := a.programmeAcademicUnit(ctx, id)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	candidate := *current
	candidate.Patch(patch)
	return updateAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"PatchProgramme", "programme", candidate.Auditable(), current.Auditable(),
		func() (*model.Programme, error) { return a.Store().Programme().Update(ctx, &candidate) },
	)
}

func (a *App) ArchiveProgramme(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) *model.AppError {
	current, unitID, appErr := a.programmeAcademicUnit(ctx, id)
	if appErr != nil {
		return appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return appErr
	}
	return archiveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"ArchiveProgramme", "programme", id, current.Auditable(),
		func(at int64) (*model.Programme, error) { return a.Store().Programme().Delete(ctx, id, at) },
	)
}

func (a *App) GetProgrammeLevel(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) (*model.ProgrammeLevel, *model.AppError) {
	level, unitID, appErr := a.programmeLevelAcademicUnit(ctx, id)
	if appErr != nil {
		return nil, appErr
	}
	if _, appErr = a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	return level, nil
}

func (a *App) ListProgrammeLevels(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, programmeID, query string, limit int) ([]*model.ProgrammeLevel, *model.AppError) {
	_, unitID, appErr := a.programmeAcademicUnit(ctx, programmeID)
	if appErr != nil {
		return nil, appErr
	}
	if _, appErr = a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	var levels []*model.ProgrammeLevel
	var err error
	if strings.TrimSpace(query) == "" {
		levels, err = a.Store().ProgrammeLevel().ListByProgramme(ctx, programmeID)
	} else {
		levels, err = a.Store().ProgrammeLevel().SearchByProgramme(
			ctx, programmeID, strings.TrimSpace(query), normalizeAdministrationLimit(limit),
		)
	}
	if err != nil {
		return nil, administrationError("ListProgrammeLevels", "programme_level", err)
	}
	return levels, nil
}

func (a *App) CreateProgrammeLevel(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, level *model.ProgrammeLevel) (*model.ProgrammeLevel, *model.AppError) {
	if level == nil {
		return nil, invalidAdministrationRequest("CreateProgrammeLevel", "programme_level")
	}
	candidate := *level
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	_, unitID, appErr := a.programmeAcademicUnit(ctx, candidate.ProgrammeId)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"CreateProgrammeLevel", "programme_level", candidate.Auditable(),
		func() (*model.ProgrammeLevel, error) { return a.Store().ProgrammeLevel().Save(ctx, &candidate) },
	)
}

func (a *App) PatchProgrammeLevel(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string, patch *model.ProgrammeLevelPatch) (*model.ProgrammeLevel, *model.AppError) {
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchProgrammeLevel", "patch")
	}
	current, unitID, appErr := a.programmeLevelAcademicUnit(ctx, id)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	candidate := *current
	candidate.Patch(patch)
	return updateAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"PatchProgrammeLevel", "programme_level", candidate.Auditable(), current.Auditable(),
		func() (*model.ProgrammeLevel, error) { return a.Store().ProgrammeLevel().Update(ctx, &candidate) },
	)
}

func (a *App) ArchiveProgrammeLevel(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) *model.AppError {
	current, unitID, appErr := a.programmeLevelAcademicUnit(ctx, id)
	if appErr != nil {
		return appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return appErr
	}
	return archiveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"ArchiveProgrammeLevel", "programme_level", id, current.Auditable(),
		func(at int64) (*model.ProgrammeLevel, error) { return a.Store().ProgrammeLevel().Delete(ctx, id, at) },
	)
}

func (a *App) GetAcademicPeriod(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) (*model.AcademicPeriod, *model.AppError) {
	if _, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	); appErr != nil {
		return nil, appErr
	}
	period, err := a.Store().AcademicPeriod().Get(ctx, id)
	if err != nil {
		return nil, administrationError("GetAcademicPeriod", "academic_period", err)
	}
	return period, nil
}

func (a *App) ListAcademicPeriods(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, query string, limit int) ([]*model.AcademicPeriod, *model.AppError) {
	if _, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	); appErr != nil {
		return nil, appErr
	}
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, administrationError("ListAcademicPeriods.institution", "institution", err)
	}
	var periods []*model.AcademicPeriod
	if strings.TrimSpace(query) == "" {
		periods, err = a.Store().AcademicPeriod().ListByInstitution(ctx, institution.Id)
	} else {
		periods, err = a.Store().AcademicPeriod().SearchByInstitution(
			ctx, institution.Id, strings.TrimSpace(query), normalizeAdministrationLimit(limit),
		)
	}
	if err != nil {
		return nil, administrationError("ListAcademicPeriods", "academic_period", err)
	}
	return periods, nil
}

func (a *App) CreateAcademicPeriod(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, period *model.AcademicPeriod) (*model.AcademicPeriod, *model.AppError) {
	if period == nil {
		return nil, invalidAdministrationRequest("CreateAcademicPeriod", "academic_period")
	}
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return nil, administrationError("CreateAcademicPeriod.institution", "institution", err)
	}
	candidate := *period
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	candidate.InstitutionId = institution.Id
	resource, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionInstitutionManage, resource,
		"CreateAcademicPeriod", "academic_period", candidate.Auditable(),
		func() (*model.AcademicPeriod, error) { return a.Store().AcademicPeriod().Save(ctx, &candidate) },
	)
}

func (a *App) PatchAcademicPeriod(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string, patch *model.AcademicPeriodPatch) (*model.AcademicPeriod, *model.AppError) {
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchAcademicPeriod", "patch")
	}
	resource, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	current, err := a.Store().AcademicPeriod().Get(ctx, id)
	if err != nil {
		return nil, administrationError("PatchAcademicPeriod.get", "academic_period", err)
	}
	candidate := *current
	candidate.Patch(patch)
	return updateAcademicEntity(
		a, ctx, principal, metadata, model.ActionInstitutionManage, resource,
		"PatchAcademicPeriod", "academic_period", candidate.Auditable(), current.Auditable(),
		func() (*model.AcademicPeriod, error) { return a.Store().AcademicPeriod().Update(ctx, &candidate) },
	)
}

func (a *App) ArchiveAcademicPeriod(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) *model.AppError {
	resource, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	)
	if appErr != nil {
		return appErr
	}
	current, err := a.Store().AcademicPeriod().Get(ctx, id)
	if err != nil {
		return administrationError("ArchiveAcademicPeriod.get", "academic_period", err)
	}
	return archiveAcademicEntity(
		a, ctx, principal, metadata, model.ActionInstitutionManage, resource,
		"ArchiveAcademicPeriod", "academic_period", id, current.Auditable(),
		func(at int64) (*model.AcademicPeriod, error) { return a.Store().AcademicPeriod().Delete(ctx, id, at) },
	)
}

func (a *App) GetClass(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) (*model.Class, *model.AppError) {
	if _, appErr := a.authorizePrincipalToClass(
		ctx, principal, id, model.ActionClassView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	class, err := a.Store().Class().Get(ctx, id)
	if err != nil {
		return nil, administrationError("GetClass", "class", err)
	}
	return class, nil
}

func (a *App) ListClasses(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, levelID string) ([]*model.Class, *model.AppError) {
	_, unitID, appErr := a.programmeLevelAcademicUnit(ctx, levelID)
	if appErr != nil {
		return nil, appErr
	}
	if _, appErr = a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	classes, err := a.Store().Class().ListByProgrammeLevel(ctx, levelID)
	if err != nil {
		return nil, administrationError("ListClasses", "class", err)
	}
	return classes, nil
}

func (a *App) SearchClasses(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	academicUnitID string,
	query string,
	limit int,
) ([]*model.Class, *model.AppError) {
	if _, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, academicUnitID, model.ActionAcademicUnitView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	classes, err := a.Store().Class().SearchByAcademicUnit(
		ctx,
		academicUnitID,
		strings.TrimSpace(query),
		normalizeAdministrationLimit(limit),
	)
	if err != nil {
		return nil, administrationError("SearchClasses", "class", err)
	}
	return classes, nil
}

func (a *App) CreateClass(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, class *model.Class) (*model.Class, *model.AppError) {
	if class == nil {
		return nil, invalidAdministrationRequest("CreateClass", "class")
	}
	candidate := *class
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	_, unitID, appErr := a.programmeLevelAcademicUnit(ctx, candidate.ProgrammeLevelId)
	if appErr != nil {
		return nil, appErr
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"CreateClass", "class", candidate.Auditable(),
		func() (*model.Class, error) { return a.Store().Class().Save(ctx, &candidate) },
	)
}

func (a *App) PatchClass(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string, patch *model.ClassPatch) (*model.Class, *model.AppError) {
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchClass", "patch")
	}
	current, err := a.Store().Class().Get(ctx, id)
	if err != nil {
		return nil, administrationError("PatchClass.get", "class", err)
	}
	oldUnitID, err := a.Store().Class().GetAcademicUnitId(ctx, id)
	if err != nil {
		return nil, administrationError("PatchClass.unit", "academic_unit", err)
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, oldUnitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	candidate := *current
	candidate.Patch(patch)
	if candidate.ProgrammeLevelId != current.ProgrammeLevelId {
		if _, newUnitID, appErr := a.programmeLevelAcademicUnit(ctx, candidate.ProgrammeLevelId); appErr != nil {
			return nil, appErr
		} else if _, appErr = a.authorizePrincipalToAcademicUnit(
			ctx, principal, newUnitID, model.ActionAcademicUnitManage, metadata,
		); appErr != nil {
			return nil, appErr
		}
	}
	return updateAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"PatchClass", "class", candidate.Auditable(), current.Auditable(),
		func() (*model.Class, error) { return a.Store().Class().Update(ctx, &candidate) },
	)
}

func (a *App) ArchiveClass(ctx context.Context, principal model.Principal, metadata model.RequestMetadata, id string) *model.AppError {
	current, err := a.Store().Class().Get(ctx, id)
	if err != nil {
		return administrationError("ArchiveClass.get", "class", err)
	}
	unitID, err := a.Store().Class().GetAcademicUnitId(ctx, id)
	if err != nil {
		return administrationError("ArchiveClass.unit", "academic_unit", err)
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, unitID, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return appErr
	}
	return archiveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"ArchiveClass", "class", id, current.Auditable(),
		func(at int64) (*model.Class, error) { return a.Store().Class().Delete(ctx, id, at) },
	)
}

func normalizeAdministrationLimit(limit int) int {
	if limit == 0 {
		return defaultAdministrationListLimit
	}
	return limit
}

func mutationAction(resource model.Resource) model.Action {
	if resource.Type == model.ResourceInstitution {
		return model.ActionInstitutionManage
	}
	return model.ActionAcademicUnitManage
}

func (a *App) beginAdministrationMutation(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
	operation string,
	value any,
	prior any,
) (*model.AuditEvent, *model.AppError) {
	return a.audit.BeginCriticalAction(
		ctx, principal, action, resource, metadata,
		map[string]any{"operation": operation, "value": value}, prior,
	)
}

func (a *App) completeAdministrationMutation(ctx context.Context, auditID string, result model.Auditable) *model.AppError {
	_, appErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusSuccess, "", result.Auditable(),
	)
	return appErr
}

func (a *App) failAdministrationMutation(ctx context.Context, auditID, where, resource string, err error) *model.AppError {
	mapped := administrationError(where, resource, err)
	if _, auditErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, mapped.ErrorCode(), nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

func administrationError(where, resource string, err error) *model.AppError {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	status, code := http.StatusInternalServerError, "administration.unavailable"
	switch {
	case store.IsNotFound(err):
		status, code = http.StatusNotFound, "resource.not_found"
	case store.IsConflict(err):
		status, code = http.StatusConflict, resource+".conflict"
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "users_last_system_admin" {
			code = "user.last_system_admin"
		}
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			status, code = http.StatusBadRequest, resource+".invalid"
		}
	}
	return model.NewAppError(where, code, nil, "", status).
		WithSafeFields(map[string]string{"resource": resource}).
		Wrap(err)
}

func saveAcademicEntity[T model.Auditable](
	a *App,
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
	where string,
	entity string,
	auditable map[string]any,
	save func() (T, error),
) (T, *model.AppError) {
	var zero T
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, action, resource, metadata, "create", auditable, nil,
	)
	if appErr != nil {
		return zero, appErr
	}
	saved, err := save()
	if err != nil {
		return zero, a.failAdministrationMutation(ctx, attempt.Id, where, entity, err)
	}
	if appErr := a.completeAdministrationMutation(ctx, attempt.Id, saved); appErr != nil {
		return zero, appErr
	}
	return saved, nil
}

func updateAcademicEntity[T model.Auditable](
	a *App,
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
	where string,
	entity string,
	auditable map[string]any,
	prior map[string]any,
	update func() (T, error),
) (T, *model.AppError) {
	var zero T
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, action, resource, metadata, "patch", auditable, prior,
	)
	if appErr != nil {
		return zero, appErr
	}
	updated, err := update()
	if err != nil {
		return zero, a.failAdministrationMutation(ctx, attempt.Id, where, entity, err)
	}
	if appErr := a.completeAdministrationMutation(ctx, attempt.Id, updated); appErr != nil {
		return zero, appErr
	}
	return updated, nil
}

func archiveAcademicEntity[T model.Auditable](
	a *App,
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
	where string,
	entity string,
	id string,
	prior map[string]any,
	archive func(int64) (T, error),
) *model.AppError {
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, action, resource, metadata,
		"archive", map[string]any{"id": id}, prior,
	)
	if appErr != nil {
		return appErr
	}
	archived, err := archive(time.Now().UnixMilli())
	if err != nil {
		return a.failAdministrationMutation(ctx, attempt.Id, where, entity, err)
	}
	return a.completeAdministrationMutation(ctx, attempt.Id, archived)
}
