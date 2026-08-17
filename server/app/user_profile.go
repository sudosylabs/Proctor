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
	AuthorizeSearch(context.Context, Invocation) (store.UserVisibilityScope, error)
	AuthorizeProfileRead(context.Context, Invocation, string) (bool, error)
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
	visibility, err := s.authorization.AuthorizeSearch(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = defaultAdministrationListLimit
	}
	includeDisabled := query.IncludeDisabled && visibility.InstitutionWide
	users, err := s.users.List(ctx, store.UserListOptions{Query: query.Query, AfterUsername: query.AfterUsername, AfterId: query.AfterID, Limit: query.Limit, IncludeDisabled: includeDisabled, Visibility: visibility})
	if err != nil {
		return nil, userProfileError(err)
	}
	if users == nil {
		users = []*model.User{}
	}
	if !visibility.InstitutionWide {
		users = scopedUserDirectoryProjection(users)
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
	fullProfile, err := s.authorization.AuthorizeProfileRead(ctx, invocation, id)
	if err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, userProfileError(err)
	}
	if fullProfile {
		return user, nil
	}
	return scopedUserProjection(user), nil
}

func scopedUserDirectoryProjection(users []*model.User) []*model.User {
	result := make([]*model.User, 0, len(users))
	for _, user := range users {
		result = append(result, scopedUserProjection(user))
	}
	return result
}

// scopedUserProjection retains only the public directory identity required to
// identify a person in academic administration. Account/security state and
// client-local presentation preferences remain institution/self-only.
func scopedUserProjection(user *model.User) *model.User {
	if user == nil {
		return nil
	}
	projected := *user
	projected.Email = ""
	projected.EmailVerified = false
	projected.Locale = ""
	projected.Timezone = ""
	projected.LastLoginAt = model.OptionalTime{}
	projected.LastActivityAt = model.OptionalTime{}
	projected.DisabledAt = model.OptionalTime{}
	projected.DefaultProfilePictureSeed = ""
	projected.DefaultProfilePictureFileID = model.FileEntryID("")
	projected.CustomProfilePictureFileID = model.FileEntryID("")
	return &projected
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
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   resource,
			Operation:  "update_profile",
			Value:      candidate.Auditable(),
			Prior:      current.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.User, error) {
			return s.users.UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
				User: &candidate, ExpectedRevision: expectedRevision,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		userProfileError,
	)
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
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a userProfileAuthorization) AuthorizeSearch(ctx context.Context, invocation Invocation) (store.UserVisibilityScope, error) {
	return a.authorization.authorizeUserSearch(ctx, invocation)
}

func (a userProfileAuthorization) AuthorizeProfileRead(ctx context.Context, invocation Invocation, userID string) (bool, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return false, invalidTokenAppError()
	}
	if principal.UserID.String() == userID {
		return true, nil
	}
	return a.authorization.authorizeUserRead(ctx, invocation, userID)
}

func (a userProfileAuthorization) AuthorizeRead(ctx context.Context, invocation Invocation, userID string) error {
	_, err := a.AuthorizeProfileRead(ctx, invocation, userID)
	return err
}

func (a userProfileAuthorization) AuthorizeManage(ctx context.Context, invocation Invocation, userID string) error {
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: userID}, invocation.RequestMetadata())
}

func (a userProfileAuthorization) AuthorizeProfilePictureWrite(ctx context.Context, invocation Invocation, userID string) error {
	action := model.ActionUserManage
	if invocation.Principal().UserID.String() == userID {
		action = model.ActionUserProfilePictureManage
	}
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, model.Resource{Type: model.ResourceUser, ID: userID}, invocation.RequestMetadata())
}
