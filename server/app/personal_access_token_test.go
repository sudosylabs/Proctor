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
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenScopesRejectRelationshipOnlyParticipationAction(t *testing.T) {
	t.Parallel()

	_, err := normalizePersonalAccessTokenScopes([]string{string(model.ActionExamSittingParticipate)})
	require.Error(t, err, "normalizePersonalAccessTokenScopes() error")
	require.True(t, apperror.Is(err, "personal_access_token.invalid"), "normalizePersonalAccessTokenScopes() error")
}

func TestPersonalAccessTokenScopesRejectInteractiveOnlyAdministration(t *testing.T) {
	t.Parallel()

	for _, action := range []model.Action{
		model.ActionAccessPolicyManage,
		model.ActionExternalIdentityManage,
		model.ActionRoleBindingManage,
		model.ActionRoleManage,
	} {
		_, err := normalizePersonalAccessTokenScopes([]string{string(action)})
		require.True(t, apperror.Is(err, "personal_access_token.invalid"), "scope %q error = %v, want personal_access_token.invalid", action, err)
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
	require.NoError(t, err)
	_, err = service.CreatePersonalAccessToken(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}),
		CreatePersonalAccessTokenCommand{
			Scopes: []string{string(model.ActionClassManage)}, AcademicUnitID: unitID.String(),
			ExpiresAt: now.Add(time.Hour).UnixMilli(),
		},
	)
	require.True(t, apperror.Is(err, "personal_access_token.invalid"), "error = %v, want personal_access_token.invalid", err)
	require.Equal(t, 0, tokens.saveCalls, "save calls")
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
	require.NoError(t, err)

	created, err := service.CreatePersonalAccessToken(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{RequestID: "request-a"}),
		CreatePersonalAccessTokenCommand{
			Description: "automation", Scopes: []string{string(model.ActionClassView)},
			AcademicUnitID: unitID.String(), ExpiresAt: now.Add(time.Hour).UnixMilli(),
		},
	)
	require.NoError(t, err)
	require.Equal(t, raw, created.Credential)
	require.Same(t, tokens.saved, created.Token)
	require.Equal(t, model.HashToken(raw), tokens.candidate.TokenHash)
	require.NotEqual(t, raw, tokens.candidate.TokenHash)
	require.Equal(t, userID, tokens.candidate.UserID, "save candidate = %#v, maximum", tokens.candidate)
	require.Equal(t, 3, tokens.maximum, "save candidate = %#v, maximum", tokens.candidate)
	require.Len(t, mail.requests, 1, "mail requests")
	request := mail.requests[0]
	require.Equal(t, model.MailTemplateIdentityPersonalAccessTokenCreated, request.TemplateKey)
	require.Equal(t, "automation", request.Description)
	require.Equal(t, 1, request.ActionCount)
	require.True(t, request.AcademicUnitScoped)
	require.True(t, request.ExpiresAt.Equal(now.Add(time.Hour)))
	require.True(t, request.ActionAt.Equal(now))
	got := fmt.Sprintf("%#v", request)
	require.NotContains(t, got, raw)
	require.NotContains(t, got, tokens.candidate.TokenHash)
	require.NotContains(t, got, string(model.ActionClassView))
	require.Equal(t, model.ActionPersonalAccessTokenCreate, audit.action, "audit action")
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, audit.resource, "audit action")
	require.NotContains(t, fmt.Sprintf("%#v %#v %#v", audit.parameters, audit.prior, audit.result), raw)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"academic_unit", "authorize_scopes", "institution", "audit_begin", "prepare", "create"}, events)
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
	require.NoError(t, err)

	got, appErr := service.SetPersonalAccessTokenDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: true})
	require.NoError(t, appErr, "SetDisabled(replay)")
	require.Same(t, token, got, "SetDisabled(replay)")
	require.Equal(t, 0, audit.beginCalls, "replay audit")
	require.Empty(t, mail.requests, "replay audit")
	require.Equal(t, 0, tokens.stateCalls, "replay audit")

	token.RevokedAt = model.OptionalTimeFrom(now)
	got, appErr = service.RevokePersonalAccessToken(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), RevokePersonalAccessTokenCommand{TokenID: token.ID})
	require.NoError(t, appErr, "Revoke(replay)")
	require.Same(t, token, got, "Revoke(replay)")
	require.Equal(t, 0, audit.beginCalls, "replay audit")
	require.Empty(t, mail.requests, "replay audit")
	require.Equal(t, 0, tokens.revokeCalls, "replay audit")
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

	_, err := service.RevokePersonalAccessToken(
		context.Background(),
		NewInvocation(personalAccessTokenSessionPrincipal(callerID, now), model.RequestMetadata{}),
		RevokePersonalAccessTokenCommand{TokenID: token.ID},
	)
	require.True(t, apperror.Is(err, "resource.not_found"), "error = %v, want resource.not_found", err)
	require.Equal(t, 0, tokens.revokeCalls, "revoke calls")
	require.Equal(t, 0, audit.beginCalls, "revoke calls")
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
	require.NoError(t, err)
	invocation := NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{})

	_, err = service.SetPersonalAccessTokenDisabled(
		context.Background(), invocation,
		SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: true},
	)
	require.NoError(t, err)
	require.Equal(t, now.UnixMilli(), tokens.stateAt, "state change at")
	require.Equal(t, 3, tokens.stateMaximum, "state change at")
	_, err = service.SetPersonalAccessTokenDisabled(
		context.Background(), invocation,
		SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: false},
	)
	require.NoError(t, err)

	tokens.current = token
	_, err = service.RevokePersonalAccessToken(
		context.Background(), invocation,
		RevokePersonalAccessTokenCommand{TokenID: token.ID},
	)
	require.NoError(t, err)
	require.Equal(t, now.UnixMilli(), tokens.revokedAt, "revoked at")
	wantKeys := []model.MailTemplateKey{
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked,
	}
	require.Len(t, mail.requests, len(wantKeys), "mail requests")
	for index, key := range wantKeys {
		require.Equal(t, key, mail.requests[index].TemplateKey, "mail request %d key", index)
	}
}

func TestPersonalAccessTokenAdministrationRequiresRecentSessionForCreationAndEnablement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 123701000, time.FixedZone("test", 2*60*60))
	userID := model.NewUserID()
	tokens := &personalAccessTokenStoreFake{events: &[]string{}}
	service := mustPersonalAccessTokenAdministrationService(
		t, tokens, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{}, &personalAccessTokenAuditorFake{}, now,
	)
	stale := personalAccessTokenSessionPrincipal(userID, now.Add(-15*time.Minute-time.Microsecond))
	invocation := NewInvocation(stale, model.RequestMetadata{})

	_, err := service.CreatePersonalAccessToken(context.Background(), invocation, CreatePersonalAccessTokenCommand{})
	require.True(t, apperror.Is(err, "authentication.reauthentication_required"), "create error = %v", err)
	_, err = service.SetPersonalAccessTokenDisabled(context.Background(), invocation, SetPersonalAccessTokenDisabledCommand{
		TokenID: model.NewPersonalAccessTokenID(), Disabled: false,
	})
	require.True(t, apperror.Is(err, "authentication.reauthentication_required"), "enable error = %v", err)
	require.Equal(t, 0, tokens.saveCalls, "store calls: save")
	require.Equal(t, 0, tokens.stateCalls, "store calls: save")
}

func TestPersonalAccessTokenAdministrationJudgesRecencyByInjectedClock(t *testing.T) {
	t.Parallel()

	// The injected clock is decades away from wall time, so a recency decision
	// made against time.Now would reject the fresh principal and accept the
	// stale one.
	now := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	userID := model.NewUserID()
	service := mustPersonalAccessTokenAdministrationService(
		t, &personalAccessTokenStoreFake{events: &[]string{}}, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{}, &personalAccessTokenAuditorFake{}, now,
	)
	fresh := NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{})
	_, err := service.CreatePersonalAccessToken(context.Background(), fresh, CreatePersonalAccessTokenCommand{})
	require.True(t, apperror.Is(err, "personal_access_token.invalid"), "fresh create error = %v, want the expires_at validation after the recency check", err)
	stale := NewInvocation(personalAccessTokenSessionPrincipal(userID, now.Add(-16*time.Minute)), model.RequestMetadata{})
	_, err = service.CreatePersonalAccessToken(context.Background(), stale, CreatePersonalAccessTokenCommand{})
	require.True(t, apperror.Is(err, "authentication.reauthentication_required"), "stale create error = %v", err)
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
			_, err := test.make()
			require.Error(t, err, "nil dependency was accepted")
		})
	}
}

func TestPersonalAccessTokenAdministrationDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	rootStoreType := reflect.TypeOf((*store.Store)(nil)).Elem()
	serviceType := reflect.TypeOf(personalAccessTokenAdministrationService{})
	for index := range serviceType.NumField() {
		field := serviceType.Field(index)
		require.NotEqual(t, rootStoreType, field.Type, "field %q retains store.Store", field.Name)
		require.False(t, field.Type.Implements(rootStoreType), "field %q retains store.Store", field.Name)
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, &personalAccessTokenUserStoreFake{}, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, &personalAccessTokenScopeAuthorizerFake{allowed: true}, mail,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return nodeAt },
	)
	require.NoError(t, err)
	_, appErr := service.SetPersonalAccessTokenDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, nodeAt), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: true})
	require.NoError(t, appErr)
	require.NotNil(t, tokens.notice.Delivery)
	require.NotEmpty(t, tokens.notice.Delivery.EncryptedPayload)
	payload, err := appmail.OpenDelivery(sealer, tokens.notice.Delivery)
	require.NoError(t, err)
	require.Contains(t, payload.Text, databaseAt.Format(time.RFC3339Nano), "rendered text %q does not contain PostgreSQL action time %s", payload.Text, databaseAt)
}

type personalAccessTokenActionTimeRenderer struct{}

func (personalAccessTokenActionTimeRenderer) Render(request MailRenderRequest) (FrozenMailContent, error) {
	details, ok := request.Presentation.(PersonalAccessTokenMailDetails)
	if !ok {
		return FrozenMailContent{}, errors.New("unexpected mail presentation")
	}
	value := details.ActionAt.Format(time.RFC3339Nano)
	return FrozenMailContent{Subject: "PAT notice", Text: value, HTML: "<p>" + value + "</p>"}, nil
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
	got, err := service.SetPersonalAccessTokenDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: true})
	require.NoError(t, err, "SetDisabled(concurrent replay)")
	require.Same(t, token, got, "SetDisabled(concurrent replay)")
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
	require.NoError(t, err)
	_, appErr := service.SetPersonalAccessTokenDisabled(context.Background(), NewInvocation(personalAccessTokenSessionPrincipal(userID, now), model.RequestMetadata{}), SetPersonalAccessTokenDisabledCommand{TokenID: token.ID, Disabled: true})
	require.True(t, apperror.Is(appErr, "personal_access_token.unavailable"), "SetDisabled(render failure) error = %v", appErr)
	require.Equal(t, 1, tokens.failCalls, "render failure terminalizations")
	require.Equal(t, 0, tokens.stateCalls, "render failure terminalizations")
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
	return &store.PreparedPersonalAccessTokenMutation{ID: model.NewPersonalAccessTokenPreparationID(), ActionAt: at, ExpiresAt: at.Add(input.Lifetime)}, nil
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

func (s *personalAccessTokenStoreFake) Get(context.Context, model.PersonalAccessTokenID) (*model.PersonalAccessToken, error) {
	if s.current == nil {
		return nil, store.NewErrNotFound("personal_access_token", "")
	}
	return s.current, nil
}

func (s *personalAccessTokenStoreFake) ListByUser(context.Context, model.UserID) ([]*model.PersonalAccessToken, error) {
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

func (s *personalAccessTokenStoreFake) Revoke(_ context.Context, input *store.PersonalAccessTokenRevocation) (*store.PersonalAccessTokenMutationResult, error) {
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

func (*personalAccessTokenUserStoreFake) Get(_ context.Context, id model.UserID) (*model.User, error) {
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

func (s *personalAccessTokenAcademicUnitStoreFake) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
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
