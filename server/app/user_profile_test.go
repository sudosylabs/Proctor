// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type userProfileStoreFake struct {
	events      *[]string
	current     *model.User
	updateInput *store.UserProfileUpdate
}

func (s *userProfileStoreFake) Get(context.Context, string) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.current, nil
}
func (s *userProfileStoreFake) List(context.Context, store.UserListOptions) ([]*model.User, error) {
	*s.events = append(*s.events, "list-users")
	return nil, nil
}
func (s *userProfileStoreFake) UpdateProfileWithAudit(_ context.Context, input *store.UserProfileUpdate) (*model.User, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	updated := *input.User
	updated.Revision = input.ExpectedRevision + 1
	return &updated, nil
}

type userProfileAuthorizerFake struct{ events *[]string }

func (a *userProfileAuthorizerFake) AuthorizeSearch(context.Context, Invocation) error {
	*a.events = append(*a.events, "authorize-search")
	return nil
}
func (a *userProfileAuthorizerFake) AuthorizeRead(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-read")
	return nil
}
func (a *userProfileAuthorizerFake) AuthorizeManage(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-manage")
	return nil
}

func TestUserProfileUpdateIsAuthorizedAndAuditAtomic(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC"}
	persistence := &userProfileStoreFake{events: &events, current: current}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newUserProfileService(persistence, &userProfileAuthorizerFake{events: &events}, auditor, func() time.Time { return time.UnixMilli(500) })
	email := "new@example.edu"
	updated, err := service.Update(context.Background(), Invocation{}, UpdateUserProfileCommand{ID: current.ID.String(), Email: &email})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Email != email || updated.EmailVerified || persistence.updateInput.ExpectedRevision != 3 || persistence.updateInput.User.Revision != 3 || updated.Revision != 4 {
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
	service := newUserProfileService(&userProfileStoreFake{events: &events}, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, time.Now)
	users, err := service.Search(context.Background(), Invocation{}, SearchUsersQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if users == nil || len(users) != 0 {
		t.Fatalf("users = %#v, want non-nil empty", users)
	}
}
