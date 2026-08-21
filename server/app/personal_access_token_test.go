// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenScopesRejectRelationshipOnlyParticipationAction(t *testing.T) {
	t.Parallel()

	_, err := normalizePersonalAccessTokenScopes([]string{string(model.ActionExamSittingParticipate)})
	if err == nil || !Is(err, "personal_access_token.invalid") {
		t.Fatalf("normalizePersonalAccessTokenScopes() error = %v, want personal_access_token.invalid", err)
	}
}

func TestPersonalAccessTokenScopesRejectInteractiveOnlyAdministration(t *testing.T) {
	t.Parallel()

	for _, action := range []model.Action{
		model.ActionAccessPolicyManage,
		model.ActionExternalIdentityManage,
		model.ActionRoleBindingManage,
		model.ActionRoleManage,
	} {
		if _, err := normalizePersonalAccessTokenScopes([]string{string(action)}); !Is(err, "personal_access_token.invalid") {
			t.Fatalf("scope %q error = %v, want personal_access_token.invalid", action, err)
		}
	}
}

func TestPersonalAccessTokenCreationCannotExceedCurrentRoleAuthority(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	unitID := model.NewAcademicUnitID()
	tokens := &personalAccessTokenStoreFake{events: &[]string{}}
	service, err := newPersonalAccessTokenAdministrationService(
		tokens,
		&personalAccessTokenUserStoreFake{},
		&personalAccessTokenAcademicUnitStoreFake{unit: &model.AcademicUnit{ID: unitID}},
		&personalAccessTokenInstitutionStoreFake{},
		&personalAccessTokenAuditorFake{},
		&personalAccessTokenScopeAuthorizerFake{allowed: false},
		&personalAccessTokenMailPreparerFake{},
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute,
		func() string { return "must-not-be-generated" },
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}),
		CreatePersonalAccessTokenCommand{
			Scopes: []string{string(model.ActionClassManage)}, AcademicUnitID: unitID.String(),
			ExpiresAt: now.Add(time.Hour).UnixMilli(),
		},
	)
	if !Is(err, "personal_access_token.invalid") {
		t.Fatalf("error = %v, want personal_access_token.invalid", err)
	}
	if tokens.saveCalls != 0 {
		t.Fatalf("save calls = %d", tokens.saveCalls)
	}
}

func TestPersonalAccessTokenAdministrationCreatesThroughFocusedContracts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	raw := "raw-credential-returned-once"
	userID := model.NewUserID()
	unitID := model.NewAcademicUnitID()
	institutionID := model.NewInstitutionID()
	events := []string{}
	tokens := &personalAccessTokenStoreFake{events: &events, saveAt: now}
	units := &personalAccessTokenAcademicUnitStoreFake{events: &events, unit: &model.AcademicUnit{ID: unitID}}
	institutions := &personalAccessTokenInstitutionStoreFake{
		events: &events, institution: &model.Institution{ID: institutionID},
	}
	audit := &personalAccessTokenAuditorFake{events: &events}
	authorization := &personalAccessTokenScopeAuthorizerFake{events: &events, allowed: true}
	mail := &personalAccessTokenMailPreparerFake{}
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, units, institutions, audit, authorization,
		mail,
		PersonalAccessTokenPolicy{
			MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3,
		},
		15*time.Minute,
		func() string { return raw },
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{RequestID: "request-a"}),
		CreatePersonalAccessTokenCommand{
			Description: "automation", Scopes: []string{string(model.ActionClassView)},
			AcademicUnitID: unitID.String(), ExpiresAt: now.Add(time.Hour).UnixMilli(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Credential != raw || created.Token != tokens.saved {
		t.Fatalf("creation = %#v", created)
	}
	if tokens.candidate.TokenHash != model.HashToken(raw) || tokens.candidate.TokenHash == raw {
		t.Fatalf("persisted token hash = %q", tokens.candidate.TokenHash)
	}
	if tokens.candidate.UserID != userID || tokens.maximum != 3 {
		t.Fatalf("save candidate = %#v, maximum = %d", tokens.candidate, tokens.maximum)
	}
	if len(mail.requests) != 1 {
		t.Fatalf("mail requests = %d, want 1", len(mail.requests))
	}
	request := mail.requests[0]
	if request.TemplateKey != model.MailTemplateIdentityPersonalAccessTokenCreated ||
		request.Description != "automation" || request.ActionCount != 1 ||
		!request.AcademicUnitScoped || !request.ExpiresAt.Equal(now.Add(time.Hour)) ||
		!request.ActionAt.Equal(now) {
		t.Fatalf("mail request = %#v", request)
	}
	if got := fmt.Sprintf("%#v", request); strings.Contains(got, raw) || strings.Contains(got, tokens.candidate.TokenHash) || strings.Contains(got, string(model.ActionClassView)) {
		t.Fatalf("credential, hash, or full scopes entered mail request: %s", got)
	}
	if audit.action != actionPersonalAccessTokenCreate ||
		audit.resource != (model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}) {
		t.Fatalf("audit action=%q resource=%#v", audit.action, audit.resource)
	}
	if got := fmt.Sprintf("%#v %#v %#v", audit.parameters, audit.prior, audit.result); strings.Contains(got, raw) {
		t.Fatalf("raw credential entered audit data: %s", got)
	}
	if want := []string{"academic_unit", "authorize_scopes", "institution", "audit_begin", "prepare", "create"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestPersonalAccessTokenAdministrationExactStateReplayEmitsNoAuditOrMail(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	token := personalAccessTokenForTest(userID, now)
	token.DisabledAt = model.OptionalTimeFrom(now)
	tokens := &personalAccessTokenStoreFake{events: &[]string{}, current: token}
	audit := &personalAccessTokenAuditorFake{}
	mail := &personalAccessTokenMailPreparerFake{}
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{}, audit, &personalAccessTokenScopeAuthorizerFake{allowed: true}, mail,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}

	got, appErr := service.SetDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: true})
	if appErr != nil || got != token {
		t.Fatalf("SetDisabled(replay) = %#v, %v", got, appErr)
	}
	if audit.beginCalls != 0 || len(mail.requests) != 0 || tokens.stateCalls != 0 {
		t.Fatalf("replay audit=%d mail=%d mutations=%d", audit.beginCalls, len(mail.requests), tokens.stateCalls)
	}

	token.RevokedAt = model.OptionalTimeFrom(now)
	got, appErr = service.Revoke(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), RevokePersonalAccessTokenCommand{TokenID: token.ID.String()})
	if appErr != nil || got != token {
		t.Fatalf("Revoke(replay) = %#v, %v", got, appErr)
	}
	if audit.beginCalls != 0 || len(mail.requests) != 0 || tokens.revokeCalls != 0 {
		t.Fatalf("replay audit=%d mail=%d revocations=%d", audit.beginCalls, len(mail.requests), tokens.revokeCalls)
	}
}

func TestPersonalAccessTokenAdministrationConcealsAnotherUsersToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	callerID := model.NewUserID()
	token := personalAccessTokenForTest(model.NewUserID(), now)
	tokens := &personalAccessTokenStoreFake{events: &[]string{}, current: token}
	audit := &personalAccessTokenAuditorFake{}
	service := mustPersonalAccessTokenAdministrationService(
		t, tokens, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{}, audit, now,
	)

	_, err := service.Revoke(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(callerID, now), model.RequestMetadata{}),
		RevokePersonalAccessTokenCommand{TokenID: token.ID.String()},
	)
	if !Is(err, "resource.not_found") {
		t.Fatalf("error = %v, want resource.not_found", err)
	}
	if tokens.revokeCalls != 0 || audit.beginCalls != 0 {
		t.Fatalf("revoke calls=%d audit begins=%d", tokens.revokeCalls, audit.beginCalls)
	}
}

func TestPersonalAccessTokenAdministrationUsesControlledClockForStateChanges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	token := personalAccessTokenForTest(userID, now)
	tokens := &personalAccessTokenStoreFake{events: &[]string{}, current: token}
	mail := &personalAccessTokenMailPreparerFake{}
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, &personalAccessTokenScopeAuthorizerFake{allowed: true}, mail,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	invocation := NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{})

	if _, err := service.SetDisabled(
		context.Background(), invocation,
		SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: true},
	); err != nil {
		t.Fatal(err)
	}
	if tokens.stateAt != now.UnixMilli() || tokens.stateMaximum != 3 {
		t.Fatalf("state change at=%d maximum=%d", tokens.stateAt, tokens.stateMaximum)
	}
	if _, err := service.SetDisabled(
		context.Background(), invocation,
		SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: false},
	); err != nil {
		t.Fatal(err)
	}

	tokens.current = token
	if _, err := service.Revoke(
		context.Background(), invocation,
		RevokePersonalAccessTokenCommand{TokenID: token.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if tokens.revokedAt != now.UnixMilli() {
		t.Fatalf("revoked at = %d, want %d", tokens.revokedAt, now.UnixMilli())
	}
	wantKeys := []model.MailTemplateKey{
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked,
	}
	if len(mail.requests) != len(wantKeys) {
		t.Fatalf("mail requests = %d, want %d", len(mail.requests), len(wantKeys))
	}
	for index, key := range wantKeys {
		if mail.requests[index].TemplateKey != key {
			t.Fatalf("mail request %d key = %q, want %q", index, mail.requests[index].TemplateKey, key)
		}
	}
}

func TestPersonalAccessTokenAdministrationRequiresRecentSessionForCreationAndEnablement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	tokens := &personalAccessTokenStoreFake{events: &[]string{}}
	service := mustPersonalAccessTokenAdministrationService(
		t, tokens, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{}, &personalAccessTokenAuditorFake{}, now,
	)
	stale := personalAccessTokenSessionPrincipal(userID, now.Add(-time.Hour))
	invocation := NewInvocation(stale, model.RequestMetadata{})

	_, err := service.Create(context.Background(), invocation, CreatePersonalAccessTokenCommand{})
	if !Is(err, "authentication.reauthentication_required") {
		t.Fatalf("create error = %v", err)
	}
	_, err = service.SetDisabled(context.Background(), invocation, SetPersonalAccessTokenDisabledCommand{
		TokenID: model.NewPersonalAccessTokenID().String(), Disabled: false,
	})
	if !Is(err, "authentication.reauthentication_required") {
		t.Fatalf("enable error = %v", err)
	}
	if tokens.saveCalls != 0 || tokens.stateCalls != 0 {
		t.Fatalf("store calls: save=%d state=%d", tokens.saveCalls, tokens.stateCalls)
	}
}

func TestPersonalAccessTokenAdministrationRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	tokens := &personalAccessTokenStoreFake{}
	users := &personalAccessTokenUserStoreFake{}
	units := &personalAccessTokenAcademicUnitStoreFake{}
	institutions := &personalAccessTokenInstitutionStoreFake{}
	audit := &personalAccessTokenAuditorFake{}
	authorization := &personalAccessTokenScopeAuthorizerFake{allowed: true}
	mail := &personalAccessTokenMailPreparerFake{}
	policy := PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: time.Hour, MaximumPerUser: 1}
	generator := func() string { return "credential" }

	tests := []struct {
		name string
		make func() (*personalAccessTokenAdministrationService, error)
	}{
		{"token store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(nil, users, units, institutions, audit, authorization, mail, policy, time.Minute, generator, time.Now)
		}},
		{"user store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, nil, units, institutions, audit, authorization, mail, policy, time.Minute, generator, time.Now)
		}},
		{"academic unit store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, nil, institutions, audit, authorization, mail, policy, time.Minute, generator, time.Now)
		}},
		{"institution store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, nil, audit, authorization, mail, policy, time.Minute, generator, time.Now)
		}},
		{"audit", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, institutions, nil, authorization, mail, policy, time.Minute, generator, time.Now)
		}},
		{"authorization", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, institutions, audit, nil, mail, policy, time.Minute, generator, time.Now)
		}},
		{"mail", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, institutions, audit, authorization, nil, policy, time.Minute, generator, time.Now)
		}},
		{"generator", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, institutions, audit, authorization, mail, policy, time.Minute, nil, time.Now)
		}},
		{"clock", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, users, units, institutions, audit, authorization, mail, policy, time.Minute, generator, nil)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.make(); err == nil {
				t.Fatal("nil dependency was accepted")
			}
		})
	}
}

func TestPersonalAccessTokenAdministrationDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	rootStoreType := reflect.TypeOf((*store.Store)(nil)).Elem()
	serviceType := reflect.TypeOf(personalAccessTokenAdministrationService{})
	for index := range serviceType.NumField() {
		field := serviceType.Field(index)
		if field.Type == rootStoreType || field.Type.Implements(rootStoreType) {
			t.Fatalf("field %q retains store.Store", field.Name)
		}
	}
}

func mustPersonalAccessTokenAdministrationService(
	t *testing.T,
	tokens store.PersonalAccessTokenStore,
	units store.AcademicUnitStore,
	institutions store.InstitutionStore,
	audit personalAccessTokenAuditor,
	now time.Time,
) *personalAccessTokenAdministrationService {
	t.Helper()
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, units, institutions, audit, &personalAccessTokenScopeAuthorizerFake{allowed: true},
		&personalAccessTokenMailPreparerFake{},
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type personalAccessTokenScopeAuthorizerFake struct {
	events  *[]string
	allowed bool
	err     error
}

func (a *personalAccessTokenScopeAuthorizerFake) CanDelegateActionsAtScope(
	context.Context,
	model.Principal,
	[]string,
	model.RoleScopeType,
	string,
) (bool, error) {
	if a.events != nil {
		*a.events = append(*a.events, "authorize_scopes")
	}
	return a.allowed, a.err
}

func TestPersonalAccessTokenAdministrationRendersPostgreSQLPreparedActionTime(t *testing.T) {
	t.Parallel()

	nodeAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	databaseAt := nodeAt.Add(2 * time.Hour)
	userID := model.NewUserID()
	token := personalAccessTokenForTest(userID, nodeAt)
	tokens := &personalAccessTokenStoreFake{current: token, preparedAt: databaseAt}
	sealer := mailTestSealer(t)
	mail, err := newDirectMailPreparer(personalAccessTokenActionTimeRenderer{}, &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, sealer)
	if err != nil {
		t.Fatal(err)
	}
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, &personalAccessTokenScopeAuthorizerFake{allowed: true}, mail,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return nodeAt },
	)
	if err != nil {
		t.Fatal(err)
	}
	_, appErr := service.SetDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, nodeAt), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: true})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if tokens.notice.Delivery == nil || len(tokens.notice.Delivery.EncryptedPayload) == 0 {
		t.Fatalf("prepared notice = %#v", tokens.notice)
	}
	var envelope secretseal.Envelope
	if err := json.Unmarshal(tokens.notice.Delivery.EncryptedPayload, &envelope); err != nil {
		t.Fatal(err)
	}
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: tokens.notice.Delivery.ID.String()}, envelope)
	if err != nil {
		t.Fatal(err)
	}
	var payload frozenMailPayloadV1
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Text, databaseAt.Format(time.RFC3339Nano)) {
		t.Fatalf("rendered text %q does not contain PostgreSQL action time %s", payload.Text, databaseAt)
	}
}

type personalAccessTokenActionTimeRenderer struct{}

func (personalAccessTokenActionTimeRenderer) Render(model.MailTemplateKey, string, string, string) (FrozenMailContent, error) {
	return FrozenMailContent{Subject: "notice", Text: "notice", HTML: "<p>notice</p>"}, nil
}

func (personalAccessTokenActionTimeRenderer) RenderPersonalAccessTokenSecurityNotice(_ model.MailTemplateKey, _, _ string, details PersonalAccessTokenMailDetails) (FrozenMailContent, error) {
	value := details.ActionAt.Format(time.RFC3339Nano)
	return FrozenMailContent{Subject: "PAT notice", Text: value, HTML: "<p>" + value + "</p>"}, nil
}

func (personalAccessTokenActionTimeRenderer) RenderExamManagerNotice(model.MailTemplateKey, string, string, ExamManagerMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, errors.New("unexpected Exam Manager render")
}

func (personalAccessTokenActionTimeRenderer) RenderClassTransitionNotice(model.MailTemplateKey, string, string, ClassTransitionMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, errors.New("unexpected Class transition render")
}

func (personalAccessTokenActionTimeRenderer) RenderSubmissionReceipt(model.MailTemplateKey, string, string, SubmissionReceiptMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, errors.New("unexpected Submission receipt render")
}

func TestPersonalAccessTokenAdministrationTerminalReplayIsSuccessful(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	token := personalAccessTokenForTest(userID, now)
	fresh := false
	tokens := &personalAccessTokenStoreFake{current: token, preparedAt: now, fresh: &fresh}
	service := mustPersonalAccessTokenAdministrationService(t, tokens, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, now)
	got, err := service.SetDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: true})
	if err != nil || got != token {
		t.Fatalf("SetDisabled(concurrent replay) = %#v, %v", got, err)
	}
}

func TestPersonalAccessTokenAdministrationTerminalizesPreparationWhenRenderingFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	userID := model.NewUserID()
	token := personalAccessTokenForTest(userID, now)
	tokens := &personalAccessTokenStoreFake{current: token, preparedAt: now}
	mail := &personalAccessTokenMailPreparerFake{err: errors.New("render unavailable")}
	service, err := newPersonalAccessTokenAdministrationService(tokens, &personalAccessTokenUserStoreFake{},
		&personalAccessTokenAcademicUnitStoreFake{}, &personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, &personalAccessTokenScopeAuthorizerFake{allowed: true}, mail,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, appErr := service.SetDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID.String(), Disabled: true}); !Is(appErr, "personal_access_token.unavailable") {
		t.Fatalf("SetDisabled(render failure) error = %v", appErr)
	}
	if tokens.failCalls != 1 || tokens.stateCalls != 0 {
		t.Fatalf("render failure terminalizations=%d state calls=%d", tokens.failCalls, tokens.stateCalls)
	}
}

func personalAccessTokenSessionPrincipal(userID model.UserID, authenticatedAt time.Time) model.Principal {
	return model.Principal{
		UserID: userID, SessionID: model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientWeb, AuthenticatedAt: authenticatedAt,
	}
}

func personalAccessTokenForTest(userID model.UserID, at time.Time) *model.PersonalAccessToken {
	token := &model.PersonalAccessToken{
		UserID: userID, Description: "automation", TokenHash: model.HashToken("credential"),
		Scopes: []string{string(model.ActionClassView)}, ExpiresAt: at.Add(time.Hour),
	}
	token.PrepareCreate(model.NewPersonalAccessTokenID(), at)
	return token
}

type personalAccessTokenStoreFake struct {
	store.PersonalAccessTokenStore
	events       *[]string
	saveAt       time.Time
	preparedAt   time.Time
	fresh        *bool
	candidate    *model.PersonalAccessToken
	saved        *model.PersonalAccessToken
	current      *model.PersonalAccessToken
	maximum      int
	stateMaximum int
	stateAt      int64
	revokedAt    int64
	saveCalls    int
	stateCalls   int
	revokeCalls  int
	prepareCalls int
	failCalls    int
	notice       store.PersonalAccessTokenSecurityNotice
}

func (s *personalAccessTokenStoreFake) PrepareMutation(_ context.Context, input *store.PersonalAccessTokenMutationPreparation) (*store.PreparedPersonalAccessTokenMutation, error) {
	if s.events != nil {
		*s.events = append(*s.events, "prepare")
	}
	s.prepareCalls++
	at := s.preparedAt
	if at.IsZero() {
		at = s.saveAt
	}
	if at.IsZero() && s.current != nil {
		at = s.current.CreatedAt
	}
	if at.IsZero() {
		at = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	}
	return &store.PreparedPersonalAccessTokenMutation{ID: model.NewId(), ActionAt: at, ExpiresAt: at.Add(input.Lifetime)}, nil
}

func (s *personalAccessTokenStoreFake) FailMutation(context.Context, *store.PersonalAccessTokenMutationFailure) error {
	s.failCalls++
	return nil
}

func (s *personalAccessTokenStoreFake) MaintainMutationPreparations(context.Context, int) (*store.PersonalAccessTokenPreparationMaintenanceResult, error) {
	return &store.PersonalAccessTokenPreparationMaintenanceResult{}, nil
}

func (s *personalAccessTokenStoreFake) Create(_ context.Context, input *store.PersonalAccessTokenCreationMutation) (*store.PersonalAccessTokenMutationResult, error) {
	if s.events != nil {
		*s.events = append(*s.events, "create")
	}
	s.saveCalls++
	s.candidate, s.maximum = input.Token, input.MaximumActive
	s.notice = input.Notice
	at := s.saveAt
	if at.IsZero() {
		at = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	}
	s.candidate.PrepareCreate(model.NewPersonalAccessTokenID(), at)
	s.saved = s.candidate
	return &store.PersonalAccessTokenMutationResult{Token: s.saved, Fresh: s.resultFresh()}, nil
}

func (s *personalAccessTokenStoreFake) Get(context.Context, string) (*model.PersonalAccessToken, error) {
	if s.current == nil {
		return nil, store.NewErrNotFound("personal_access_token", "")
	}
	return s.current, nil
}

func (s *personalAccessTokenStoreFake) ListByUser(context.Context, string) ([]*model.PersonalAccessToken, error) {
	return []*model.PersonalAccessToken{s.current}, nil
}

func (s *personalAccessTokenStoreFake) ChangeState(_ context.Context, input *store.PersonalAccessTokenStateMutation) (*store.PersonalAccessTokenMutationResult, error) {
	s.stateCalls++
	s.stateAt, s.stateMaximum = s.current.CreatedAt.UnixMilli(), input.MaximumActive
	s.notice = input.Notice
	if input.Disabled {
		s.current.DisabledAt = model.OptionalTimeFrom(s.current.CreatedAt)
	} else {
		s.current.DisabledAt = model.OptionalTime{}
	}
	return &store.PersonalAccessTokenMutationResult{Token: s.current, Fresh: s.resultFresh()}, nil
}

func (s *personalAccessTokenStoreFake) RevokeWithAudit(_ context.Context, input *store.PersonalAccessTokenRevocation) (*store.PersonalAccessTokenMutationResult, error) {
	s.revokeCalls++
	s.notice = input.Notice
	s.revokedAt = s.current.CreatedAt.UnixMilli()
	s.current.RevokedAt = model.OptionalTimeFrom(s.current.CreatedAt)
	return &store.PersonalAccessTokenMutationResult{Token: s.current, Fresh: s.resultFresh()}, nil
}

func (s *personalAccessTokenStoreFake) resultFresh() bool {
	return s.fresh == nil || *s.fresh
}

type personalAccessTokenUserStoreFake struct{}

func (*personalAccessTokenUserStoreFake) Get(_ context.Context, id string) (*model.User, error) {
	user := &model.User{
		ID: model.UserID(id), Username: "operator", Email: "operator@example.test",
		EmailVerified: true, DisplayName: "Operator", Locale: model.DefaultLocale,
	}
	user.PrepareCreate(model.UserID(id), time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	return user, nil
}

type personalAccessTokenMailPreparerFake struct {
	requests []personalAccessTokenSecurityNoticePreparation
	err      error
}

func (p *personalAccessTokenMailPreparerFake) PreparePersonalAccessTokenSecurityNotice(request personalAccessTokenSecurityNoticePreparation) (*preparedDirectMail, error) {
	p.requests = append(p.requests, request)
	if p.err != nil {
		return nil, p.err
	}
	return &preparedDirectMail{
		Occurrence: &model.MailOccurrence{ID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceSecurityNotice, TemplateKey: request.TemplateKey, ActorUserID: request.Recipient.ID, CreatedAt: request.ActionAt},
		Delivery:   &model.MailDelivery{ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), TargetUserID: request.Recipient.ID, TemplateKey: request.TemplateKey, Deadline: request.ActionAt.Add(24 * time.Hour)},
		Job:        &model.Job{ID: model.NewJobID(), Type: model.JobTypeMailDeliver},
	}, nil
}

type personalAccessTokenAcademicUnitStoreFake struct {
	store.AcademicUnitStore
	events *[]string
	unit   *model.AcademicUnit
}

func (s *personalAccessTokenAcademicUnitStoreFake) Get(context.Context, string) (*model.AcademicUnit, error) {
	if s.events != nil {
		*s.events = append(*s.events, "academic_unit")
	}
	if s.unit == nil {
		return nil, store.NewErrNotFound("academic_unit", "")
	}
	return s.unit, nil
}

type personalAccessTokenInstitutionStoreFake struct {
	store.InstitutionStore
	events      *[]string
	institution *model.Institution
}

func (s *personalAccessTokenInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	if s.events != nil {
		*s.events = append(*s.events, "institution")
	}
	if s.institution == nil {
		return nil, store.NewErrNotFound("institution", "")
	}
	return s.institution, nil
}

type personalAccessTokenAuditorFake struct {
	events     *[]string
	beginCalls int
	action     model.Action
	resource   model.Resource
	parameters map[string]any
	prior      map[string]any
	status     model.AuditStatus
	errorCode  string
	result     map[string]any
}

func (a *personalAccessTokenAuditorFake) Prepare(_ context.Context, principal model.Principal, action model.Action, resource model.Resource, _ model.RequestMetadata, parameters, prior map[string]any) (*model.AuditEvent, error) {
	if a.events != nil {
		*a.events = append(*a.events, "audit_begin")
	}
	a.beginCalls++
	a.action, a.resource, a.parameters, a.prior = action, resource, parameters, prior
	return &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: resource,
		ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Status: model.AuditStatusAttempt, NodeID: "test",
		ClientType: string(principal.ClientType), AuthMethod: principal.AuthenticationMethod}, nil
}
