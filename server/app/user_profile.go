// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
	ID model.UserID
}

type UpdateUserProfileCommand struct {
	ID          model.UserID
	Username    *string
	DisplayName *string
	FirstName   *string
	LastName    *string
	Locale      *string
	Timezone    *string
}

type userProfileStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
	List(context.Context, store.UserListOptions) ([]*model.User, error)
	GetCurrentContext(context.Context, model.UserID, int) (*store.CurrentUserContext, error)
	UpdateProfile(context.Context, *store.UserProfileUpdate) (*model.User, error)
}

type CurrentUserContextView struct {
	UserID                       model.UserID
	Username                     string
	DisplayName                  string
	ProfilePictureReference      string
	NoCurrentAffiliation         bool
	NoAssignedAccess             bool
	AvailableProductAreas        []store.CurrentUserProductArea
	ManagementScopes             []store.CurrentUserManagementScope
	ManagementScopesHasMore      bool
	UnresolvedAttempt            *store.CurrentUserAttemptSelector
	SessionManagementAvailable   bool
	CurrentDesktopRegistrationID model.DesktopRegistrationID
}

func (s *userProfileService) GetCurrentUserContext(ctx context.Context, invocation invocation.Invocation) (*CurrentUserContextView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, apperror.New("authentication.token_invalid")
	}
	value, err := s.deps.Users.GetCurrentContext(ctx, principal.UserID, 21)
	if err != nil {
		return nil, userStoreErrors.Translate(err)
	}
	if value == nil || value.UserID != principal.UserID || value.Username == "" || value.DisplayName == "" ||
		len(value.AvailableProductAreas) < 2 || len(value.ManagementScopes) > 20 {
		return nil, apperror.New("administration.unavailable").Wrap(errors.New("current User context projection is incomplete"))
	}
	view := &CurrentUserContextView{UserID: value.UserID, Username: value.Username, DisplayName: value.DisplayName,
		ProfilePictureReference: "/api/v1/users/" + value.UserID.String() + "/profile-picture",
		NoCurrentAffiliation:    value.NoCurrentAffiliation, NoAssignedAccess: value.NoAssignedAccess,
		AvailableProductAreas:   append([]store.CurrentUserProductArea(nil), value.AvailableProductAreas...),
		ManagementScopes:        append([]store.CurrentUserManagementScope(nil), value.ManagementScopes...),
		ManagementScopesHasMore: value.ManagementScopesHasMore, SessionManagementAvailable: true}
	if value.UnresolvedAttempt != nil {
		attempt := *value.UnresolvedAttempt
		view.UnresolvedAttempt = &attempt
	}
	if principal.ClientType == model.SessionClientDesktop {
		view.CurrentDesktopRegistrationID = principal.DesktopRegistrationID
	}
	return view, nil
}

type userProfileAuthorizer interface {
	AuthorizeSearch(context.Context, invocation.Invocation) (store.UserVisibilityScope, error)
	// AuthorizeProfileRead reports fullProfile when the caller reads its own
	// profile or holds institution-wide User visibility; a scoped grant reads
	// the limited profile.
	AuthorizeProfileRead(context.Context, invocation.Invocation, string) (fullProfile bool, err error)
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
}

// userProfileServiceDependencies are the collaborators of userProfileService;
// newUserProfileService validates them once and userProfileService keeps them in deps.
type userProfileServiceDependencies struct {
	Users         userProfileStore
	Authorization userProfileAuthorizer
	Audit         mutationAuditor
	Now           func() time.Time
}

type userProfileService struct {
	deps userProfileServiceDependencies
}

func newUserProfileService(deps userProfileServiceDependencies) *userProfileService {
	return &userProfileService{deps: deps}
}

func (s *userProfileService) SearchUsers(ctx context.Context, invocation invocation.Invocation, query SearchUsersQuery) (Page[*model.User], error) {
	limit, probe, err := resolvePageLimit(query.Limit, "limit")
	if err != nil {
		return Page[*model.User]{}, err
	}
	visibility, err := s.deps.Authorization.AuthorizeSearch(ctx, invocation)
	if err != nil {
		return Page[*model.User]{}, err
	}
	includeDisabled := query.IncludeDisabled && visibility.InstitutionWide
	users, err := s.deps.Users.List(ctx, store.UserListOptions{Query: query.Query, AfterUsername: query.AfterUsername, AfterID: model.UserID(query.AfterID), Limit: probe, IncludeDisabled: includeDisabled, Visibility: visibility})
	if err != nil {
		return Page[*model.User]{}, userStoreErrors.Translate(err)
	}
	page := newPage(users, limit)
	if !visibility.InstitutionWide {
		page.Items = scopedUserDirectoryProjection(page.Items)
	}
	return page, nil
}

func (s *userProfileService) GetUserProfile(ctx context.Context, invocation invocation.Invocation, query GetUserProfileQuery) (*model.User, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	fullProfile, err := s.deps.Authorization.AuthorizeProfileRead(ctx, invocation, id.String())
	if err != nil {
		return nil, err
	}
	user, err := s.deps.Users.Get(ctx, id)
	if err != nil {
		return nil, userStoreErrors.Translate(err)
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

func (s *userProfileService) UpdateUserProfile(ctx context.Context, invocation invocation.Invocation, command UpdateUserProfileCommand) (*model.User, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionUserManage, model.Resource{Type: model.ResourceUser, ID: id.String()}); err != nil {
		return nil, err
	}
	current, err := s.deps.Users.Get(ctx, id)
	if err != nil {
		return nil, userStoreErrors.Translate(err)
	}
	changes := model.UserProfileChanges{Username: command.Username, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName, Locale: command.Locale, Timezone: command.Timezone}
	candidate := *current
	expectedRevision := current.Revision
	candidate.ApplyProfileChanges(&changes)
	at := s.deps.Now()
	candidate.PrepareUpdate(at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("user.invalid", err)
	}
	resource := model.Resource{Type: model.ResourceUser, ID: id.String()}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
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
			return s.deps.Users.UpdateProfile(ctx, &store.UserProfileUpdate{
				UserID: current.ID, Changes: changes, ExpectedRevision: expectedRevision,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		userStoreErrors.Translate,
	)
}

var userStoreErrors = apperror.AdministrationStoreTable("user", "user.conflict", "user.invalid", nil)

type userProfileAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a userProfileAuthorization) AuthorizeSearch(ctx context.Context, invocation invocation.Invocation) (store.UserVisibilityScope, error) {
	return a.authorization.authorizeUserSearch(ctx, invocation)
}

func (a userProfileAuthorization) AuthorizeProfileRead(ctx context.Context, invocation invocation.Invocation, userID string) (fullProfile bool, err error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return false, invalidTokenAppError()
	}
	if principal.UserID.String() == userID {
		return true, nil
	}
	return a.authorization.authorizeUserRead(ctx, invocation, userID)
}

func (a userProfileAuthorization) AuthorizeRead(ctx context.Context, invocation invocation.Invocation, userID string) error {
	_, err := a.AuthorizeProfileRead(ctx, invocation, userID)
	return err
}

func (a userProfileAuthorization) Authorize(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource) error {
	return a.authorization.Authorize(ctx, invocation, action, resource)
}

// AuthorizeAccountStateManage keeps disabled accounts addressable by the one
// operation whose purpose includes re-enabling them. Other User operations
// retain the ordinary active-resource visibility rule.
func (a userProfileAuthorization) AuthorizeAccountStateManage(ctx context.Context, invocation invocation.Invocation, userID string) error {
	return a.authorization.authorizeUserAccountState(ctx, invocation, userID)
}

func (a userProfileAuthorization) AuthorizeProfilePictureWrite(ctx context.Context, invocation invocation.Invocation, userID string) error {
	action := model.ActionUserManage
	if invocation.Principal().UserID.String() == userID {
		action = model.ActionUserProfilePictureManage
	}
	return a.authorization.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceUser, ID: userID})
}
