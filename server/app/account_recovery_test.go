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

func TestAccountTokenServiceDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	typeOfStore := reflect.TypeOf((*store.Store)(nil)).Elem()
	typeOfService := reflect.TypeOf(accountTokenService{})
	for i := 0; i < typeOfService.NumField(); i++ {
		field := typeOfService.Field(i)
		if field.Type == typeOfStore || field.Type.Implements(typeOfStore) {
			t.Fatalf("account token service field %q retains root store", field.Name)
		}
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
		{"rate limiter", func(a *accountTokenConstructorArgs) { a.rateLimiter = nil }},
		{"hasher", func(a *accountTokenConstructorArgs) { a.hasher = nil }},
		{"audit", func(a *accountTokenConstructorArgs) { a.audit = nil }},
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
			if _, err := args.build(); err == nil {
				t.Fatal("constructor accepted missing dependency")
			}
		})
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if tokens.issued == nil {
		t.Fatal("password-reset token was not issued")
	}
	if tokens.issued.Target != "student@example.edu" {
		t.Fatalf("token target = %q", tokens.issued.Target)
	}
	if tokens.issued.Purpose != model.UserTokenPasswordReset {
		t.Fatalf("token purpose = %q", tokens.issued.Purpose)
	}
	if want := now.Add(45 * time.Minute); !tokens.issued.ExpiresAt.Equal(want) {
		t.Fatalf("token expiry = %s, want %s", tokens.issued.ExpiresAt, want)
	}
	if tokens.event == nil || tokens.event.Action != auditPasswordResetRequest {
		t.Fatalf("issue audit = %#v", tokens.event)
	}
	if len(mailer.messages) != 1 || !strings.Contains(mailer.messages[0], "#token="+accountTokenTestRawToken) {
		t.Fatalf("credential mail = %#v", mailer.messages)
	}

	unknown := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users:     &accountTokenUserStoreFake{byEmailErr: store.NewErrNotFound("user", "")},
		passwords: &accountTokenPasswordStoreFake{}, tokens: &accountTokenStoreFake{},
		institution: institution, mailer: &accountTokenMailerFake{enabled: true},
		now: func() time.Time { return now },
	})
	if err := unknown.RequestPasswordReset(
		context.Background(), Invocation{},
		RequestPasswordResetCommand{Email: "unknown@example.edu"},
	); err != nil {
		t.Fatalf("unknown account exposed failure: %v", err)
	}
}

func TestCompletePasswordResetCommitsBeforeEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	user := &model.User{ID: model.NewUserID(), Email: "student@example.edu"}
	session := &model.Session{ID: model.NewSessionID(), UserID: user.ID}
	order := make([]string, 0, 2)
	tokens := &accountTokenStoreFake{
		consumeReset: func(hash, passwordHash string, at int64, event *model.AuditEvent) (*store.PasswordResetResult, error) {
			order = append(order, "commit")
			if hash != model.HashToken(accountTokenTestRawToken) {
				t.Fatalf("token hash = %q", hash)
			}
			if passwordHash != "encoded-new-password" {
				t.Fatalf("password hash = %q", passwordHash)
			}
			if at != now.UnixMilli() {
				t.Fatalf("consume time = %d, want %d", at, now.UnixMilli())
			}
			if event == nil || event.Action != auditPasswordResetComplete {
				t.Fatalf("completion audit = %#v", event)
			}
			return &store.PasswordResetResult{
				User: user, RevokedSessions: []*model.Session{session},
				RevokedAccessHashes: []string{"access-hash"},
			}, nil
		},
	}
	effects := &accountTokenEffectsFake{called: func(userID string, sessionIDs, hashes []string) {
		order = append(order, "effects")
		if userID != user.ID.String() || !reflect.DeepEqual(sessionIDs, []string{session.ID.String()}) ||
			!reflect.DeepEqual(hashes, []string{"access-hash"}) {
			t.Fatalf("effects = user %q sessions %#v hashes %#v", userID, sessionIDs, hashes)
		}
	}}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{}, passwords: &accountTokenPasswordStoreFake{},
		tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()},
		mailer: &accountTokenMailerFake{enabled: true}, effects: effects,
		hasher: accountTokenHasherFake{hash: "encoded-new-password"},
		now:    func() time.Time { return now },
	})

	got, err := app.CompletePasswordReset(
		context.Background(), Invocation{},
		CompletePasswordResetCommand{Token: accountTokenTestRawToken, Password: "new password"},
	)
	if err != nil || got != user {
		t.Fatalf("complete password reset = %#v, %v", got, err)
	}
	if !reflect.DeepEqual(order, []string{"commit", "effects"}) {
		t.Fatalf("operation order = %#v", order)
	}
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
			var appErr *Error
			if !errors.As(err, &appErr) || appErr.Code() != "authentication.account_token.invalid" {
				t.Fatalf("completion error = %v", err)
			}
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
	if !Is(err, "authentication.account_token.invalid") {
		t.Fatalf("completion error = %v", err)
	}
}

const accountTokenTestRawToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type accountTokenTestDependencies struct {
	users       store.UserStore
	passwords   store.PasswordCredentialStore
	tokens      store.UserTokenStore
	institution *model.Institution
	mailer      AccountMailer
	effects     accountTokenEffects
	hasher      accountTokenPasswordHasher
	now         func() time.Time
}

func newAccountTokenTestApp(t *testing.T, deps accountTokenTestDependencies) *App {
	t.Helper()
	if deps.effects == nil {
		deps.effects = &accountTokenEffectsFake{}
	}
	if deps.hasher == nil {
		deps.hasher = accountTokenHasherFake{hash: "encoded-password"}
	}
	if deps.now == nil {
		deps.now = func() time.Time { return time.UnixMilli(1) }
	}
	service, err := newAccountTokenService(
		deps.users,
		deps.passwords,
		deps.tokens,
		&accountTokenInstitutionStoreFake{institution: deps.institution},
		deps.mailer,
		accountTokenRateLimiterFake{},
		deps.hasher,
		accountTokenAuditRecorder{nodeID: "node-1"},
		deps.effects,
		accountTokenDiagnosticsFake{},
		AccountRecoveryPolicy{
			EmailVerificationTTL: 24 * time.Hour,
			PasswordResetTTL:     45 * time.Minute,
		},
		"https://proctor.example.edu",
		func() string { return accountTokenTestRawToken },
		deps.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &App{accountTokens: service}
}

type accountTokenUserStoreFake struct {
	store.UserStore
	byID       *model.User
	byIDErr    error
	byEmail    *model.User
	byEmailErr error
}

func (s *accountTokenUserStoreFake) Get(context.Context, string) (*model.User, error) {
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

func (s *accountTokenPasswordStoreFake) GetByUser(context.Context, string) (*model.PasswordCredential, error) {
	return s.credential, s.err
}

type accountTokenStoreFake struct {
	store.UserTokenStore
	issued                 *model.UserToken
	event                  *model.AuditEvent
	issueErr               error
	consumeVerificationErr error
	consumeReset           func(string, string, int64, *model.AuditEvent) (*store.PasswordResetResult, error)
}

func (s *accountTokenStoreFake) Issue(
	_ context.Context,
	token *model.UserToken,
	event *model.AuditEvent,
) (*model.UserToken, error) {
	s.issued = token
	s.event = event
	return token, s.issueErr
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
	tokenHash string,
	passwordHash string,
	at int64,
	_ string,
	event *model.AuditEvent,
) (*store.PasswordResetResult, error) {
	if s.consumeReset == nil {
		return nil, store.NewErrNotFound("user_token", "")
	}
	return s.consumeReset(tokenHash, passwordHash, at, event)
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

func (m *accountTokenMailerFake) SendCredentialMail(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	textBody string,
	_ string,
	_ time.Time,
) error {
	m.messages = append(m.messages, textBody)
	return m.err
}

type accountTokenRateLimiterFake struct{ err error }

func (f accountTokenRateLimiterFake) Allow(context.Context, string, string, string) error {
	return f.err
}

type accountTokenHasherFake struct {
	hash string
	err  error
}

func (f accountTokenHasherFake) Hash(string) (string, error) { return f.hash, f.err }

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
	mailer       AccountMailer
	rateLimiter  accountTokenRateLimiter
	hasher       accountTokenPasswordHasher
	audit        accountTokenAudit
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
		rateLimiter:  accountTokenRateLimiterFake{},
		hasher:       accountTokenHasherFake{},
		audit:        accountTokenAuditRecorder{nodeID: "node-1"},
		effects:      &accountTokenEffectsFake{},
		diagnostics:  accountTokenDiagnosticsFake{},
		newToken:     func() string { return accountTokenTestRawToken },
	}
}

func (a accountTokenConstructorArgs) build() (*accountTokenService, error) {
	return newAccountTokenService(
		a.users, a.passwords, a.tokens, a.institutions, a.mailer, a.rateLimiter,
		a.hasher, a.audit, a.effects, a.diagnostics, AccountRecoveryPolicy{},
		"https://proctor.example.edu", a.newToken, time.Now,
	)
}
