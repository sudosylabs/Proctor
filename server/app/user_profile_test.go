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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type userProfileStoreFake struct {
	events              *[]string
	current             *model.User
	currentContext      *store.CurrentUserContext
	currentContextError error
	currentContextLimit int
	updateInput         *store.UserProfileUpdate
	listOptions         store.UserListOptions
}

func (s *userProfileStoreFake) Get(context.Context, model.UserID) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.current, nil
}
func (s *userProfileStoreFake) List(_ context.Context, options store.UserListOptions) ([]*model.User, error) {
	*s.events = append(*s.events, "list-users")
	s.listOptions = options
	return nil, nil
}
func (s *userProfileStoreFake) GetCurrentContext(_ context.Context, _ model.UserID, limit int) (*store.CurrentUserContext, error) {
	s.currentContextLimit = limit
	if s.currentContextError != nil {
		return nil, s.currentContextError
	}
	if s.currentContext == nil {
		return nil, errors.New("current context is not configured")
	}
	return s.currentContext, nil
}
func (s *userProfileStoreFake) UpdateProfile(_ context.Context, input *store.UserProfileUpdate) (*model.User, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	updated := *s.current
	updated.ApplyProfileChanges(&input.Changes)
	updated.Revision = input.ExpectedRevision + 1
	return &updated, nil
}

type userProfileAuthorizerFake struct {
	events      *[]string
	searchScope store.UserVisibilityScope
	fullRead    bool
	readErr     error
	writeErr    error
}

func TestCurrentUserContextReturnsBoundedNavigationProjection(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	attempt := &store.CurrentUserAttemptSelector{
		AttemptID: model.NewExamAttemptID(), SittingID: model.NewExamSittingID(), State: model.ExamAttemptSuspended,
	}
	persisted := &store.CurrentUserContext{
		UserID: userID, Username: "student", DisplayName: "Student",
		AvailableProductAreas: []store.CurrentUserProductArea{
			store.CurrentUserProductAreaAccount, store.CurrentUserProductAreaSettings, store.CurrentUserProductAreaStudentActivity,
		},
		ManagementScopes: []store.CurrentUserManagementScope{{
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewAcademicUnitID().String(), DisplayName: "Engineering",
		}},
		UnresolvedAttempt: attempt,
	}
	persistence := &userProfileStoreFake{events: &[]string{}, currentContext: persisted}
	application := &App{userProfileService: &userProfileService{users: persistence}}
	principal := userSettingsSessionPrincipal(time.Now())
	principal.UserID = userID
	principal.ClientType = model.SessionClientDesktop
	principal.DesktopRegistrationID = model.NewDesktopRegistrationID()
	principal.DPoPKeyThumbprint = strings.Repeat("A", 43)
	principal.RegisteredDesktopKey = true
	principal.DesktopRelease = "1.0.0"
	principal.DesktopBuildID = "context-test"
	principal.DesktopPlatform = model.DesktopPlatformDarwin
	principal.DesktopArchitecture = model.DesktopArchitectureARM64
	principal.DesktopRealtimeProtocol = 1

	got, err := application.GetCurrentUserContext(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	require.NoError(t, err)
	require.Equal(t, 21, persistence.currentContextLimit)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, "/api/v1/users/"+userID.String()+"/profile-picture", got.ProfilePictureReference)
	require.True(t, got.SessionManagementAvailable)
	require.Equal(t, principal.DesktopRegistrationID, got.CurrentDesktopRegistrationID)
	require.NotNil(t, got.UnresolvedAttempt)
	require.Equal(t, attempt.AttemptID, got.UnresolvedAttempt.AttemptID)
	require.Len(t, got.ManagementScopes, 1)
	require.Len(t, got.AvailableProductAreas, 3)
	got.AvailableProductAreas[0] = store.CurrentUserProductAreaAdministration
	got.ManagementScopes[0].DisplayName = "changed"
	got.UnresolvedAttempt.State = model.ExamAttemptActive
	require.Equal(t, store.CurrentUserProductAreaAccount, persisted.AvailableProductAreas[0], "GetCurrentUserContext mutated its persistence projection")
	require.Equal(t, "Engineering", persisted.ManagementScopes[0].DisplayName, "GetCurrentUserContext mutated its persistence projection")
	require.Equal(t, model.ExamAttemptSuspended, persisted.UnresolvedAttempt.State, "GetCurrentUserContext mutated its persistence projection")
}

func TestCurrentUserContextRejectsPersonalAccessTokensWithoutReading(t *testing.T) {
	t.Parallel()
	persistence := &userProfileStoreFake{events: &[]string{}}
	application := &App{userProfileService: &userProfileService{users: persistence}}
	principal := model.Principal{
		UserID: model.NewUserID(), CredentialID: model.PrincipalCredentialID(model.NewPersonalAccessTokenID()),
		CredentialType: model.CredentialPersonalAccessToken, AuthenticationMethod: "personal_access_token",
		ClientType: model.SessionClientCLI, CredentialScopes: []string{string(model.ActionUserView)},
	}
	_, err := application.GetCurrentUserContext(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "GetCurrentUserContext() error = %v", err)
	require.Equal(t, 0, persistence.currentContextLimit, "unauthorized request reached persistence with limit")
}

func (a *userProfileAuthorizerFake) AuthorizeSearch(context.Context, Invocation) (store.UserVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-search")
	if a.searchScope.InstitutionWide || a.searchScope.ClassMemberInstitutionWide ||
		len(a.searchScope.AcademicUnitRootIDs) > 0 || len(a.searchScope.ClassMemberAcademicUnitRootIDs) > 0 ||
		len(a.searchScope.ClassIDs) > 0 {
		return a.searchScope, nil
	}
	return store.UserVisibilityScope{ClassIDs: []model.ClassID{"class-a"}, ActiveAt: model.TimeFromMillis(100)}, nil
}
func (a *userProfileAuthorizerFake) AuthorizeProfileRead(context.Context, Invocation, string) (bool, error) {
	*a.events = append(*a.events, "authorize-read")
	return a.fullRead, a.readErr
}
func (a *userProfileAuthorizerFake) AuthorizeRead(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-read")
	return a.readErr
}
func (a *userProfileAuthorizerFake) Authorize(context.Context, Invocation, model.Action, model.Resource) error {
	*a.events = append(*a.events, "authorize-manage")
	return nil
}

func (a *userProfileAuthorizerFake) AuthorizeAccountStateManage(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-manage")
	return nil
}
func (a *userProfileAuthorizerFake) AuthorizeProfilePictureWrite(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-profile-picture-write")
	return a.writeErr
}

func TestUserProfileUpdateIsAuthorizedAndAuditAtomic(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC"}
	persistence := &userProfileStoreFake{events: &events, current: current}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, auditor, func() time.Time { return time.UnixMilli(500) })
	displayName := "Updated Student"
	updated, err := service.UpdateUserProfile(context.Background(), Invocation{}, UpdateUserProfileCommand{ID: current.ID, DisplayName: &displayName})
	require.NoError(t, err)
	require.Equal(t, current.Email, updated.Email, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.True(t, updated.EmailVerified, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.Equal(t, displayName, updated.DisplayName, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.Equal(t, int64(3), persistence.updateInput.ExpectedRevision, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.Equal(t, current.ID, persistence.updateInput.UserID, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.NotNil(t, persistence.updateInput.Changes.DisplayName, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.Equal(t, displayName, *persistence.updateInput.Changes.DisplayName, "updated/input = %#v / %#v", updated, persistence.updateInput)
	require.Equal(t, int64(4), updated.Revision, "updated/input = %#v / %#v", updated, persistence.updateInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize-manage", "get-user", "audit-begin", "store-update"}, events)
}

func TestUserProfileSearchReturnsEmptyCollection(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &userProfileStoreFake{events: &events}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now)
	users, err := service.SearchUsers(context.Background(), Invocation{}, SearchUsersQuery{})
	require.NoError(t, err)
	require.NotNil(t, users.Items, "users")
	require.Empty(t, users.Items, "users")
	require.Equal(t, []model.ClassID{"class-a"}, persistence.listOptions.Visibility.ClassIDs)
}

func TestScopedUserSearchCannotRequestDisabledUsers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		scope store.UserVisibilityScope
	}{
		{name: "academic unit", scope: store.UserVisibilityScope{
			AcademicUnitRootIDs: []model.AcademicUnitID{model.NewAcademicUnitID()}, ActiveAt: model.TimeFromMillis(100),
		}},
		{name: "institution class members", scope: store.UserVisibilityScope{
			ClassMemberInstitutionWide: true, ActiveAt: model.TimeFromMillis(100),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, includeDisabled := range []bool{false, true} {
				events := []string{}
				persistence := &userProfileStoreFake{events: &events}
				service := newUserProfileService(
					persistence,
					&userProfileAuthorizerFake{events: &events, searchScope: test.scope},
					&institutionAuditorFake{events: &events}, time.Now,
				)
				_, err := service.SearchUsers(context.Background(), Invocation{}, SearchUsersQuery{
					Limit: 10, IncludeDisabled: includeDisabled,
				})
				require.NoError(t, err)
				require.False(t, persistence.listOptions.IncludeDisabled, "%s scoped IncludeDisabled=%t reached persistence as true", test.name, includeDisabled)
			}
		})
	}

	events := []string{}
	persistence := &userProfileStoreFake{events: &events}
	service := newUserProfileService(
		persistence,
		&userProfileAuthorizerFake{events: &events, searchScope: store.UserVisibilityScope{InstitutionWide: true}},
		&institutionAuditorFake{events: &events}, time.Now,
	)
	_, err := service.SearchUsers(context.Background(), Invocation{}, SearchUsersQuery{
		Limit: 10, IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.True(t, persistence.listOptions.IncludeDisabled, "institution-wide IncludeDisabled=true was not preserved")
}

func TestScopedUserSearchReturnsOnlySafeDirectoryFields(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{
		ID: model.NewUserID(), Username: "student", Email: "private@example.edu", EmailVerified: true,
		DisplayName: "Student", FirstName: "Private", LastName: "Person", Locale: "fr", Timezone: "Europe/Paris",
		LastLoginAt: model.OptionalTimeFromMillis(100), LastActivityAt: model.OptionalTimeFromMillis(200),
		DisabledAt: model.OptionalTimeFromMillis(300),
	}
	persistence := &userProfileStoreFake{events: &events, current: user}
	service := newUserProfileService(
		&userProfileSearchStoreFake{userProfileStoreFake: persistence, values: []*model.User{user}},
		&userProfileAuthorizerFake{events: &events, searchScope: store.UserVisibilityScope{AcademicUnitRootIDs: []model.AcademicUnitID{model.NewAcademicUnitID()}, ActiveAt: model.TimeFromMillis(100)}},
		&institutionAuditorFake{events: &events}, time.Now,
	)
	users, err := service.SearchUsers(context.Background(), Invocation{}, SearchUsersQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, users.Items, 1)
	require.Equal(t, "student", users.Items[0].Username)
	require.Equal(t, "Student", users.Items[0].DisplayName)
	require.Empty(t, users.Items[0].Email)
	require.False(t, users.Items[0].EmailVerified)
	require.Empty(t, users.Items[0].Locale)
	require.Empty(t, users.Items[0].Timezone)
	require.False(t, users.Items[0].LastLoginAt.Valid)
	require.False(t, users.Items[0].LastActivityAt.Valid)
	require.False(t, users.Items[0].DisabledAt.Valid)
	require.Empty(t, users.Items[0].DefaultProfilePictureSeed)
	require.True(t, users.Items[0].DefaultProfilePictureFileID.IsZero())
	require.True(t, users.Items[0].CustomProfilePictureFileID.IsZero())
	require.Equal(t, "private@example.edu", user.Email, "scoped projection mutated the persisted User")
	require.True(t, user.EmailVerified, "scoped projection mutated the persisted User")
}

type userProfileSearchStoreFake struct {
	*userProfileStoreFake
	values []*model.User
}

func (s *userProfileSearchStoreFake) List(_ context.Context, options store.UserListOptions) ([]*model.User, error) {
	*s.events = append(*s.events, "list-users")
	s.listOptions = options
	return s.values, nil
}

func TestUserProfileUpdateRejectsControlCharacterNamesBeforeAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC"}
	persistence := &userProfileStoreFake{events: &events, current: current}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, auditor, func() time.Time { return time.UnixMilli(500) })
	displayName := "Student\r\nBcc: other@example.test"
	_, err := service.UpdateUserProfile(context.Background(), Invocation{}, UpdateUserProfileCommand{ID: current.ID, DisplayName: &displayName})
	failure, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "user.invalid", failure.Code())
	require.Equal(t, []string{"authorize-manage", "get-user"}, events)
}
