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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPublicRegistrationPreparesOneAtomicUnverifiedLocalAccount(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	persistence := &publicRegistrationStoreFake{}
	mail := &publicRegistrationMailFake{enabled: true}
	service := newTestPublicRegistrationService(t, persistence, mail, at)
	rawToken := model.NewCredentialToken()
	service.newToken = func() string { return rawToken }

	err := service.Register(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{
		RequestID: "register-1", IPAddress: "192.0.2.10",
	}), RegisterLocalUserCommand{
		Username: "New.Student", Email: " New.Student@Example.EDU ",
		FirstName: "  New  ", LastName: " Student ", Password: "correct horse battery staple",
		Source: "192.0.2.10:443",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := persistence.input
	if input == nil || input.User == nil || input.PasswordCredential == nil || input.Settings == nil ||
		input.DefaultProfilePictureJob == nil || input.VerificationToken == nil || input.VerificationOccurrence == nil ||
		input.VerificationDelivery == nil || input.VerificationJob == nil || input.AuditEvent == nil {
		t.Fatalf("registration aggregate = %#v", input)
	}
	if input.User.Username != "new.student" || input.User.Email != "new.student@example.edu" ||
		input.User.FirstName != "New" || input.User.LastName != "Student" || input.User.DisplayName != "" || input.User.EmailVerified ||
		input.PasswordCredential.UserID != input.User.ID || input.Settings.UserID != input.User.ID ||
		input.VerificationToken.UserID != input.User.ID || input.VerificationToken.Target != input.User.Email ||
		input.VerificationToken.Purpose != model.UserTokenEmailVerification ||
		input.VerificationDelivery.TargetUserID != input.User.ID ||
		input.VerificationDelivery.TemplateKey != model.MailTemplateIdentityVerifyEmail ||
		input.VerificationJob.Type != model.JobTypeMailDeliverCredential {
		t.Fatalf("registration aggregate = %#v", input)
	}
	if input.AuditEvent.ActorID.IsValid() || input.AuditEvent.Action != publicRegistrationAuditAction ||
		strings.Contains(string(input.AuditEvent.Parameters), input.User.Email) || strings.Contains(string(input.AuditEvent.Parameters), "correct horse") {
		t.Fatalf("registration audit leaked private input: %#v", input.AuditEvent)
	}
	if mail.actionURL == "" || !strings.Contains(mail.actionURL, rawToken) ||
		strings.Contains(mail.actionURL, input.VerificationToken.TokenHash) || input.VerificationToken.TokenHash == rawToken ||
		strings.Contains(string(input.AuditEvent.Parameters), rawToken) || strings.Contains(string(input.AuditEvent.Result), rawToken) {
		t.Fatalf("verification action URL = %q", mail.actionURL)
	}
}

func TestPublicRegistrationFailsClosedBeforeAccountPreparationWhenPolicyOrMailDisallowsIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		policy   publicRegistrationPolicyFake
		mail     bool
		wantCode string
	}{
		{name: "public registration disabled", policy: publicRegistrationPolicyFake{local: true}, mail: true, wantCode: "authentication.registration.invitation_required"},
		{name: "local login disabled", policy: publicRegistrationPolicyFake{registration: true}, mail: true, wantCode: "authentication.registration.invitation_required"},
		{name: "mail disabled", policy: publicRegistrationPolicyFake{registration: true, local: true}, wantCode: "authentication.registration.unavailable"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			persistence := &publicRegistrationStoreFake{}
			mail := &publicRegistrationMailFake{enabled: test.mail}
			service := newTestPublicRegistrationService(t, persistence, mail, time.Now())
			service.policies = test.policy
			err := service.Register(context.Background(), Invocation{}, RegisterLocalUserCommand{
				Username: "student", Email: "student@example.edu", FirstName: "New", LastName: "Student",
				Password: "correct horse battery staple", Source: "192.0.2.20",
			})
			if !Is(err, test.wantCode) {
				t.Fatalf("Register() = %v, want %s", err, test.wantCode)
			}
			if persistence.input != nil || mail.prepareCalls != 0 {
				t.Fatalf("disabled registration reached preparation: input=%#v mail_calls=%d", persistence.input, mail.prepareCalls)
			}
		})
	}
}

func TestPublicRegistrationConcealsDuplicateIdentity(t *testing.T) {
	t.Parallel()

	for _, constraint := range []string{"users_username_key", "users_email_key"} {
		constraint := constraint
		t.Run(constraint, func(t *testing.T) {
			t.Parallel()
			persistence := &publicRegistrationStoreFake{err: store.NewErrConflict("user", constraint, nil)}
			service := newTestPublicRegistrationService(t, persistence, &publicRegistrationMailFake{enabled: true}, time.Now())
			if err := service.Register(context.Background(), Invocation{}, RegisterLocalUserCommand{
				Username: "student", Email: "student@example.edu", FirstName: "New", LastName: "Student",
				Password: "correct horse battery staple", Source: "192.0.2.30",
			}); err != nil {
				t.Fatalf("duplicate registration disclosed its outcome: %v", err)
			}
		})
	}
}

func TestPublicRegistrationDoesNotConcealUnrelatedPersistenceConflicts(t *testing.T) {
	t.Parallel()

	for _, conflict := range []*store.ErrConflict{
		store.NewErrConflict("job", "jobs_pkey", nil),
		store.NewErrConflict("user_token", "user_tokens_pkey", nil),
		store.NewErrConflict("mail_delivery", "mail_deliveries_pkey", nil),
		store.NewErrConflict("user", "users_pkey", nil),
	} {
		conflict := conflict
		t.Run(conflict.Resource+"/"+conflict.Constraint, func(t *testing.T) {
			t.Parallel()
			persistence := &publicRegistrationStoreFake{err: conflict}
			service := newTestPublicRegistrationService(t, persistence, &publicRegistrationMailFake{enabled: true}, time.Now())
			err := service.Register(context.Background(), Invocation{}, RegisterLocalUserCommand{
				Username: "student", Email: "student@example.edu", FirstName: "New", LastName: "Student",
				Password: "correct horse battery staple", Source: "192.0.2.30",
			})
			if !Is(err, "authentication.registration.unavailable") {
				t.Fatalf("unrelated persistence conflict = %v, want authentication.registration.unavailable", err)
			}
		})
	}
}

func TestPublicRegistrationMailAndPersistenceFailuresCreateNoPartialApplicationState(t *testing.T) {
	t.Parallel()

	mailFailureStore := &publicRegistrationStoreFake{}
	mailFailure := newTestPublicRegistrationService(t, mailFailureStore, &publicRegistrationMailFake{
		enabled: true, err: errors.New("seal failed"),
	}, time.Now())
	command := RegisterLocalUserCommand{
		Username: "student", Email: "student@example.edu", FirstName: "New", LastName: "Student",
		Password: "correct horse battery staple", Source: "192.0.2.31",
	}
	if err := mailFailure.Register(context.Background(), Invocation{}, command); !Is(err, "authentication.registration.unavailable") {
		t.Fatalf("mail preparation failure = %v", err)
	}
	if mailFailureStore.input != nil {
		t.Fatalf("mail preparation failure reached Store: %#v", mailFailureStore.input)
	}

	persistenceFailure := newTestPublicRegistrationService(t, &publicRegistrationStoreFake{err: errors.New("database unavailable")}, &publicRegistrationMailFake{enabled: true}, time.Now())
	if err := persistenceFailure.Register(context.Background(), Invocation{}, command); !Is(err, "authentication.registration.unavailable") {
		t.Fatalf("persistence failure = %v", err)
	}
}

func TestPublicRegistrationRejectsAnInvalidPasswordBeforeMailOrPersistence(t *testing.T) {
	t.Parallel()

	persistence := &publicRegistrationStoreFake{}
	mail := &publicRegistrationMailFake{enabled: true}
	service := newTestPublicRegistrationService(t, persistence, mail, time.Now())
	err := service.Register(context.Background(), Invocation{}, RegisterLocalUserCommand{
		Username: "student", Email: "student@example.edu", FirstName: "New", LastName: "Student",
		Password: "", Source: "192.0.2.32",
	})
	if !Is(err, "authentication.password.invalid") {
		t.Fatalf("invalid password = %v", err)
	}
	if persistence.input != nil || mail.prepareCalls != 0 {
		t.Fatalf("invalid password reached mail/Store: calls=%d input=%#v", mail.prepareCalls, persistence.input)
	}
}

func TestPublicRegistrationRequiresFirstAndLastNamesBeforeCredentialPreparation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		firstName string
		lastName  string
	}{
		{name: "missing first name", lastName: "Student"},
		{name: "missing last name", firstName: "New"},
		{name: "whitespace names", firstName: "  ", lastName: "\t"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			persistence := &publicRegistrationStoreFake{}
			mail := &publicRegistrationMailFake{enabled: true}
			service := newTestPublicRegistrationService(t, persistence, mail, time.Now())
			err := service.Register(context.Background(), Invocation{}, RegisterLocalUserCommand{
				Username: "student", Email: "student@example.edu", FirstName: test.firstName,
				LastName: test.lastName, Password: "correct horse battery staple", Source: "192.0.2.33",
			})
			if !Is(err, "authentication.registration.invalid") {
				t.Fatalf("missing personal name = %v", err)
			}
			if persistence.input != nil || mail.prepareCalls != 0 {
				t.Fatalf("invalid personal name reached mail/Store: calls=%d input=%#v", mail.prepareCalls, persistence.input)
			}
		})
	}
}

func TestPublicRegistrationUsesPrivateSharedAttemptAccounting(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	service := newTestPublicRegistrationService(t, &publicRegistrationStoreFake{}, &publicRegistrationMailFake{enabled: true}, time.Now())
	service.attempts = mustAuthenticationAttemptAccounting(t, cache)
	service.rateLimit = LoginRateLimitPolicy{Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 10}
	command := RegisterLocalUserCommand{Username: "student", Email: " Student@Example.EDU ", FirstName: "New", LastName: "Student", Password: "correct horse battery staple", Source: "192.0.2.40:443"}
	if err := service.Register(context.Background(), Invocation{}, command); err != nil {
		t.Fatal(err)
	}
	command.Email = "student@example.edu"
	if err := service.Register(context.Background(), Invocation{}, command); !Is(err, "authentication.rate_limited") {
		t.Fatalf("second registration = %v", err)
	}
	for key := range cache.snapshot() {
		if !strings.HasPrefix(key, "authentication/attempts/public-registration/") || strings.Contains(strings.ToLower(key), "student") || strings.Contains(key, "192.0.2.40") {
			t.Fatalf("unsafe registration attempt key = %q", key)
		}
	}
}

type publicRegistrationStoreFake struct {
	input *store.PublicLocalUserRegistration
	err   error
}

func (s *publicRegistrationStoreFake) RegisterLocal(_ context.Context, input *store.PublicLocalUserRegistration) (*store.PublicLocalUserRegistrationResult, error) {
	s.input = input
	if s.err != nil {
		return nil, s.err
	}
	return &store.PublicLocalUserRegistrationResult{User: input.User, Token: input.VerificationToken}, nil
}

type publicRegistrationPolicyFake struct {
	registration bool
	local        bool
	err          error
}

func (p publicRegistrationPolicyFake) PublicRegistration(context.Context) (bool, bool, error) {
	return p.registration, p.local, p.err
}

type publicRegistrationMailFake struct {
	enabled      bool
	prepareCalls int
	actionURL    string
	err          error
}

func (m *publicRegistrationMailFake) Enabled() bool { return m.enabled }

func (m *publicRegistrationMailFake) PreparePublicRegistrationVerification(recipient *model.User, occurrenceID model.MailOccurrenceID, actionURL string, at, deadline time.Time) (*preparedDirectMail, error) {
	m.prepareCalls++
	m.actionURL = actionURL
	if m.err != nil {
		return nil, m.err
	}
	if !m.enabled {
		return nil, errors.New("mail disabled")
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityVerifyEmail,
		ActorUserID: recipient.ID, CreatedAt: at}
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrence.ID, JobID: jobID,
		TargetUserID: recipient.ID, TemplateKey: model.MailTemplateIdentityVerifyEmail, State: model.MailDeliveryQueued,
		CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline, Revision: 1}
	job := &model.Job{ID: jobID, Type: model.JobTypeMailDeliverCredential, Status: model.JobStatusQueued}
	return &preparedDirectMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func newTestPublicRegistrationService(t *testing.T, persistence publicRegistrationStore, mail publicRegistrationVerificationPreparer, at time.Time) *publicRegistrationService {
	t.Helper()
	service, err := newPublicRegistrationService(publicRegistrationDependencies{
		registrations: persistence,
		policies:      publicRegistrationPolicyFake{registration: true, local: true},
		institutions:  publicRegistrationInstitutionFake{id: model.NewInstitutionID()},
		mail:          mail,
		attempts:      mustAuthenticationAttemptAccounting(t, newExpiringAuthenticationAttemptCache(time.Now)),
		hasher:        &registrationPasswordHasherFake{},
		rateLimit:     LoginRateLimitPolicy{Window: time.Minute, MaximumAttempts: 10, MaximumSourceAttempts: 100},
		tokenTTL:      time.Hour,
		publicURL:     "https://proctor.example.test",
		nodeID:        "registration-test-node",
		newToken:      model.NewCredentialToken,
		now:           func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type publicRegistrationInstitutionFake struct{ id model.InstitutionID }

func (f publicRegistrationInstitutionFake) InstitutionID(context.Context) (model.InstitutionID, error) {
	return f.id, nil
}

type registrationPasswordHasherFake struct{}

func (*registrationPasswordHasherFake) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("empty password")
	}
	return "$argon2id$registration-test", nil
}
