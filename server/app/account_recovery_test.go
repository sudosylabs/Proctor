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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
)

func TestAccountTokenServiceDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	typeOfStore := reflect.TypeOf((*store.Store)(nil)).Elem()
	typeOfService := reflect.TypeOf(accountTokenService{})
	for i := 0; i < typeOfService.NumField(); i++ {
		field := typeOfService.Field(i)
		require.NotEqual(t, typeOfStore, field.Type, "account token service field %q retains root store", field.Name)
		require.False(t, field.Type.Implements(typeOfStore), "account token service field %q retains root store", field.Name)
	}
}

func TestAccountTokenServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remove func(*accountTokenConstructorArgs)
	}{
		{"user store", func(a *accountTokenConstructorArgs) { a.users = nil }},
		{"password store", func(a *accountTokenConstructorArgs) { a.passwords = nil }},
		{"token store", func(a *accountTokenConstructorArgs) { a.tokens = nil }},
		{"institution store", func(a *accountTokenConstructorArgs) { a.institutions = nil }},
		{"mailer", func(a *accountTokenConstructorArgs) { a.mailer = nil }},
		{"attempt accounting", func(a *accountTokenConstructorArgs) { a.attempts = nil }},
		{"hasher", func(a *accountTokenConstructorArgs) { a.hasher = nil }},
		{"audit", func(a *accountTokenConstructorArgs) { a.audit = accountTokenAuditRecorder{} }},
		{"effects", func(a *accountTokenConstructorArgs) { a.effects = nil }},
		{"diagnostics", func(a *accountTokenConstructorArgs) { a.diagnostics = nil }},
		{"generator", func(a *accountTokenConstructorArgs) { a.newToken = nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := validAccountTokenConstructorArgs()
			test.remove(&args)
			_, err := args.build()
			require.Error(t, err, "constructor accepted missing dependency")
		})
	}
}

func TestAccountRecoveryAttemptAccountingIsolatesAndNormalizesOperations(t *testing.T) {
	t.Parallel()

	cache := newExpiringAuthenticationAttemptCache(time.Now)
	service := &accountTokenService{
		attempts: mustAuthenticationAttemptAccounting(t, cache),
		policy: AccountRecoveryPolicy{RateLimit: LoginRateLimitPolicy{
			Window: time.Minute, MaximumAttempts: 10, MaximumSourceAttempts: 100,
		}},
	}
	operations := []accountRecoveryAttemptOperation{
		accountRecoveryAttemptEmailVerificationRequest,
		accountRecoveryAttemptPasswordResetRequest,
		accountRecoveryAttemptEmailVerificationComplete,
		accountRecoveryAttemptPasswordResetComplete,
	}
	for _, operation := range operations {
		require.NoError(t, service.checkAccountRecoveryRateLimit(
			context.Background(), operation, " Student@Example.EDU ", " Example.COM:443 ",
		))
		require.NoError(t, service.checkAccountRecoveryRateLimit(
			context.Background(), operation, "student@example.edu", "example.com:8443",
		))
	}

	entries := cache.snapshot()
	require.Len(t, entries, len(operations)*2, "recovery counters")
	for key, entry := range entries {
		require.True(t, strings.HasPrefix(key, "authentication/attempts/account-recovery/"), "counter key = %q", key)
		require.NotContains(t, strings.ToLower(key), "student")
		require.NotContains(t, strings.ToLower(key), "example")
		require.NotContains(t, key, "verification")
		require.NotContains(t, key, "reset")
		require.Equal(t, int64(2), entry.count, "counter %q", key)
	}
}

func TestAccountRecoveryAttemptAccountingUsesExactThresholdForEveryOperation(t *testing.T) {
	t.Parallel()

	operations := []accountRecoveryAttemptOperation{
		accountRecoveryAttemptEmailVerificationRequest,
		accountRecoveryAttemptPasswordResetRequest,
		accountRecoveryAttemptEmailVerificationComplete,
		accountRecoveryAttemptPasswordResetComplete,
	}
	for _, operation := range operations {
		operation := operation
		t.Run(operationNameForTest(operation), func(t *testing.T) {
			t.Parallel()
			service := &accountTokenService{
				attempts: mustAuthenticationAttemptAccounting(
					t, newExpiringAuthenticationAttemptCache(time.Now),
				),
				policy: AccountRecoveryPolicy{RateLimit: LoginRateLimitPolicy{
					Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 100,
				}},
			}
			require.NoError(t, service.checkAccountRecoveryRateLimit(
				context.Background(), operation, "student@example.edu", "192.0.2.10:443",
			))
			err := service.checkAccountRecoveryRateLimit(
				context.Background(), operation, "student@example.edu", "192.0.2.10:443",
			)
			require.True(t, apperror.Is(err, "authentication.rate_limited"), "attempt beyond maximum = %v", err)
		})
	}
}

func TestAccountRecoveryAttemptAccountingFailsClosedWithoutCounterRollback(t *testing.T) {
	t.Parallel()

	for _, failAt := range []int{1, 2} {
		failAt := failAt
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			t.Parallel()
			cache := &faultingAuthenticationAttemptCache{
				expiringAuthenticationAttemptCache: newExpiringAuthenticationAttemptCache(time.Now),
				failAt:                             failAt,
			}
			service := &accountTokenService{
				attempts: mustAuthenticationAttemptAccounting(t, cache),
				policy: AccountRecoveryPolicy{RateLimit: LoginRateLimitPolicy{
					Window: time.Minute, MaximumAttempts: 10, MaximumSourceAttempts: 100,
				}},
			}
			err := service.checkAccountRecoveryRateLimit(
				context.Background(), accountRecoveryAttemptPasswordResetRequest,
				"student@example.edu", "192.0.2.10:443",
			)
			require.True(t, apperror.Is(err, "authentication.rate_limit_unavailable"), "cache failure = %v", err)
			require.Len(t, cache.calls, failAt, "counter calls")
			require.Contains(t, cache.calls[0], "/identity/", "first counter")
			entries := cache.snapshot()
			if failAt == 1 {
				require.Empty(t, entries, "first-counter failure retained")
			}
			if failAt == 2 {
				require.Contains(t, cache.calls[1], "/source/", "second counter")
				require.Len(t, entries, 1, "second-counter failure retained")
			}
		})
	}
}

func operationNameForTest(operation accountRecoveryAttemptOperation) string {
	name, valid := operation.qualifier()
	if !valid {
		return "invalid"
	}
	return name
}

func TestRequestPasswordResetPreservesGenericResponseAndIssuedToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 9, 30, 0, 0, time.UTC)
	user := &model.User{
		ID: model.NewUserID(), Email: "student@example.edu", DisplayName: "Student",
	}
	institution := &model.Institution{ID: model.NewInstitutionID()}
	tokens := &accountTokenStoreFake{}
	mailer := &accountTokenMailerFake{enabled: true}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byEmail: user},
		passwords: &accountTokenPasswordStoreFake{
			credential: &model.PasswordCredential{UserID: user.ID},
		},
		tokens: tokens, institution: institution, mailer: mailer,
		now: func() time.Time { return now },
	})

	err := app.RequestPasswordReset(context.Background(), Invocation{}, RequestPasswordResetCommand{
		Email: "  STUDENT@EXAMPLE.EDU  ", Source: "192.0.2.10",
	})
	require.NoError(t, err)
	require.NotNil(t, tokens.issued, "password-reset token was not issued")
	require.Equal(t, "student@example.edu", tokens.issued.Target)
	require.Equal(t, model.UserTokenPasswordReset, tokens.issued.Purpose)
	require.True(t, tokens.issued.ExpiresAt.Equal(now.Add(45*time.Minute)), "token expiry = %s", tokens.issued.ExpiresAt)
	require.NotNil(t, tokens.event)
	require.Equal(t, string(model.ActionAuthenticationPasswordResetRequest), tokens.event.Action)
	require.Len(t, mailer.messages, 1)
	require.Contains(t, mailer.messages[0], "#token="+accountTokenTestRawToken)

	unknown := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users:     &accountTokenUserStoreFake{byEmailErr: store.NewErrNotFound("user", "")},
		passwords: &accountTokenPasswordStoreFake{}, tokens: &accountTokenStoreFake{},
		institution: institution, mailer: &accountTokenMailerFake{enabled: true},
		now: func() time.Time { return now },
	})
	require.NoError(t, unknown.RequestPasswordReset(
		context.Background(), Invocation{},
		RequestPasswordResetCommand{Email: "unknown@example.edu"},
	))
}

func TestRequestPasswordResetReportsMailUnavailableWhenLocalLoginRemainsEnabled(t *testing.T) {
	t.Parallel()

	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens: &accountTokenStoreFake{}, institution: &model.Institution{ID: model.NewInstitutionID()},
		mailer: &accountTokenMailerFake{enabled: false},
	})
	err := app.RequestPasswordReset(context.Background(), Invocation{}, RequestPasswordResetCommand{
		Email: "student@example.edu", Source: "192.0.2.10",
	})
	require.True(t, apperror.Is(err, "authentication.account_recovery.unavailable"), "enabled-local disabled-mail error = %v", err)
}

func TestRequestPasswordResetConcealsAtomicEnqueueFailure(t *testing.T) {
	t.Parallel()

	user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
	tokens := &accountTokenStoreFake{issueErr: errors.New("database unavailable")}
	mailer := &accountTokenMailerFake{enabled: true}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users:     &accountTokenUserStoreFake{byEmail: user},
		passwords: &accountTokenPasswordStoreFake{credential: &model.PasswordCredential{UserID: user.ID}},
		tokens:    tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: mailer,
	})
	require.NoError(t, app.RequestPasswordReset(context.Background(), Invocation{}, RequestPasswordResetCommand{Email: user.Email}))
	require.NotNil(t, tokens.issuedMail, "prepared durable reset aggregate")
	require.Equal(t, model.JobTypeMailDeliverCredential, tokens.issuedMail.Job.Type, "prepared durable reset aggregate")
	require.Len(t, mailer.messages, 1, "prepared durable reset aggregate")
}

func TestRequestPasswordResetCreatesNoIntentForDisabledOrExternalOnlyAccount(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		user     *model.User
		password *accountTokenPasswordStoreFake
	}{
		{name: "disabled", user: &model.User{ID: model.NewUserID(), Email: "disabled@example.edu", DisabledAt: model.OptionalTimeFrom(time.Now())}, password: &accountTokenPasswordStoreFake{credential: &model.PasswordCredential{UserID: model.NewUserID()}}},
		{name: "external-only", user: &model.User{ID: model.NewUserID(), Email: "external@example.edu"}, password: &accountTokenPasswordStoreFake{err: store.NewErrNotFound("password_credential", "")}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tokens := &accountTokenStoreFake{}
			mailer := &accountTokenMailerFake{enabled: true}
			app := newAccountTokenTestApp(t, accountTokenTestDependencies{
				users: &accountTokenUserStoreFake{byEmail: test.user}, passwords: test.password,
				tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: mailer,
			})
			require.NoError(t, app.RequestPasswordReset(context.Background(), Invocation{}, RequestPasswordResetCommand{Email: test.user.Email}))
			require.Nil(t, tokens.issuedMail, "ineligible account intent")
			require.Empty(t, mailer.messages, "ineligible account intent")
		})
	}
}

func TestRequestPasswordResetIsGenericAndInertWhenCurrentPolicyDisablesLocalLogin(t *testing.T) {
	t.Parallel()

	user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
	tokens := &accountTokenStoreFake{}
	mailer := &accountTokenMailerFake{enabled: false}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byEmail: user}, passwords: &accountTokenPasswordStoreFake{
			credential: &model.PasswordCredential{UserID: user.ID},
		}, tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: mailer,
		accessPolicy: authenticationAccessPolicyFake{local: false},
	})
	require.NoError(t, app.RequestPasswordReset(context.Background(), Invocation{}, RequestPasswordResetCommand{
		Email: user.Email, Source: "192.0.2.7",
	}))
	require.Nil(t, tokens.issued, "disabled local recovery issued")
	require.Empty(t, mailer.messages, "disabled local recovery issued")
}

func TestCompletePasswordResetCommitsBeforeEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
	session := &model.Session{ID: model.NewSessionID(), UserID: user.ID}
	order := make([]string, 0, 2)
	tokens := &accountTokenStoreFake{
		byHash: &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset},
		consumeReset: func(hash, passwordHash string, at int64, event *model.AuditEvent) (*store.PasswordResetResult, error) {
			order = append(order, "commit")
			require.Equal(t, model.HashToken(accountTokenTestRawToken), hash)
			require.Equal(t, "encoded-new-password", passwordHash)
			require.Equal(t, now.UnixMilli(), at, "consume time")
			require.NotNil(t, event)
			require.Equal(t, string(model.ActionAuthenticationPasswordResetComplete), event.Action)
			return &store.PasswordResetResult{
				User: user, RevokedSessions: []*model.Session{session},
				RevokedAccessHashes: []string{"access-hash"},
			}, nil
		},
	}
	effects := &accountTokenEffectsFake{called: func(userID string, sessionIDs, hashes []string) {
		order = append(order, "effects")
		require.Equal(t, user.ID.String(), userID, "effects = user")
		require.Equal(t, []string{session.ID.String()}, sessionIDs, "effects = user")
		require.Equal(t, []string{"access-hash"}, hashes, "effects = user")
	}}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{},
		tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()},
		mailer: &accountTokenMailerFake{enabled: true}, effects: effects,
		hasher: accountTokenHasherFake{hash: "encoded-new-password"},
		now:    func() time.Time { return now },
	})

	got, err := app.CompletePasswordReset(
		context.Background(), Invocation{},
		CompletePasswordResetCommand{Token: accountTokenTestRawToken, Password: "new password"},
	)
	require.NoError(t, err, "complete password reset")
	require.Same(t, user, got, "complete password reset")
	// Contract (guardrail 14): the reset commits before its effects.
	ordertest.RequireInOrder(t, order, "commit", "effects")
}

func TestCompletePasswordResetMapsTerminalAccessPolicyFenceToInvalidCredential(t *testing.T) {
	t.Parallel()

	user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{},
		tokens: &accountTokenStoreFake{byHash: &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset}, consumeReset: func(string, string, int64, *model.AuditEvent) (*store.PasswordResetResult, error) {
			return nil, store.ErrAuthenticationMethodDisabled
		}},
		institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &accountTokenMailerFake{enabled: true},
	})
	result, err := app.CompletePasswordReset(context.Background(), Invocation{}, CompletePasswordResetCommand{
		Token: accountTokenTestRawToken, Password: "NewCorrectHorseBatteryStaple1!", Source: "192.0.2.8",
	})
	require.Nil(t, result, "result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.account_token.invalid"), "result=%#v err=%v", result, err)
}

func TestAccountTokenCompletionConcealsTerminalTokenStates(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"expired", "superseded", "consumed", "concurrent-loser"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			app := newAccountTokenTestApp(t, accountTokenTestDependencies{
				users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
				tokens:      &accountTokenStoreFake{consumeVerificationErr: store.NewErrNotFound("user_token", state)},
				institution: &model.Institution{ID: model.NewInstitutionID()},
				mailer:      &accountTokenMailerFake{enabled: true},
			})
			_, err := app.CompleteEmailVerification(
				context.Background(), Invocation{},
				CompleteEmailVerificationCommand{Token: accountTokenTestRawToken},
			)
			var appErr *apperror.Error
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, "authentication.account_token.invalid", appErr.Code())
		})
	}
}

func TestAccountTokenCompletionRejectsMalformedCredentialGenerically(t *testing.T) {
	t.Parallel()

	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens:      &accountTokenStoreFake{},
		institution: &model.Institution{ID: model.NewInstitutionID()},
		mailer:      &accountTokenMailerFake{enabled: true},
	})
	_, err := app.CompleteEmailVerification(
		context.Background(), Invocation{},
		CompleteEmailVerificationCommand{Token: "malformed"},
	)
	require.True(t, apperror.Is(err, "authentication.account_token.invalid"), "completion error = %v", err)
}

func TestPasswordResetWorkFailuresDoNotConsumeResetToken(t *testing.T) {
	testPasswordHashFailures(t, "authentication.account_recovery.unavailable", func(t *testing.T, ctx context.Context, hasher passwordHash) error {
		consumed := false
		user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
		tokens := &accountTokenStoreFake{byHash: &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset},
			consumeReset: func(string, string, int64, *model.AuditEvent) (*store.PasswordResetResult, error) {
				consumed = true
				return nil, errors.New("password reset should not be reached")
			}}
		application := newAccountTokenTestApp(t, accountTokenTestDependencies{
			users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{}, tokens: tokens,
			institution: &model.Institution{ID: model.NewInstitutionID()},
			mailer:      &accountTokenMailerFake{enabled: true}, hasher: hasher,
		})
		user, err := application.CompletePasswordReset(ctx, Invocation{}, CompletePasswordResetCommand{
			Token: accountTokenTestRawToken, Password: "correct horse battery staple", Source: "192.0.2.34",
		})
		require.Nil(t, user, "failed password work reached password reset mutation")
		require.False(t, consumed, "failed password work reached password reset mutation")
		require.Nil(t, tokens.event, "failed password work reached password reset mutation")
		return err
	})
}

const accountTokenTestRawToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type accountTokenTestDependencies struct {
	users        store.UserStore
	passwords    store.PasswordCredentialStore
	tokens       store.UserTokenStore
	institution  *model.Institution
	mailer       accountTokenMailPreparer
	effects      accountTokenEffects
	hasher       accountTokenPasswordHasher
	attempts     *authenticationAttemptAccounting
	rateLimit    LoginRateLimitPolicy
	now          func() time.Time
	accessPolicy authenticationAccessPolicy
}

func newAccountTokenTestApp(t *testing.T, deps accountTokenTestDependencies) *App {
	t.Helper()
	if deps.effects == nil {
		deps.effects = &accountTokenEffectsFake{}
	}
	if deps.hasher == nil {
		deps.hasher = accountTokenHasherFake{hash: "encoded-password"}
	}
	if deps.attempts == nil {
		deps.attempts = mustAuthenticationAttemptAccounting(
			t, newExpiringAuthenticationAttemptCache(time.Now),
		)
	}
	if deps.rateLimit == (LoginRateLimitPolicy{}) {
		deps.rateLimit = LoginRateLimitPolicy{
			Window: time.Minute, MaximumAttempts: 20, MaximumSourceAttempts: 100,
		}
	}
	if deps.now == nil {
		deps.now = func() time.Time { return time.UnixMilli(1) }
	}
	if deps.accessPolicy == nil {
		deps.accessPolicy = allowAllAuthenticationAccessPolicy()
	}
	service, err := newAccountTokenService(
		deps.users,
		deps.passwords,
		deps.tokens,
		deps.accessPolicy,
		&accountTokenInstitutionStoreFake{institution: deps.institution},
		deps.mailer,
		deps.attempts,
		deps.hasher,
		accountTokenAuditRecorder{nodeID: "node-1"},
		deps.effects,
		accountTokenDiagnosticsFake{},
		AccountRecoveryPolicy{
			EmailVerificationTTL: 24 * time.Hour,
			PasswordResetTTL:     45 * time.Minute,
			RateLimit:            deps.rateLimit,
		},
		"https://proctor.example.edu",
		func() string { return accountTokenTestRawToken },
		deps.now,
	)
	require.NoError(t, err)
	return &App{accountTokenService: service}
}

type accountTokenUserStoreFake struct {
	store.UserStore
	byID       *model.User
	byIDErr    error
	byEmail    *model.User
	byEmailErr error
}

func (s *accountTokenUserStoreFake) Get(context.Context, model.UserID) (*model.User, error) {
	return s.byID, s.byIDErr
}

func (s *accountTokenUserStoreFake) GetByEmail(context.Context, string) (*model.User, error) {
	return s.byEmail, s.byEmailErr
}

type accountTokenPasswordStoreFake struct {
	store.PasswordCredentialStore
	credential *model.PasswordCredential
	err        error
}

func (s *accountTokenPasswordStoreFake) GetByUser(context.Context, model.UserID) (*model.PasswordCredential, error) {
	return s.credential, s.err
}

type accountTokenStoreFake struct {
	store.UserTokenStore
	issued                 *model.UserToken
	issuedMail             *store.UserTokenMailIssue
	event                  *model.AuditEvent
	issueErr               error
	byHash                 *model.UserToken
	byHashErr              error
	consumeVerificationErr error
	consumeReset           func(string, string, int64, *model.AuditEvent) (*store.PasswordResetResult, error)
	emailChange            *store.UserEmailChange
	privilegedVerification *store.PrivilegedEmailVerification
}

func (s *accountTokenStoreFake) ChangeEmail(_ context.Context, input *store.UserEmailChange) (*store.UserEmailChangeResult, error) {
	s.emailChange = input
	user := &model.User{ID: input.UserID, Email: input.NewEmail}
	return &store.UserEmailChangeResult{User: user, Token: input.Token}, nil
}

func (s *accountTokenStoreFake) VerifyEmailPrivileged(_ context.Context, input *store.PrivilegedEmailVerification) (*model.User, error) {
	s.privilegedVerification = input
	return &model.User{ID: input.UserID, EmailVerified: true}, nil
}

func (s *accountTokenStoreFake) Issue(
	_ context.Context,
	input *store.UserTokenMailIssue,
) (*model.UserToken, error) {
	s.issued = input.Token
	s.issuedMail = input
	s.event = input.AuditEvent
	return input.Token, s.issueErr
}

func (s *accountTokenStoreFake) GetByHash(context.Context, string, model.UserTokenPurpose) (*model.UserToken, error) {
	if s.byHashErr != nil {
		return nil, s.byHashErr
	}
	if s.byHash == nil {
		return nil, store.NewErrNotFound("user_token", "")
	}
	return s.byHash, nil
}

func (s *accountTokenStoreFake) ConsumeEmailVerification(
	context.Context,
	string,
	int64,
	*model.AuditEvent,
) (*store.EmailVerificationResult, error) {
	if s.consumeVerificationErr != nil {
		return nil, s.consumeVerificationErr
	}
	return &store.EmailVerificationResult{User: &model.User{ID: model.NewUserID()}}, nil
}

func (s *accountTokenStoreFake) ConsumePasswordReset(
	_ context.Context,
	input *store.PasswordResetCompletion,
) (*store.PasswordResetResult, error) {
	if s.consumeReset == nil {
		return nil, store.NewErrNotFound("user_token", "")
	}
	return s.consumeReset(input.TokenHash, input.PasswordHash, input.At, input.AuditEvent)
}

type accountTokenInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
	err         error
}

func (s *accountTokenInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, s.err
}

type accountTokenMailerFake struct {
	enabled  bool
	messages []string
	err      error
}

func (m *accountTokenMailerFake) Enabled() bool { return m.enabled }

func (m *accountTokenMailerFake) prepareAccountToken(request AccountTokenMailPreparation, key model.MailTemplateKey) (*preparedDirectMail, error) {
	m.messages = append(m.messages, request.ActionURL)
	if m.err != nil {
		return nil, m.err
	}
	return &preparedDirectMail{
		Occurrence: &model.MailOccurrence{ID: request.OccurrenceID, Kind: model.MailOccurrenceAccountToken, TemplateKey: key, CreatedAt: request.At},
		Delivery:   &model.MailDelivery{Deadline: request.Deadline},
		Job:        &model.Job{Type: model.JobTypeMailDeliverCredential},
	}, nil
}

func (m *accountTokenMailerFake) PrepareEmailVerification(request AccountTokenMailPreparation) (*preparedDirectMail, error) {
	return m.prepareAccountToken(request, model.MailTemplateIdentityVerifyEmail)
}
func (m *accountTokenMailerFake) PreparePasswordReset(request AccountTokenMailPreparation) (*preparedDirectMail, error) {
	return m.prepareAccountToken(request, model.MailTemplateIdentityPasswordReset)
}
func (m *accountTokenMailerFake) PreparePasswordChanged(request NoticeMailPreparation) (*preparedDirectMail, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &preparedDirectMail{Occurrence: &model.MailOccurrence{ID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceSecurityNotice,
		TemplateKey: model.MailTemplateIdentityPasswordChanged, CreatedAt: request.At}, Delivery: &model.MailDelivery{Deadline: request.At.Add(24 * time.Hour)},
		Job: &model.Job{Type: model.JobTypeMailDeliver}}, nil
}
func (m *accountTokenMailerFake) PrepareEmailChangeWarning(NoticeMailPreparation) (*preparedDirectMail, error) {
	return nil, errors.New("unexpected email change warning")
}
func (m *accountTokenMailerFake) PrepareEmailChangeVerification(AccountTokenMailPreparation) (*preparedDirectMail, error) {
	return nil, errors.New("unexpected email change verification")
}
func (m *accountTokenMailerFake) PrepareEmailVerifiedByAdministrator(NoticeMailPreparation) (*preparedDirectMail, error) {
	return nil, errors.New("unexpected privileged email verification")
}

type accountTokenHasherFake struct {
	hash string
	err  error
}

func (f accountTokenHasherFake) Hash(context.Context, string) (string, error) { return f.hash, f.err }

type accountTokenEffectsFake struct {
	called func(string, []string, []string)
}

func (f *accountTokenEffectsFake) SessionsRevoked(
	_ context.Context,
	userID string,
	sessionIDs []string,
	hashes []string,
) {
	if f.called != nil {
		f.called(userID, sessionIDs, hashes)
	}
}

type accountTokenDiagnosticsFake struct{}

func (accountTokenDiagnosticsFake) ErrorContext(context.Context, string, error) {}

type accountTokenConstructorArgs struct {
	users        store.UserStore
	passwords    store.PasswordCredentialStore
	tokens       store.UserTokenStore
	institutions store.InstitutionStore
	mailer       accountTokenMailPreparer
	attempts     *authenticationAttemptAccounting
	hasher       accountTokenPasswordHasher
	audit        accountTokenAuditRecorder
	effects      accountTokenEffects
	diagnostics  recoveryDiagnostics
	newToken     func() string
}

func validAccountTokenConstructorArgs() accountTokenConstructorArgs {
	return accountTokenConstructorArgs{
		users:        &accountTokenUserStoreFake{},
		passwords:    &accountTokenPasswordStoreFake{},
		tokens:       &accountTokenStoreFake{},
		institutions: &accountTokenInstitutionStoreFake{},
		mailer:       &accountTokenMailerFake{enabled: true},
		attempts:     &authenticationAttemptAccounting{cache: newExpiringAuthenticationAttemptCache(time.Now)},
		hasher:       accountTokenHasherFake{},
		audit:        accountTokenAuditRecorder{nodeID: "node-1"},
		effects:      &accountTokenEffectsFake{},
		diagnostics:  accountTokenDiagnosticsFake{},
		newToken:     func() string { return accountTokenTestRawToken },
	}
}

func (a accountTokenConstructorArgs) build() (*accountTokenService, error) {
	return newAccountTokenService(
		a.users, a.passwords, a.tokens, allowAllAuthenticationAccessPolicy(), a.institutions, a.mailer, a.attempts,
		a.hasher, a.audit, a.effects, a.diagnostics, AccountRecoveryPolicy{},
		"https://proctor.example.edu", a.newToken, time.Now,
	)
}

type countingAccountTokenHasher struct{ calls *int }

func (h countingAccountTokenHasher) Hash(context.Context, string) (string, error) {
	*h.calls++
	return "encoded-password", nil
}

func TestCompletePasswordResetUnknownTokenDoesNotSpendPasswordWork(t *testing.T) {
	t.Parallel()

	hashes := 0
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens:      &accountTokenStoreFake{},
		institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &accountTokenMailerFake{enabled: true},
		hasher: countingAccountTokenHasher{calls: &hashes},
	})
	result, err := app.CompletePasswordReset(context.Background(), Invocation{}, CompletePasswordResetCommand{
		Token: model.NewCredentialToken(), Password: "NewCorrectHorseBatteryStaple1!", Source: "192.0.2.9",
	})
	require.Nil(t, result, "result=%#v err=%v", result, err)
	require.True(t, apperror.Is(err, "authentication.account_token.invalid"), "result=%#v err=%v", result, err)
	require.Equal(t, 0, hashes, "unknown reset token spent")
}
