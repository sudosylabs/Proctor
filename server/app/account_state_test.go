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

type securityNoticeMailerFake struct {
	events   *[]string
	requests []DirectMailPreparation
	err      error
}

func (m *securityNoticeMailerFake) prepare(request NoticeMailPreparation, key model.MailTemplateKey) (*preparedDirectMail, error) {
	if m.events != nil {
		*m.events = append(*m.events, "prepare-mail")
	}
	direct := DirectMailPreparation{Recipient: request.Recipient, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceSecurityNotice, TemplateKey: key, At: request.At,
		Deadline: request.At.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver}
	m.requests = append(m.requests, direct)
	if m.err != nil {
		return nil, m.err
	}
	occurrenceID := model.NewMailOccurrenceID()
	return &preparedDirectMail{
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSecurityNotice, TemplateKey: key},
		Delivery:   &model.MailDelivery{ID: model.NewMailDeliveryID(), OccurrenceID: occurrenceID, TemplateKey: key},
		Job:        &model.Job{ID: model.NewJobID(), Type: model.JobTypeMailDeliver},
	}, nil
}

func (m *securityNoticeMailerFake) PrepareAccountStateChanged(request NoticeMailPreparation, enabled bool) (*preparedDirectMail, error) {
	key := model.MailTemplateIdentityAccountDisabled
	if enabled {
		key = model.MailTemplateIdentityAccountEnabled
	}
	return m.prepare(request, key)
}

func (m *securityNoticeMailerFake) PrepareSessionsRevokedByAdministrator(request NoticeMailPreparation) (*preparedDirectMail, error) {
	return m.prepare(request, model.MailTemplateIdentitySessionsRevokedByAdmin)
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
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{
		Descriptor: model.ExternalAuthenticationProvider{Id: "campus", Type: "oidc"},
	}}}}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, capabilities, auditor, mailer, &accountStateEffectsFake{events: &events}, func() time.Time { return time.UnixMilli(500) })
	result, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.DisabledAt.Millis() != 500 || persistence.input.ExpectedRevision != 3 || !persistence.input.Disabled || persistence.input.AuditEventID == "" {
		t.Fatalf("result/input = %#v / %#v", result, persistence.input)
	}
	if _, available := persistence.input.Capabilities.Providers["campus"]; !available {
		t.Fatalf("Store capability snapshot = %#v, want configured campus provider", persistence.input.Capabilities)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateIdentityAccountDisabled ||
		persistence.input.Occurrence == nil || persistence.input.Delivery == nil || persistence.input.DeliveryJob == nil {
		t.Fatalf("mail request/input = %#v / %#v", mailer.requests, persistence.input)
	}
	want := []string{"authorize-manage", "get-user", "prepare-mail", "audit-begin", "store-set-disabled", "publish-revocation"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountDisableFailurePublishesNoRevocation(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3, Username: "student", Locale: "en", Timezone: "UTC"}
	persistence := &accountStateStoreFake{events: &events, user: user, err: store.NewErrConflict("user", "users_revision", errors.New("stale"))}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, &securityNoticeMailerFake{events: &events}, &accountStateEffectsFake{events: &events}, time.Now)
	_, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false})
	if !Is(err, "user.conflict") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-user", "prepare-mail", "audit-begin", "store-set-disabled", "audit-fail"}
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
	mailer := &securityNoticeMailerFake{events: &events}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer, &accountStateEffectsFake{events: &events}, func() time.Time { return time.UnixMilli(500) })
	result, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.DisabledAt.Valid || persistence.input.Disabled {
		t.Fatalf("result/input = %#v / %#v", result, persistence.input)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateIdentityAccountEnabled {
		t.Fatalf("mail requests = %#v", mailer.requests)
	}
	want := []string{"authorize-manage", "get-user", "prepare-mail", "audit-begin", "store-set-disabled"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountSelfDisableIsRejectedAfterAuthorization(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	service := newAccountStateService(&accountStateStoreFake{events: &events}, &userProfileAuthorizerFake{events: &events}, &accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events}, &securityNoticeMailerFake{events: &events}, &accountStateEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserID: model.UserID(userID)}, model.RequestMetadata{})
	_, err := service.SetEnabled(context.Background(), invocation, SetUserEnabledCommand{ID: userID, Enabled: false})
	if !Is(err, "request.invalid") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"authorize-manage"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountStateReplayDoesNotCreateAnotherNoticeOrAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3,
		Username: "student", Email: "student@example.edu", Locale: "en", Timezone: "UTC", DisabledAt: model.OptionalTimeFromMillis(200)}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newAccountStateService(&accountStateStoreFake{events: &events, user: user}, &userProfileAuthorizerFake{events: &events},
		&accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events}, mailer, &accountStateEffectsFake{events: &events}, time.Now)
	result, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false})
	if err != nil || result != user || len(mailer.requests) != 0 {
		t.Fatalf("replay result/error/mail = %#v / %v / %#v", result, err, mailer.requests)
	}
	if want := []string{"authorize-manage", "get-user"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAccountStateIdempotentNoOpPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	actor := model.NewUserID()
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(200), Revision: 3,
		Username: "student", Email: "student@example.edu", Locale: "en", Timezone: "UTC", DisabledAt: model.OptionalTimeFromMillis(200)}
	persistence := &accountStateStoreFake{events: &events, user: user, result: &store.UserDisabledStateResult{User: user}}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer, &accountStateEffectsFake{events: &events}, time.Now)
	result, err := service.SetEnabled(context.Background(), NewInvocation(model.Principal{UserID: actor}, model.RequestMetadata{}),
		SetUserEnabledCommand{ID: user.ID.String(), Enabled: false, IdempotencyKey: "row"})
	if err != nil || result != user || len(mailer.requests) != 1 || persistence.input == nil || persistence.input.Occurrence == nil {
		t.Fatalf("idempotent no-op result=%#v error=%v mail=%#v input=%#v", result, err, mailer.requests, persistence.input)
	}
}

func TestAccountStateRetainedOutcomeBypassesMailAfterLaterStateChange(t *testing.T) {
	t.Parallel()
	events := []string{}
	actor := model.NewUserID()
	current := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(300), Revision: 4,
		Username: "student", Email: "student@example.edu", Locale: "en", Timezone: "UTC"}
	original := *current
	original.DisabledAt = model.OptionalTimeFromMillis(200)
	persistence := &accountStateStoreFake{events: &events, user: current, result: &store.UserDisabledStateResult{User: &original}}
	mailer := &securityNoticeMailerFake{events: &events, err: errors.New("mail unavailable")}
	service := newAccountStateService(persistence, &userProfileAuthorizerFake{events: &events}, &accessPolicyCapabilitiesFake{},
		&institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer, &accountStateEffectsFake{events: &events}, time.Now)
	result, err := service.SetEnabled(context.Background(), NewInvocation(model.Principal{UserID: actor}, model.RequestMetadata{}),
		SetUserEnabledCommand{ID: current.ID.String(), Enabled: false, IdempotencyKey: "row", batchRetainedOutcome: true})
	if err != nil || result != &original && result.ID != original.ID || len(mailer.requests) != 0 {
		t.Fatalf("retained result=%#v error=%v mail=%#v", result, err, mailer.requests)
	}
}

func TestAccountStateMailPreparationFailureStartsNoMutation(t *testing.T) {
	t.Parallel()
	events := []string{}
	user := &model.User{ID: model.NewUserID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 3,
		Username: "student", Email: "student@example.edu", Locale: "en", Timezone: "UTC"}
	service := newAccountStateService(&accountStateStoreFake{events: &events, user: user}, &userProfileAuthorizerFake{events: &events},
		&accessPolicyCapabilitiesFake{}, &institutionAuditorFake{events: &events}, &securityNoticeMailerFake{events: &events, err: errors.New("render failed")},
		&accountStateEffectsFake{events: &events}, time.Now)
	if _, err := service.SetEnabled(context.Background(), Invocation{}, SetUserEnabledCommand{ID: user.ID.String(), Enabled: false}); !Is(err, "administration.unavailable") {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"authorize-manage", "get-user", "prepare-mail"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
