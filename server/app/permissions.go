// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/authorization.go. Proctor uses an
// immutable principal and resolves current role bindings from PostgreSQL
// instead of trusting permission or role snapshots carried by a session.

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

// PrincipalHasPermissionTo is the reusable, non-auditing permission predicate.
// Application use cases call AuthorizePrincipalTo at their security boundary
// when the decision must be durably recorded.
func (a *App) PrincipalHasPermissionTo(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, *model.AppError) {
	return a.authorization.Can(ctx, principal, action, resource)
}

func (a *App) PrincipalHasPermissionToInstitution(
	ctx context.Context,
	principal model.Principal,
	institutionID string,
	action model.Action,
) (bool, *model.AppError) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceInstitution, Id: institutionID},
	)
}

func (a *App) PrincipalHasPermissionToAcademicUnit(
	ctx context.Context,
	principal model.Principal,
	academicUnitID string,
	action model.Action,
) (bool, *model.AppError) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceAcademicUnit, Id: academicUnitID},
	)
}

func (a *App) PrincipalHasPermissionToClass(
	ctx context.Context,
	principal model.Principal,
	classID string,
	action model.Action,
) (bool, *model.AppError) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceClass, Id: classID},
	)
}

// AuthorizePrincipalTo records the allow/deny decision durably and fails
// closed if either permission resolution or audit persistence fails.
func (a *App) AuthorizePrincipalTo(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) *model.AppError {
	return a.authorization.Authorize(ctx, principal, action, resource, metadata)
}

func (a *App) AuthorizePrincipalToInstitution(
	ctx context.Context,
	principal model.Principal,
	institutionID string,
	action model.Action,
	metadata model.RequestMetadata,
) *model.AppError {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceInstitution, Id: institutionID},
		metadata,
	)
}

func (a *App) AuthorizePrincipalToAcademicUnit(
	ctx context.Context,
	principal model.Principal,
	academicUnitID string,
	action model.Action,
	metadata model.RequestMetadata,
) *model.AppError {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceAcademicUnit, Id: academicUnitID},
		metadata,
	)
}

func (a *App) AuthorizePrincipalToClass(
	ctx context.Context,
	principal model.Principal,
	classID string,
	action model.Action,
	metadata model.RequestMetadata,
) *model.AppError {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceClass, Id: classID},
		metadata,
	)
}

func (a *App) AuthorizePrincipalToUser(
	ctx context.Context,
	principal model.Principal,
	userID string,
	action model.Action,
	metadata model.RequestMetadata,
) *model.AppError {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceUser, Id: userID},
		metadata,
	)
}

// PrincipalHasPermissionToUser combines self-access with institution-wide user
// permissions. Academic-unit and class relationship rules will be added here
// when their membership stores are implemented; until then cross-user access
// is denied unless an institution-scoped role explicitly grants it.
func (a *App) PrincipalHasPermissionToUser(
	ctx context.Context,
	principal model.Principal,
	userID string,
	action model.Action,
) (bool, *model.AppError) {
	if !principal.IsValid() {
		return false, invalidTokenError("PrincipalHasPermissionToUser")
	}
	if !model.IsValidId(userID) {
		return false, nil
	}
	if action == model.ActionUserView && principal.UserId == userID {
		return true, nil
	}
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceUser, Id: userID},
	)
}

// UserCanSeeOtherUser is a contextual visibility policy, not merely a route
// authentication check. It is deliberately default-deny until Proctor has
// durable academic membership data from which teacher/student visibility can
// be resolved safely.
func (a *App) UserCanSeeOtherUser(
	ctx context.Context,
	principal model.Principal,
	otherUserID string,
) (bool, *model.AppError) {
	return a.PrincipalHasPermissionToUser(
		ctx,
		principal,
		otherUserID,
		model.ActionUserView,
	)
}

// GetUserForPrincipal is the transport-safe user read use case. Self-access is
// allowed directly; cross-user reads require and durably audit user.view.
func (a *App) GetUserForPrincipal(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
) (*model.User, *model.AppError) {
	if !principal.IsValid() {
		return nil, invalidTokenError("GetUserForPrincipal")
	}
	if principal.UserId != userID {
		if appErr := a.AuthorizePrincipalToUser(
			ctx,
			principal,
			userID,
			model.ActionUserView,
			metadata,
		); appErr != nil {
			return nil, appErr
		}
	}
	return a.GetUser(ctx, userID)
}
