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
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func (a *App) authorizePrincipalToSystem(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	metadata model.RequestMetadata,
) (model.Resource, error) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, authorizationResourceError("institution", err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if appErr := a.AuthorizePrincipalToInstitution(
		ctx,
		principal,
		institution.ID.String(),
		action,
		metadata,
	); appErr != nil {
		return model.Resource{}, appErr
	}
	return resource, nil
}

// PrincipalHasPermissionTo is the reusable, non-auditing permission predicate.
// Application use cases call AuthorizePrincipalTo at their security boundary
// when the decision must be durably recorded.
func (a *App) PrincipalHasPermissionTo(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, error) {
	return a.authorization.Can(ctx, principal, action, resource)
}

func (a *App) PrincipalHasPermissionToInstitution(
	ctx context.Context,
	principal model.Principal,
	institutionID string,
	action model.Action,
) (bool, error) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceInstitution, ID: institutionID},
	)
}

func (a *App) PrincipalHasPermissionToAcademicUnit(
	ctx context.Context,
	principal model.Principal,
	academicUnitID string,
	action model.Action,
) (bool, error) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceAcademicUnit, ID: academicUnitID},
	)
}

func (a *App) PrincipalHasPermissionToClass(
	ctx context.Context,
	principal model.Principal,
	classID string,
	action model.Action,
) (bool, error) {
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceClass, ID: classID},
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
) error {
	return a.authorization.Authorize(ctx, principal, action, resource, metadata)
}

func (a *App) AuthorizePrincipalToInstitution(
	ctx context.Context,
	principal model.Principal,
	institutionID string,
	action model.Action,
	metadata model.RequestMetadata,
) error {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceInstitution, ID: institutionID},
		metadata,
	)
}

func (a *App) AuthorizePrincipalToAcademicUnit(
	ctx context.Context,
	principal model.Principal,
	academicUnitID string,
	action model.Action,
	metadata model.RequestMetadata,
) error {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceAcademicUnit, ID: academicUnitID},
		metadata,
	)
}

func (a *App) authorizePrincipalToAcademicUnit(
	ctx context.Context,
	principal model.Principal,
	academicUnitID string,
	action model.Action,
	metadata model.RequestMetadata,
) (model.Resource, error) {
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: academicUnitID}
	if appErr := a.AuthorizePrincipalTo(
		ctx, principal, action, resource, metadata,
	); appErr != nil {
		return model.Resource{}, appErr
	}
	return resource, nil
}

func (a *App) AuthorizePrincipalToClass(
	ctx context.Context,
	principal model.Principal,
	classID string,
	action model.Action,
	metadata model.RequestMetadata,
) error {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceClass, ID: classID},
		metadata,
	)
}

func (a *App) authorizePrincipalToClass(
	ctx context.Context,
	principal model.Principal,
	classID string,
	action model.Action,
	metadata model.RequestMetadata,
) (model.Resource, error) {
	resource := model.Resource{Type: model.ResourceClass, ID: classID}
	if appErr := a.AuthorizePrincipalTo(
		ctx, principal, action, resource, metadata,
	); appErr != nil {
		return model.Resource{}, appErr
	}
	return resource, nil
}

func (a *App) AuthorizePrincipalToUser(
	ctx context.Context,
	principal model.Principal,
	userID string,
	action model.Action,
	metadata model.RequestMetadata,
) error {
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceUser, ID: userID},
		metadata,
	)
}

func (a *App) authorizePrincipalToUser(
	ctx context.Context,
	principal model.Principal,
	userID string,
	action model.Action,
	metadata model.RequestMetadata,
) (model.Resource, error) {
	resource := model.Resource{Type: model.ResourceUser, ID: userID}
	if appErr := a.AuthorizePrincipalTo(
		ctx, principal, action, resource, metadata,
	); appErr != nil {
		return model.Resource{}, appErr
	}
	return resource, nil
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
) (bool, error) {
	if principal.Validate() != nil {
		return false, invalidTokenAppError()
	}
	if !model.IsValidId(userID) {
		return false, nil
	}
	if (action == model.ActionUserView || action == model.ActionUserProfilePictureManage) && principal.UserID.String() == userID {
		return true, nil
	}
	if action == model.ActionUserView {
		resource, appErr := a.userVisibilityPermission(ctx, principal, userID)
		return resource.Type != "", appErr
	}
	return a.PrincipalHasPermissionTo(
		ctx,
		principal,
		action,
		model.Resource{Type: model.ResourceUser, ID: userID},
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
) (bool, error) {
	return a.PrincipalHasPermissionToUser(
		ctx,
		principal,
		otherUserID,
		model.ActionUserView,
	)
}

func (a *App) userVisibilityPermission(
	ctx context.Context,
	principal model.Principal,
	otherUserID string,
) (model.Resource, error) {
	userResource := model.Resource{Type: model.ResourceUser, ID: otherUserID}
	allowed, appErr := a.PrincipalHasPermissionTo(
		ctx, principal, model.ActionUserView, userResource,
	)
	if appErr != nil || allowed {
		if allowed {
			return userResource, nil
		}
		return model.Resource{}, appErr
	}
	memberships, err := a.Store().ClassMember().ListActiveByUser(
		ctx, otherUserID, time.Now().UnixMilli(),
	)
	if err != nil {
		return model.Resource{}, administrationError(
			"userVisibilityPermission", "class_member", err,
		)
	}
	for _, membership := range memberships {
		classResource := model.Resource{Type: model.ResourceClass, ID: membership.ClassID.String()}
		allowed, appErr = a.PrincipalHasPermissionTo(
			ctx, principal, model.ActionClassMembersView, classResource,
		)
		if appErr != nil {
			return model.Resource{}, appErr
		}
		if allowed {
			return classResource, nil
		}
	}
	return model.Resource{}, nil
}

// GetUserForPrincipal is the transport-safe user read use case. Self-access is
// allowed directly; cross-user reads require and durably audit user.view.
func (a *App) GetUserForPrincipal(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
) (*model.User, error) {
	if principal.Validate() != nil {
		return nil, invalidTokenAppError()
	}
	if principal.UserID.String() != userID {
		if appErr := a.authorizeUserVisibility(
			ctx, principal, userID, metadata,
		); appErr != nil {
			return nil, appErr
		}
	}
	user, err := a.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (a *App) authorizeUserVisibility(
	ctx context.Context,
	principal model.Principal,
	userID string,
	metadata model.RequestMetadata,
) error {
	resource, appErr := a.userVisibilityPermission(ctx, principal, userID)
	if appErr != nil {
		return appErr
	}
	switch resource.Type {
	case model.ResourceUser:
		return a.AuthorizePrincipalToUser(
			ctx, principal, userID, model.ActionUserView, metadata,
		)
	case model.ResourceClass:
		return a.AuthorizePrincipalToClass(
			ctx, principal, resource.ID, model.ActionClassMembersView, metadata,
		)
	default:
		// Record the final denial against the resource the caller attempted to
		// see, rather than leaking which academic relationship was absent.
		return a.AuthorizePrincipalToUser(
			ctx, principal, userID, model.ActionUserView, metadata,
		)
	}
}
