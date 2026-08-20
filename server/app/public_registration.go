// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const publicRegistrationAuditAction = "authentication.public_registration"

// RegisterLocalUserCommand contains the public, request-only account fields.
// Password and mailbox values never enter audit data or successful responses.
type RegisterLocalUserCommand struct {
	Username string
	Email    string
	Password string
	Source   string
}

type publicRegistrationStore interface {
	RegisterLocal(context.Context, *store.PublicLocalUserRegistration) (*store.PublicLocalUserRegistrationResult, error)
}

type publicRegistrationPolicy interface {
	PublicRegistration(context.Context) (enabled bool, localLogin bool, err error)
}

type publicRegistrationInstitution interface {
	InstitutionID(context.Context) (model.InstitutionID, error)
}

type publicRegistrationPasswordHasher interface {
	Hash(string) (string, error)
}

// publicRegistrationVerificationPreparer exposes only the one frozen
// credential message this use case is allowed to create.
type publicRegistrationVerificationPreparer interface {
	Enabled() bool
	PreparePublicRegistrationVerification(*model.User, model.MailOccurrenceID, string, time.Time, time.Time) (*preparedDirectMail, error)
}

type publicRegistrationDependencies struct {
	registrations publicRegistrationStore
	policies      publicRegistrationPolicy
	institutions  publicRegistrationInstitution
	mail          publicRegistrationVerificationPreparer
	attempts      *authenticationAttemptAccounting
	hasher        publicRegistrationPasswordHasher
	rateLimit     LoginRateLimitPolicy
	tokenTTL      time.Duration
	publicURL     string
	nodeID        string
	newToken      func() string
	now           func() time.Time
}

type publicRegistrationService struct {
	publicRegistrationDependencies
}

func newPublicRegistrationService(deps publicRegistrationDependencies) (*publicRegistrationService, error) {
	if deps.registrations == nil || deps.policies == nil || deps.institutions == nil || deps.mail == nil ||
		deps.attempts == nil || deps.hasher == nil || deps.rateLimit.Window <= 0 ||
		deps.rateLimit.MaximumAttempts <= 0 || deps.rateLimit.MaximumSourceAttempts <= 0 ||
		deps.tokenTTL <= 0 || deps.publicURL == "" || deps.nodeID == "" || deps.newToken == nil || deps.now == nil {
		return nil, errors.New("public registration dependencies are invalid")
	}
	return &publicRegistrationService{publicRegistrationDependencies: deps}, nil
}

func (a *App) RegisterLocalUser(ctx context.Context, invocation Invocation, command RegisterLocalUserCommand) error {
	if a == nil || a.publicRegistration == nil {
		return registrationUnavailable(errors.New("public registration is unavailable"))
	}
	return a.publicRegistration.Register(ctx, invocation, command)
}

func (s *publicRegistrationService) Register(ctx context.Context, invocation Invocation, command RegisterLocalUserCommand) error {
	registrationEnabled, localLoginEnabled, err := s.policies.PublicRegistration(ctx)
	if err != nil {
		return registrationUnavailable(err)
	}
	if !registrationEnabled || !localLoginEnabled {
		return NewError("authentication.registration.invitation_required")
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(command.Email)))
	if err = s.checkRateLimit(ctx, normalizedEmail, command.Source); err != nil {
		return err
	}
	if !s.mail.Enabled() {
		return registrationUnavailable(errors.New("mail delivery is disabled"))
	}

	at := model.TimeUTC(s.now())
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{
		Username: command.Username,
		Email:    command.Email,
	}, at)
	if err != nil {
		return NewError("authentication.registration.invalid").Wrap(err)
	}
	passwordHash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: passwordHash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return registrationUnavailable(err)
	}

	rawToken := s.newToken()
	token := &model.UserToken{
		UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(rawToken), Target: user.Email, ExpiresAt: at.Add(s.tokenTTL),
	}
	token.PrepareCreate(model.NewUserTokenID(), at)
	actionURL, err := accountCredentialLink(s.publicURL, "/account/verify-email", rawToken)
	if err != nil {
		return registrationUnavailable(err)
	}
	prepared, err := s.mail.PreparePublicRegistrationVerification(
		user, model.MailOccurrenceID(token.ID.String()), actionURL, at, token.ExpiresAt,
	)
	if err != nil {
		return registrationUnavailable(err)
	}
	institutionID, err := s.institutions.InstitutionID(ctx)
	if err != nil {
		return registrationUnavailable(err)
	}
	audit := recoveryAuditEvent(
		publicRegistrationAuditAction,
		model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, institutionID.String(),
		invocation.RequestMetadata(), s.nodeID, nil, "anonymous",
	)
	_, err = s.registrations.RegisterLocal(ctx, &store.PublicLocalUserRegistration{
		User: user, Settings: settings, PasswordCredential: credential, DefaultProfilePictureJob: defaultJob,
		VerificationToken: token, TokenLifetime: s.tokenTTL, MailLifetime: s.tokenTTL,
		VerificationOccurrence: prepared.Occurrence,
		VerificationDelivery:   prepared.Delivery, VerificationJob: prepared.Job, AuditEvent: audit,
	})
	if err == nil {
		return nil
	}
	if store.IsUserIdentityConflict(err) {
		// A syntactically valid duplicate is indistinguishable from accepted work.
		return nil
	}
	if errors.Is(err, store.ErrAuthenticationMethodDisabled) || store.IsNotFound(err) {
		return NewError("authentication.registration.invitation_required")
	}
	return registrationUnavailable(err)
}

func (s *publicRegistrationService) checkRateLimit(ctx context.Context, identity, source string) error {
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose:   authenticationAttemptPurposePublicRegistration,
		qualifier: "register",
		window:    s.rateLimit.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: s.rateLimit.MaximumAttempts, identity: identity},
			{dimension: authenticationAttemptDimensionSource, maximum: s.rateLimit.MaximumSourceAttempts, source: source},
		},
	})
	if err != nil {
		return NewError("authentication.rate_limit_unavailable").Wrap(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}

func registrationUnavailable(err error) error {
	return NewError("authentication.registration.unavailable").Wrap(err)
}

type currentPublicRegistrationPolicy struct{ policies store.AccessPolicyStore }

func (p currentPublicRegistrationPolicy) PublicRegistration(ctx context.Context) (bool, bool, error) {
	snapshot, err := p.policies.Get(ctx, 0)
	if err != nil {
		return false, false, err
	}
	if snapshot == nil || snapshot.Policy == nil || snapshot.Policy.Validate() != nil {
		return false, false, errors.New("current access policy is invalid")
	}
	return snapshot.Policy.PublicRegistrationEnabled, snapshot.Policy.LocalLoginEnabled, nil
}

type publicRegistrationInstitutionAdapter struct{ institutions store.InstitutionStore }

func (a publicRegistrationInstitutionAdapter) InstitutionID(ctx context.Context) (model.InstitutionID, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return "", err
	}
	return institution.ID, nil
}

var (
	_ publicRegistrationStore          = (store.UserStore)(nil)
	_ publicRegistrationPasswordHasher = (*passwordHasher)(nil)
)

func (p *directMailPreparer) PreparePublicRegistrationVerification(
	recipient *model.User,
	occurrenceID model.MailOccurrenceID,
	actionURL string,
	at time.Time,
	deadline time.Time,
) (*preparedDirectMail, error) {
	return p.PrepareDirect(DirectMailPreparation{
		Recipient: recipient, OccurrenceID: occurrenceID,
		Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityVerifyEmail,
		ActionURL: actionURL, At: at, Deadline: deadline, JobType: model.JobTypeMailDeliverCredential,
	})
}
