// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMFAChallengeRejectsReplayedRecoveryCodeAndPreservesEffectOrdering(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 10, 15, 0, 0, time.UTC)
	principal := mfaTestPrincipal(now, model.AuthenticationSingleFactor)
	events := []string{}
	persistence := &mfaApplicationStoreFake{
		events: &events,
		credential: &model.MFACredential{
			ID: model.NewMFACredentialID(), UserID: principal.UserID,
			State: model.MFAStateActive,
		},
		session: &model.Session{
			ID: principal.SessionID, UserID: principal.UserID,
			AuthenticationStrength: model.AuthenticationMultiFactor,
			MFACompletedAt:         model.OptionalTimeFrom(now),
		},
		hashes: []string{"access-hash"},
	}
	audit := &mfaApplicationAuditFake{events: &events}
	effects := &mfaApplicationEffectsFake{events: &events}
	service := newTestMFAApplicationService(t, persistence, audit, effects, now)
	application := &App{mfaApplication: service}
	invocation := NewInvocation(principal, model.RequestMetadata{RequestID: "request-a"})
	command := ChallengeMFACommand{Code: "aaaa-aaaa-aaaa-aaaa-aaaa-aaaa"}

	if _, err := application.ChallengeMFA(context.Background(), invocation, command); err != nil {
		t.Fatal(err)
	}
	if _, err := application.ChallengeMFA(context.Background(), invocation, command); !Is(err, "authentication.mfa.invalid_code") {
		t.Fatalf("replayed challenge error = %v, want authentication.mfa.invalid_code", err)
	}

	want := []string{
		"institution", "audit_begin", "credential_get", "consume", "upgrade",
		"effects", "session_get", "audit_success",
		"institution", "audit_begin", "credential_get", "consume", "audit_fail",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if effects.userID != principal.UserID.String() ||
		!reflect.DeepEqual(effects.sessionIDs, []string{principal.SessionID.String()}) ||
		!reflect.DeepEqual(effects.hashes, []string{"access-hash"}) {
		t.Fatalf("effects = %#v", effects)
	}
}

func TestMFASetupUsesControlledClockAndSecretGenerator(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 10, 20, 0, 0, time.UTC)
	principal := mfaTestPrincipal(now, model.AuthenticationSingleFactor)
	events := []string{}
	persistence := &mfaApplicationStoreFake{events: &events}
	audit := &mfaApplicationAuditFake{events: &events}
	service := newTestMFAApplicationService(
		t, persistence, audit, &mfaApplicationEffectsFake{}, now,
	)
	const secret = "JBSWY3DPEHPK3PXP"
	service.mechanics.newSecret = func() (string, error) { return secret, nil }
	application := &App{mfaApplication: service}

	setup, err := application.SetupMFA(
		context.Background(),
		NewInvocation(principal, model.RequestMetadata{}),
		SetupMFACommand{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Secret != secret || setup.ExpiresAt != now.Add(10*time.Minute) {
		t.Fatalf("setup = %#v", setup)
	}
	if persistence.savedPending == nil ||
		persistence.savedPending.CreatedAt != now ||
		persistence.savedPending.PendingExpiresAt.Time != now.Add(10*time.Minute) ||
		persistence.savedPending.EncryptedSecret == secret {
		t.Fatalf("pending credential = %#v", persistence.savedPending)
	}
	if want := []string{"institution", "audit_begin", "save_pending", "audit_success"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestMFARecoveryCodeLoginConsumptionIsSerialized(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	userID := model.NewUserID()
	persistence := &mfaApplicationStoreFake{
		events: &[]string{},
		credential: &model.MFACredential{
			ID: model.NewMFACredentialID(), UserID: userID, State: model.MFAStateActive,
		},
	}
	service := newTestMFAApplicationService(
		t, persistence, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now,
	)
	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _, err := service.VerifyLogin(
				context.Background(), userID.String(),
				"aaaa-aaaa-aaaa-aaaa-aaaa-aaaa", now,
			)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case Is(err, "authentication.mfa.invalid_code"):
			rejected++
		default:
			t.Fatalf("VerifyLogin error = %v", err)
		}
	}
	if successes != 1 || rejected != contenders-1 {
		t.Fatalf("successes=%d rejected=%d", successes, rejected)
	}
}

func TestMFASecurityTransitionsPrepareOneOrdinaryNoticeWithoutSecrets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	principal := mfaTestPrincipal(now, model.AuthenticationMultiFactor)
	persistence := &mfaApplicationStoreFake{
		session: &model.Session{ID: principal.SessionID, UserID: principal.UserID},
	}
	mailer := &mfaSecurityNoticeMailPreparerFake{}
	service := newTestMFAApplicationService(t, persistence, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now)
	service.mail = mailer
	application := &App{mfaApplication: service}
	invocation := NewInvocation(principal, model.RequestMetadata{RequestID: "mfa-notices"})
	const secret = "JBSWY3DPEHPK3PXP"
	encrypted, err := service.mechanics.encrypt(principal.UserID.String(), secret)
	if err != nil {
		t.Fatal(err)
	}
	persistence.credential = &model.MFACredential{
		ID: model.NewMFACredentialID(), UserID: principal.UserID,
		State: model.MFAStatePending, EncryptedSecret: encrypted,
		EncryptionKeyID: service.mechanics.primary, CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute), PendingExpiresAt: model.OptionalTimeFrom(now.Add(time.Minute)),
	}
	code, err := computeTOTP(secret, now.Unix()/30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.ActivateMFA(context.Background(), invocation, ActivateMFACommand{Code: code}); err != nil {
		t.Fatal(err)
	}

	if _, err := application.RegenerateMFARecoveryCodes(context.Background(), invocation, RegenerateMFARecoveryCodesCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := application.DisableMFA(context.Background(), invocation, DisableMFACommand{}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.requests) != 3 {
		t.Fatalf("mail preparations = %d, want 3", len(mailer.requests))
	}
	for index, want := range []model.MailTemplateKey{
		model.MailTemplateIdentityMFAEnabled,
		model.MailTemplateIdentityMFARecoveryCodesRegenerated,
		model.MailTemplateIdentityMFADisabled,
	} {
		request := mailer.requests[index]
		if request.TemplateKey != want || request.Recipient.ID != principal.UserID ||
			!request.At.Equal(now) {
			t.Fatalf("mail request %d = %#v", index, request)
		}
	}
}

func TestMFAApplicationServiceRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	mechanics := mustTestMFAMechanics(t)
	persistence := &mfaApplicationStoreFake{}
	users := mfaApplicationUserStoreFake{}
	sessions := mfaApplicationSessionStoreFake{persistence: persistence}
	institutions := mfaApplicationInstitutionStoreFake{persistence: persistence}
	audit := &mfaApplicationAuditFake{}
	effects := &mfaApplicationEffectsFake{}
	mail := &mfaSecurityNoticeMailPreparerFake{}
	now := time.Now
	tests := []struct {
		name  string
		build func() (*mfaApplicationService, error)
	}{
		{"user store", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(nil, persistence, sessions, institutions, audit, effects, mail, mechanics, time.Minute, now)
		}},
		{"MFA store", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, nil, sessions, institutions, audit, effects, mail, mechanics, time.Minute, now)
		}},
		{"session store", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, nil, institutions, audit, effects, mail, mechanics, time.Minute, now)
		}},
		{"institution store", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, nil, audit, effects, mail, mechanics, time.Minute, now)
		}},
		{"audit", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, nil, effects, mail, mechanics, time.Minute, now)
		}},
		{"effects", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, audit, nil, mail, mechanics, time.Minute, now)
		}},
		{"mail", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, audit, effects, nil, mechanics, time.Minute, now)
		}},
		{"mechanics", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, audit, effects, mail, nil, time.Minute, now)
		}},
		{"recent authentication TTL", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, audit, effects, mail, mechanics, 0, now)
		}},
		{"clock", func() (*mfaApplicationService, error) {
			return newMFAApplicationService(users, persistence, sessions, institutions, audit, effects, mail, mechanics, time.Minute, nil)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.build(); err == nil {
				t.Fatal("nil dependency was accepted")
			}
		})
	}
}

func newTestMFAApplicationService(
	t *testing.T,
	persistence *mfaApplicationStoreFake,
	audit mfaAudit,
	effects mfaEffects,
	now time.Time,
) *mfaApplicationService {
	t.Helper()
	service, err := newMFAApplicationService(
		mfaApplicationUserStoreFake{}, persistence,
		mfaApplicationSessionStoreFake{persistence: persistence},
		mfaApplicationInstitutionStoreFake{persistence: persistence}, audit, effects,
		&mfaSecurityNoticeMailPreparerFake{}, mustTestMFAMechanics(t), 15*time.Minute, func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mustTestMFAMechanics(t *testing.T) *mfaMechanics {
	t.Helper()
	mechanics, err := newMFAMechanics(MFAPolicy{
		Enabled: true, Issuer: "Proctor", SetupTTL: 10 * time.Minute,
		RecoveryCodeCount: 5,
		EncryptionKey:     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return mechanics
}

func mfaTestPrincipal(at time.Time, strength model.AuthenticationStrength) model.Principal {
	principal := selfSessionPrincipal(at.Add(-time.Minute))
	principal.AuthenticationStrength = strength
	if strength == model.AuthenticationMultiFactor {
		principal.MFACompletedAt = model.OptionalTimeFrom(at.Add(-30 * time.Second))
	}
	return principal
}

type mfaApplicationStoreFake struct {
	store.MFAStore

	mu             sync.Mutex
	events         *[]string
	credential     *model.MFACredential
	savedPending   *model.MFACredential
	session        *model.Session
	hashes         []string
	consumed       bool
	upgradeCalls   int
	credentialGets int
	activation     *store.MFAActivationMutation
	regeneration   *store.MFARecoveryCodesRegeneration
	disablement    *store.MFADisablement
}

func (s *mfaApplicationStoreFake) appendEvent(event string) {
	if s.events != nil {
		*s.events = append(*s.events, event)
	}
}

func (s *mfaApplicationStoreFake) GetByUser(context.Context, string) (*model.MFACredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentialGets++
	s.appendEvent("credential_get")
	if s.credential == nil {
		return nil, store.NewErrNotFound("mfa_credential", "")
	}
	return s.credential, nil
}

func (s *mfaApplicationStoreFake) SavePending(_ context.Context, candidate *model.MFACredential) (*model.MFACredential, error) {
	s.appendEvent("save_pending")
	saved := *candidate
	saved.PrepareCreate(model.NewMFACredentialID(), candidate.CreatedAt)
	s.savedPending = &saved
	return &saved, nil
}

func (s *mfaApplicationStoreFake) ConsumeSecondFactor(context.Context, string, int64, string, int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEvent("consume")
	if s.consumed {
		return store.NewErrNotFound("mfa_recovery_code", "")
	}
	s.consumed = true
	return nil
}

func (s *mfaApplicationStoreFake) UpgradeSession(context.Context, string, string, int64) ([]string, error) {
	s.upgradeCalls++
	s.appendEvent("upgrade")
	return append([]string(nil), s.hashes...), nil
}

func (s *mfaApplicationStoreFake) Activate(_ context.Context, input *store.MFAActivationMutation) (*store.MFAActivationResult, error) {
	s.activation = input
	active := *s.credential
	active.State = model.MFAStateActive
	return &store.MFAActivationResult{Credential: &active, Session: s.session, AccessTokenHashes: append([]string(nil), s.hashes...)}, nil
}

func (s *mfaApplicationStoreFake) ReplaceRecoveryCodes(_ context.Context, input *store.MFARecoveryCodesRegeneration) error {
	s.regeneration = input
	return nil
}

func (s *mfaApplicationStoreFake) Disable(_ context.Context, input *store.MFADisablement) (*store.MFADisableResult, error) {
	s.disablement = input
	return &store.MFADisableResult{AccessTokenHashes: append([]string(nil), s.hashes...)}, nil
}

type mfaApplicationUserStoreFake struct{ store.UserStore }

func (mfaApplicationUserStoreFake) Get(_ context.Context, id string) (*model.User, error) {
	return &model.User{ID: model.UserID(id), Email: "user@example.edu"}, nil
}

type mfaSecurityNoticeMailPreparerFake struct {
	requests []securityNoticePreparation
}

func (m *mfaSecurityNoticeMailPreparerFake) PrepareSecurityNotice(request securityNoticePreparation) (*preparedDirectMail, error) {
	m.requests = append(m.requests, request)
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	return &preparedDirectMail{
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSecurityNotice, TemplateKey: request.TemplateKey, ActorUserID: request.Recipient.ID, CreatedAt: request.At},
		Delivery:   &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: request.Recipient.ID, TemplateKey: request.TemplateKey, Deadline: request.At.Add(securityNoticeDeliveryLifetime)},
		Job:        &model.Job{ID: jobID, Type: model.JobTypeMailDeliver},
	}, nil
}

type mfaApplicationSessionStoreFake struct {
	store.SessionStore
	persistence *mfaApplicationStoreFake
}

func (s mfaApplicationSessionStoreFake) Get(context.Context, string) (*model.Session, error) {
	s.persistence.appendEvent("session_get")
	return s.persistence.session, nil
}

type mfaApplicationInstitutionStoreFake struct {
	store.InstitutionStore
	persistence *mfaApplicationStoreFake
}

func (s mfaApplicationInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	s.persistence.appendEvent("institution")
	return &model.Institution{ID: model.NewInstitutionID()}, nil
}

type mfaApplicationAuditFake struct{ events *[]string }

func (a *mfaApplicationAuditFake) appendEvent(event string) {
	if a.events != nil {
		*a.events = append(*a.events, event)
	}
}

func (a *mfaApplicationAuditFake) Begin(context.Context, Invocation, model.Action, model.Resource, any) (string, error) {
	a.appendEvent("audit_begin")
	return model.NewAuditEventID().String(), nil
}

func (a *mfaApplicationAuditFake) Complete(_ context.Context, _ string, status model.AuditStatus, _ string, _ any) error {
	if status == model.AuditStatusSuccess {
		a.appendEvent("audit_success")
	} else {
		a.appendEvent("audit_fail")
	}
	return nil
}

type mfaApplicationEffectsFake struct {
	events     *[]string
	userID     string
	sessionIDs []string
	hashes     []string
}

func (e *mfaApplicationEffectsFake) SessionsRevoked(_ context.Context, userID string, sessionIDs, hashes []string) {
	if e.events != nil {
		*e.events = append(*e.events, "effects")
	}
	e.userID = userID
	e.sessionIDs = append([]string(nil), sessionIDs...)
	e.hashes = append([]string(nil), hashes...)
}
