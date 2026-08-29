// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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

func (s *userProfileStoreFake) Get(context.Context, string) (*model.User, error) {
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
func (s *userProfileStoreFake) UpdateProfileWithAudit(_ context.Context, input *store.UserProfileUpdate) (*model.User, error) {
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
	application := &App{userProfiles: &userProfileService{users: persistence}}
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
	if err != nil {
		t.Fatal(err)
	}
	if persistence.currentContextLimit != 21 || got.UserID != userID || got.ProfilePictureReference != "/api/v1/users/"+userID.String()+"/profile-picture" ||
		!got.SessionManagementAvailable || got.CurrentDesktopRegistrationID != principal.DesktopRegistrationID || got.UnresolvedAttempt == nil ||
		got.UnresolvedAttempt.AttemptID != attempt.AttemptID || len(got.ManagementScopes) != 1 || len(got.AvailableProductAreas) != 3 {
		t.Fatalf("GetCurrentUserContext() = %#v", got)
	}
	got.AvailableProductAreas[0] = store.CurrentUserProductAreaAdministration
	got.ManagementScopes[0].DisplayName = "changed"
	got.UnresolvedAttempt.State = model.ExamAttemptActive
	if persisted.AvailableProductAreas[0] != store.CurrentUserProductAreaAccount || persisted.ManagementScopes[0].DisplayName != "Engineering" ||
		persisted.UnresolvedAttempt.State != model.ExamAttemptSuspended {
		t.Fatal("GetCurrentUserContext mutated its persistence projection")
	}
}

func TestCurrentUserContextRejectsPersonalAccessTokensWithoutReading(t *testing.T) {
	t.Parallel()
	persistence := &userProfileStoreFake{events: &[]string{}}
	application := &App{userProfiles: &userProfileService{users: persistence}}
	principal := model.Principal{
		UserID: model.NewUserID(), CredentialID: model.PrincipalCredentialID(model.NewPersonalAccessTokenID()),
		CredentialType: model.CredentialPersonalAccessToken, AuthenticationMethod: "personal_access_token",
		ClientType: model.SessionClientCLI, CredentialScopes: []string{string(model.ActionUserView)},
	}
	if _, err := application.GetCurrentUserContext(context.Background(), NewInvocation(principal, model.RequestMetadata{})); !Is(err, "authentication.invalid_token") {
		t.Fatalf("GetCurrentUserContext() error = %v", err)
	}
	if persistence.currentContextLimit != 0 {
		t.Fatalf("unauthorized request reached persistence with limit %d", persistence.currentContextLimit)
	}
}

func (a *userProfileAuthorizerFake) AuthorizeSearch(context.Context, Invocation) (store.UserVisibilityScope, error) {
	*a.events = append(*a.events, "authorize-search")
	if a.searchScope.InstitutionWide || a.searchScope.ClassMemberInstitutionWide ||
		len(a.searchScope.AcademicUnitRootIDs) > 0 || len(a.searchScope.ClassMemberAcademicUnitRootIDs) > 0 ||
		len(a.searchScope.ClassIDs) > 0 {
		return a.searchScope, nil
	}
	return store.UserVisibilityScope{ClassIDs: []string{"class-a"}, ActiveAt: 100}, nil
}
func (a *userProfileAuthorizerFake) AuthorizeProfileRead(context.Context, Invocation, string) (bool, error) {
	*a.events = append(*a.events, "authorize-read")
	return a.fullRead, a.readErr
}
func (a *userProfileAuthorizerFake) AuthorizeRead(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-read")
	return a.readErr
}
func (a *userProfileAuthorizerFake) AuthorizeManage(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-manage")
	return nil
}

func (a *userProfileAuthorizerFake) AuthorizeAccountStateManage(ctx context.Context, invocation Invocation, userID string) error {
	return a.AuthorizeManage(ctx, invocation, userID)
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
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, auditor, func() time.Time { return time.UnixMilli(500) })
	displayName := "Updated Student"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateUserProfileCommand{ID: current.ID.String(), DisplayName: &displayName})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Email != current.Email || !updated.EmailVerified || updated.DisplayName != displayName || persistence.updateInput.ExpectedRevision != 3 || persistence.updateInput.UserID != current.ID || persistence.updateInput.Changes.DisplayName == nil || *persistence.updateInput.Changes.DisplayName != displayName || updated.Revision != 4 {
		t.Fatalf("updated/input = %#v / %#v", updated, persistence.updateInput)
	}
	want := []string{"authorize-manage", "get-user", "audit-begin", "store-update"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestUserProfileSearchReturnsEmptyCollection(t *testing.T) {
	t.Parallel()
	events := []string{}
	persistence := &userProfileStoreFake{events: &events}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now)
	users, err := service.Search(context.Background(), Invocation{}, SearchUsersQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if users == nil || len(users) != 0 {
		t.Fatalf("users = %#v, want non-nil empty", users)
	}
	if !reflect.DeepEqual(persistence.listOptions.Visibility.ClassIDs, []string{"class-a"}) {
		t.Fatalf("visibility = %#v", persistence.listOptions.Visibility)
	}
}

func TestScopedUserSearchCannotRequestDisabledUsers(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		scope store.UserVisibilityScope
	}{
		{name: "academic unit", scope: store.UserVisibilityScope{
			AcademicUnitRootIDs: []string{model.NewAcademicUnitID().String()}, ActiveAt: 100,
		}},
		{name: "institution class members", scope: store.UserVisibilityScope{
			ClassMemberInstitutionWide: true, ActiveAt: 100,
		}},
	} {
		for _, includeDisabled := range []bool{false, true} {
			events := []string{}
			persistence := &userProfileStoreFake{events: &events}
			service := newUserProfileService(
				persistence,
				&userProfileAuthorizerFake{events: &events, searchScope: test.scope},
				&institutionAuditorFake{events: &events}, time.Now,
			)
			if _, err := service.Search(context.Background(), Invocation{}, SearchUsersQuery{
				Limit: 10, IncludeDisabled: includeDisabled,
			}); err != nil {
				t.Fatal(err)
			}
			if persistence.listOptions.IncludeDisabled {
				t.Fatalf("%s scoped IncludeDisabled=%t reached persistence as true", test.name, includeDisabled)
			}
		}
	}

	events := []string{}
	persistence := &userProfileStoreFake{events: &events}
	service := newUserProfileService(
		persistence,
		&userProfileAuthorizerFake{events: &events, searchScope: store.UserVisibilityScope{InstitutionWide: true}},
		&institutionAuditorFake{events: &events}, time.Now,
	)
	if _, err := service.Search(context.Background(), Invocation{}, SearchUsersQuery{
		Limit: 10, IncludeDisabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !persistence.listOptions.IncludeDisabled {
		t.Fatal("institution-wide IncludeDisabled=true was not preserved")
	}
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
		&userProfileAuthorizerFake{events: &events, searchScope: store.UserVisibilityScope{AcademicUnitRootIDs: []string{model.NewId()}, ActiveAt: 100}},
		&institutionAuditorFake{events: &events}, time.Now,
	)
	users, err := service.Search(context.Background(), Invocation{}, SearchUsersQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "student" || users[0].DisplayName != "Student" ||
		users[0].Email != "" || users[0].EmailVerified || users[0].Locale != "" || users[0].Timezone != "" ||
		users[0].LastLoginAt.Valid || users[0].LastActivityAt.Valid || users[0].DisabledAt.Valid ||
		users[0].DefaultProfilePictureSeed != "" || !users[0].DefaultProfilePictureFileID.IsZero() ||
		!users[0].CustomProfilePictureFileID.IsZero() {
		t.Fatalf("scoped projection = %#v", users)
	}
	if user.Email != "private@example.edu" || !user.EmailVerified {
		t.Fatal("scoped projection mutated the persisted User")
	}
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
