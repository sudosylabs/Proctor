// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

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
	service, err := newPersonalAccessTokenAdministrationService(
		tokens, units, institutions, audit,
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
	if audit.action != actionPersonalAccessTokenCreate ||
		audit.resource != (model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}) {
		t.Fatalf("audit action=%q resource=%#v", audit.action, audit.resource)
	}
	if audit.status != model.AuditStatusSuccess || audit.errorCode != "" {
		t.Fatalf("audit completion status=%q error=%q", audit.status, audit.errorCode)
	}
	if got := fmt.Sprintf("%#v %#v %#v", audit.parameters, audit.prior, audit.result); strings.Contains(got, raw) {
		t.Fatalf("raw credential entered audit data: %s", got)
	}
	if want := []string{"academic_unit", "institution", "audit_begin", "save", "audit_complete"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
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
	service := mustPersonalAccessTokenAdministrationService(
		t, tokens, &personalAccessTokenAcademicUnitStoreFake{},
		&personalAccessTokenInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&personalAccessTokenAuditorFake{}, now,
	)
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
	units := &personalAccessTokenAcademicUnitStoreFake{}
	institutions := &personalAccessTokenInstitutionStoreFake{}
	audit := &personalAccessTokenAuditorFake{}
	policy := PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: time.Hour, MaximumPerUser: 1}
	generator := func() string { return "credential" }

	tests := []struct {
		name string
		make func() (*personalAccessTokenAdministrationService, error)
	}{
		{"token store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(nil, units, institutions, audit, policy, time.Minute, generator, time.Now)
		}},
		{"academic unit store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, nil, institutions, audit, policy, time.Minute, generator, time.Now)
		}},
		{"institution store", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, units, nil, audit, policy, time.Minute, generator, time.Now)
		}},
		{"audit", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, units, institutions, nil, policy, time.Minute, generator, time.Now)
		}},
		{"generator", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, units, institutions, audit, policy, time.Minute, nil, time.Now)
		}},
		{"clock", func() (*personalAccessTokenAdministrationService, error) {
			return newPersonalAccessTokenAdministrationService(tokens, units, institutions, audit, policy, time.Minute, generator, nil)
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
		tokens, units, institutions, audit,
		PersonalAccessTokenPolicy{MinimumLifetime: time.Minute, MaximumLifetime: 24 * time.Hour, MaximumPerUser: 3},
		15*time.Minute, func() string { return "credential" }, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
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
}

func (s *personalAccessTokenStoreFake) Save(_ context.Context, token *model.PersonalAccessToken, maximum int) (*model.PersonalAccessToken, error) {
	if s.events != nil {
		*s.events = append(*s.events, "save")
	}
	s.saveCalls++
	s.candidate, s.maximum = token, maximum
	token.PrepareCreate(model.NewPersonalAccessTokenID(), s.saveAt)
	s.saved = token
	return token, nil
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

func (s *personalAccessTokenStoreFake) SetDisabled(_ context.Context, _ string, _ string, disabled bool, at int64, maximum int) (*model.PersonalAccessToken, error) {
	s.stateCalls++
	s.stateAt, s.stateMaximum = at, maximum
	if disabled {
		s.current.DisabledAt = model.OptionalTimeFrom(model.TimeFromMillis(at))
	} else {
		s.current.DisabledAt = model.OptionalTime{}
	}
	return s.current, nil
}

func (s *personalAccessTokenStoreFake) Revoke(_ context.Context, _ string, _ string, at int64) (*model.PersonalAccessToken, error) {
	s.revokeCalls++
	s.revokedAt = at
	s.current.RevokedAt = model.OptionalTimeFrom(model.TimeFromMillis(at))
	return s.current, nil
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

func (a *personalAccessTokenAuditorFake) Begin(_ context.Context, _ model.Principal, action model.Action, resource model.Resource, _ model.RequestMetadata, parameters, prior map[string]any) (string, error) {
	if a.events != nil {
		*a.events = append(*a.events, "audit_begin")
	}
	a.beginCalls++
	a.action, a.resource, a.parameters, a.prior = action, resource, parameters, prior
	return model.NewAuditEventID().String(), nil
}

func (a *personalAccessTokenAuditorFake) Complete(_ context.Context, _ string, status model.AuditStatus, errorCode string, result map[string]any) error {
	if a.events != nil {
		*a.events = append(*a.events, "audit_complete")
	}
	a.status, a.errorCode, a.result = status, errorCode, result
	return nil
}
