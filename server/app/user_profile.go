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

type SearchUsersQuery struct {
	Query           string
	AfterUsername   string
	AfterID         string
	Limit           int
	IncludeDisabled bool
}

type GetUserProfileQuery struct {
	ID string
}

type UpdateUserProfileCommand struct {
	ID            string
	Username      *string
	Email         *string
	EmailVerified *bool
	DisplayName   *string
	FirstName     *string
	LastName      *string
	Locale        *string
	Timezone      *string
}

type userProfileStore interface {
	Get(context.Context, string) (*model.User, error)
	List(context.Context, store.UserListOptions) ([]*model.User, error)
	UpdateProfileWithAudit(context.Context, *store.UserProfileUpdate) (*model.User, error)
}

type userProfileAuthorizer interface {
	AuthorizeSearch(context.Context, Invocation) error
	AuthorizeRead(context.Context, Invocation, string) error
	AuthorizeManage(context.Context, Invocation, string) error
}

type userProfileService struct {
	users         userProfileStore
	authorization userProfileAuthorizer
	audit         mutationAuditor
	now           func() time.Time
}

func newUserProfileService(users userProfileStore, authorization userProfileAuthorizer, audit mutationAuditor, now func() time.Time) *userProfileService {
	return &userProfileService{users: users, authorization: authorization, audit: audit, now: now}
}

func (a *App) SearchUsers(ctx context.Context, invocation Invocation, query SearchUsersQuery) ([]*model.User, error) {
	return a.userProfiles.Search(ctx, invocation, query)
}

func (s *userProfileService) Search(ctx context.Context, invocation Invocation, query SearchUsersQuery) ([]*model.User, error) {
	if err := s.authorization.AuthorizeSearch(ctx, invocation); err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = defaultAdministrationListLimit
	}
	users, err := s.users.List(ctx, store.UserListOptions{Query: query.Query, AfterUsername: query.AfterUsername, AfterId: query.AfterID, Limit: query.Limit, IncludeDisabled: query.IncludeDisabled})
	if err != nil {
		return nil, userProfileError(err)
	}
	if users == nil {
		users = []*model.User{}
	}
	return users, nil
}

func (a *App) GetUserProfile(ctx context.Context, invocation Invocation, query GetUserProfileQuery) (*model.User, error) {
	return a.userProfiles.Get(ctx, invocation, query)
}

func (s *userProfileService) Get(ctx context.Context, invocation Invocation, query GetUserProfileQuery) (*model.User, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeRead(ctx, invocation, id); err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, userProfileError(err)
	}
	return user, nil
}

func (a *App) UpdateUserProfile(ctx context.Context, invocation Invocation, command UpdateUserProfileCommand) (*model.User, error) {
	return a.userProfiles.Update(ctx, invocation, command)
}

func (s *userProfileService) Update(ctx context.Context, invocation Invocation, command UpdateUserProfileCommand) (*model.User, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, id); err != nil {
		return nil, err
	}
	current, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, userProfileError(err)
	}
	candidate := *current
	expectedRevision := current.Revision
	candidate.Patch(&model.UserPatch{Username: command.Username, Email: command.Email, EmailVerified: command.EmailVerified, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName, Locale: command.Locale, Timezone: command.Timezone})
	at := s.now()
	candidate.PrepareUpdate(at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("user.invalid", err)
	}
	resource := model.Resource{Type: model.ResourceUser, ID: id}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionUserManage, resource, "update_profile", candidate.Auditable(), current.Auditable())
	if err != nil {
		return nil, err
	}
	updated, err := s.users.UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{User: &candidate, ExpectedRevision: expectedRevision, AuditEventID: auditID, AuditAt: at.UnixMilli()})
	if err != nil {
		mapped := userProfileError(err)
		failure, _ := As(mapped)
		if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	return updated, nil
}

func userProfileError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "user").Wrap(err)
	case store.IsConflict(err):
		return NewError("user.conflict").WithField("resource", "user").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("user.invalid").WithField("resource", "user").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "user").Wrap(err)
	}
}

type userProfileAuthorization struct {
	authorization *AuthorizationService
	institutions  store.InstitutionStore
	classMembers  store.ClassMemberStore
	now           func() time.Time
}

func (a userProfileAuthorization) AuthorizeSearch(ctx context.Context, invocation Invocation) error {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return userProfileError(err)
	}
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), model.ActionInstitutionManage, model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, invocation.RequestMetadata())
}

func (a userProfileAuthorization) AuthorizeRead(ctx context.Context, invocation Invocation, userID string) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	if principal.UserID.String() == userID {
		return nil
	}
	userResource := model.Resource{Type: model.ResourceUser, ID: userID}
	allowed, appErr := a.authorization.Can(ctx, principal, model.ActionUserView, userResource)
	if appErr != nil {
		return appErr
	}
	if allowed {
		return a.authorization.authorizeCurrentState(ctx, principal, model.ActionUserView, userResource, invocation.RequestMetadata())
	}
	memberships, err := a.classMembers.ListActiveByUser(ctx, userID, a.now().UnixMilli())
	if err != nil {
		return userProfileError(err)
	}
	for _, membership := range memberships {
		resource := model.Resource{Type: model.ResourceClass, ID: membership.ClassID.String()}
		allowed, appErr = a.authorization.Can(ctx, principal, model.ActionClassMembersView, resource)
		if appErr != nil {
			return appErr
		}
		if allowed {
			return a.authorization.authorizeUserViewThroughClass(ctx, principal, userResource, resource, invocation.RequestMetadata())
		}
	}
	return a.authorization.authorizeCurrentState(ctx, principal, model.ActionUserView, userResource, invocation.RequestMetadata())
}

func (a userProfileAuthorization) AuthorizeManage(ctx context.Context, invocation Invocation, userID string) error {
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: userID}, invocation.RequestMetadata())
}
