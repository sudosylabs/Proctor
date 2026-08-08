// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type accountStateStoreFake struct {
	events *[]string
	user   *model.User
	input  *store.UserDisabledStateChange
	result *store.UserDisabledStateResult
	err    error
}

func (s *accountStateStoreFake) Get(context.Context, string) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.user, nil
}

func (s *accountStateStoreFake) SetDisabledWithAudit(_ context.Context, input *store.UserDisabledStateChange) (*store.UserDisabledStateResult, error) {
	*s.events = append(*s.events, "store-set-disabled")
	s.input = input
	return s.result, s.err
}

type accountStateEffectsFake struct{ events *[]string }

func (e *accountStateEffectsFake) SessionsRevoked(context.Context, string, []*model.Session, []string) {
	*e.events = append(*e.events, "publish-revocation")
}

func TestAccountDisableCommitsBeforePublishingRevocation(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Locale: "en", Timezone: "UTC"}
	updated := *user
	updated.DisabledAt = model.OptionalTimeFromMillis(500)
	updated.Revision++
	persistence := &accountStateStoreFake{events: &events, user: user, result: &store.UserDisabledStateResult{User: &updated, RevokedSessions: []*model.Session{{ID: model.NewSessionID()}}, RevokedTokenHashes: []string{"hash"}}}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewId()}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, auditor, &accountStateEffectsFake{events: &events}, func() time.Time { return time.UnixMilli(500) })
	result, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.DisabledAt.Millis() != 500 || persistence.input.ExpectedRevision != 3 || !persistence.input.Disabled || persistence.input.AuditEventID == "" {
		t.Fatalf("result/input = %#v / %#v", result, persistence.input)
	}
	want := []string{"authorize-manage", "get-user", "audit-begin", "store-set-disabled", "publish-revocation"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountDisableFailurePublishesNoRevocation(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Locale: "en", Timezone: "UTC"}
	persistence := &accountStateStoreFake{events: &events, user: user, err: store.NewErrConflict("user", "users_revision", errors.New("stale"))}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, &accountStateEffectsFake{events: &events}, time.Now)
	_, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false})
	if !Is(err, "user.conflict") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-user", "audit-begin", "store-set-disabled", "audit-fail"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountEnablePublishesNoRevocation(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", DisabledAt: model.OptionalTimeFromMillis(100), Locale: "en", Timezone: "UTC"}
	updated := *user
	updated.DisabledAt = model.OptionalTime{}
	updated.Revision++
	persistence := &accountStateStoreFake{events: &events, user: user, result: &store.UserDisabledStateResult{User: &updated, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, &accountStateEffectsFake{events: &events}, func() time.Time { return time.UnixMilli(500) })
	result, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.DisabledAt.Valid || persistence.input.Disabled {
		t.Fatalf("result/input = %#v / %#v", result, persistence.input)
	}
	want := []string{"authorize-manage", "get-user", "audit-begin", "store-set-disabled"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountSelfDisableIsRejectedAfterAuthorization(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	service := newAccountStateService(&accountStateStoreFake{events: &events}, &userProfileAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events}, &accountStateEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserId: userID}, model.RequestMetadata{})
	_, err := service.SetEnabled(context.Background(), invocation, SetUserEnabledCommand{ID: userID, Enabled: false})
	if !Is(err, "request.invalid") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"authorize-manage"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
