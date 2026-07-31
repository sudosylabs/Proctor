// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost's application-owned team/channel membership flow.
// Proctor separates organizational membership, scoped roles, and student
// enrollment, and retains effective-dated history instead of deleting rows.

package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func (a *App) ListAffiliations(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
) ([]*model.Affiliation, *model.AppError) {
	if _, appErr := a.authorizePrincipalToUser(
		ctx, principal, userID, model.ActionUserManage, metadata,
	); appErr != nil {
		return nil, appErr
	}
	affiliations, err := a.Store().Affiliation().ListByUser(ctx, userID)
	if err != nil {
		return nil, administrationError("ListAffiliations", "affiliation", err)
	}
	return affiliations, nil
}

func (a *App) CreateAffiliation(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	affiliation *model.Affiliation,
) (*model.Affiliation, *model.AppError) {
	if affiliation == nil {
		return nil, invalidAdministrationRequest("CreateAffiliation", "affiliation")
	}
	resource, appErr := a.authorizePrincipalToUser(
		ctx, principal, affiliation.UserId, model.ActionUserManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	candidate := *affiliation
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionUserManage, resource,
		"CreateAffiliation", "affiliation", candidate.Auditable(),
		func() (*model.Affiliation, error) { return a.Store().Affiliation().Save(ctx, &candidate) },
	)
}

func (a *App) EndAffiliation(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	id string,
) (*model.Affiliation, *model.AppError) {
	current, err := a.Store().Affiliation().Get(ctx, id)
	if err != nil {
		return nil, administrationError("EndAffiliation.get", "affiliation", err)
	}
	resource, appErr := a.authorizePrincipalToUser(
		ctx, principal, current.UserId, model.ActionUserManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	if current.Kind == model.AffiliationStudent {
		enrollments, err := a.Store().ClassMember().ListByUser(ctx, current.UserId)
		if err != nil {
			return nil, administrationError(
				"EndAffiliation.enrollments", "class_member", err,
			)
		}
		for _, enrollment := range enrollments {
			if enrollment.EndAt == 0 {
				return nil, model.NewAppError(
					"EndAffiliation",
					"affiliation.student_has_active_enrollment",
					nil,
					"",
					http.StatusConflict,
				)
			}
		}
	}
	return endMembership(
		a, ctx, principal, metadata, model.ActionUserManage, resource,
		"EndAffiliation", "affiliation", current,
		func(at int64) (*model.Affiliation, error) { return a.Store().Affiliation().End(ctx, id, at) },
	)
}

func (a *App) ListAcademicUnitMembers(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	academicUnitID string,
	activeAt int64,
) ([]*model.AcademicUnitMember, *model.AppError) {
	if _, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, academicUnitID, model.ActionAcademicUnitManage, metadata,
	); appErr != nil {
		return nil, appErr
	}
	members, err := a.Store().AcademicUnitMember().ListByAcademicUnit(
		ctx, academicUnitID, activeAt,
	)
	if err != nil {
		return nil, administrationError(
			"ListAcademicUnitMembers", "academic_unit_member", err,
		)
	}
	return members, nil
}

func (a *App) CreateAcademicUnitMember(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	member *model.AcademicUnitMember,
) (*model.AcademicUnitMember, *model.AppError) {
	if member == nil {
		return nil, invalidAdministrationRequest(
			"CreateAcademicUnitMember", "academic_unit_member",
		)
	}
	candidate := *member
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	candidate.EndAt = 0
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, candidate.AcademicUnitId, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return saveAcademicEntity(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"CreateAcademicUnitMember", "academic_unit_member", candidate.Auditable(),
		func() (*model.AcademicUnitMember, error) {
			return a.Store().AcademicUnitMember().Save(ctx, &candidate)
		},
	)
}

func (a *App) EndAcademicUnitMember(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	id string,
) (*model.AcademicUnitMember, *model.AppError) {
	current, err := a.Store().AcademicUnitMember().Get(ctx, id)
	if err != nil {
		return nil, administrationError(
			"EndAcademicUnitMember.get", "academic_unit_member", err,
		)
	}
	resource, appErr := a.authorizePrincipalToAcademicUnit(
		ctx, principal, current.AcademicUnitId, model.ActionAcademicUnitManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return endMembership(
		a, ctx, principal, metadata, model.ActionAcademicUnitManage, resource,
		"EndAcademicUnitMember", "academic_unit_member", current,
		func(at int64) (*model.AcademicUnitMember, error) {
			return a.Store().AcademicUnitMember().End(ctx, id, at)
		},
	)
}

func (a *App) ListClassMembers(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	classID string,
	activeAt int64,
) ([]*model.ClassMember, *model.AppError) {
	if _, appErr := a.authorizePrincipalToClass(
		ctx, principal, classID, model.ActionClassMembersView, metadata,
	); appErr != nil {
		return nil, appErr
	}
	members, err := a.Store().ClassMember().ListByClass(ctx, classID, activeAt)
	if err != nil {
		return nil, administrationError("ListClassMembers", "class_member", err)
	}
	return members, nil
}

// EnrollClassMember also performs transfers. The store serializes enrollment
// changes per user and academic period, closes any previous active membership,
// and returns both rows for a complete audit trail.
func (a *App) EnrollClassMember(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	member *model.ClassMember,
) (*model.ClassEnrollment, *model.AppError) {
	if member == nil {
		return nil, invalidAdministrationRequest("EnrollClassMember", "class_member")
	}
	candidate := *member
	candidate.Id, candidate.CreateAt, candidate.UpdateAt, candidate.DeleteAt = "", 0, 0, 0
	resource, appErr := a.authorizePrincipalToClass(
		ctx, principal, candidate.ClassId, model.ActionClassMembersManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	effectiveAt := candidate.StartAt
	if effectiveAt == 0 {
		effectiveAt = time.Now().UnixMilli()
	}
	affiliations, err := a.Store().Affiliation().ListActiveByUser(
		ctx, candidate.UserId, effectiveAt,
	)
	if err != nil {
		return nil, administrationError(
			"EnrollClassMember.affiliations", "affiliation", err,
		)
	}
	isStudent := false
	for _, affiliation := range affiliations {
		if affiliation.Kind == model.AffiliationStudent && affiliation.EndAt == 0 {
			isStudent = true
			break
		}
	}
	if !isStudent {
		return nil, model.NewAppError(
			"EnrollClassMember",
			"class_member.student_affiliation_required",
			nil,
			"",
			http.StatusConflict,
		)
	}
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, model.ActionClassMembersManage, resource, metadata,
		"enroll", candidate.Auditable(), nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	result, err := a.Store().ClassMember().Enroll(ctx, &candidate)
	if err != nil {
		return nil, a.failAdministrationMutation(
			ctx, attempt.Id, "EnrollClassMember", "class_member", err,
		)
	}
	enrollment := &model.ClassEnrollment{
		Membership: result.Membership,
		Previous:   result.Previous,
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", enrollment.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return enrollment, nil
}

func (a *App) EndClassMember(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	id string,
) (*model.ClassMember, *model.AppError) {
	current, err := a.Store().ClassMember().Get(ctx, id)
	if err != nil {
		return nil, administrationError("EndClassMember.get", "class_member", err)
	}
	resource, appErr := a.authorizePrincipalToClass(
		ctx, principal, current.ClassId, model.ActionClassMembersManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	return endMembership(
		a, ctx, principal, metadata, model.ActionClassMembersManage, resource,
		"EndClassMember", "class_member", current,
		func(at int64) (*model.ClassMember, error) { return a.Store().ClassMember().End(ctx, id, at) },
	)
}

func endMembership[T model.Auditable](
	a *App,
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
	where string,
	entity string,
	current T,
	end func(int64) (T, error),
) (T, *model.AppError) {
	var zero T
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, action, resource, metadata,
		"end", nil, current.Auditable(),
	)
	if appErr != nil {
		return zero, appErr
	}
	ended, err := end(time.Now().UnixMilli())
	if err != nil {
		return zero, a.failAdministrationMutation(ctx, attempt.Id, where, entity, err)
	}
	if appErr := a.completeAdministrationMutation(ctx, attempt.Id, ended); appErr != nil {
		return zero, appErr
	}
	return ended, nil
}
