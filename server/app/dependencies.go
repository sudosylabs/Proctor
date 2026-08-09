// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

// Dependencies are the explicit capabilities package app needs. The module-root
// composition package builds these from deployment configuration and concrete
// adapters; App never holds platform.Service.
type Dependencies struct {
	Store       store.Store
	Cache       authenticationCache
	Mailer      AccountMailer
	Registry    externalProviderSource
	FileContent FileContent

	NodeID    string
	PublicURL string

	Password                PasswordPolicy
	Sessions                SessionPolicy
	LoginRateLimit          LoginRateLimitPolicy
	PersonalAccessToken     PersonalAccessTokenPolicy
	AccountRecovery         AccountRecoveryPolicy
	MFA                     MFAPolicy
	ExternalAuth            ExternalAuthenticationPolicy
	RecentAuthenticationTTL time.Duration

	AuthenticationDiagnostics authenticationDiagnostics
	RealtimeDiagnostics       RealtimeDiagnostics
	RecoveryDiagnostics       recoveryDiagnostics
}

// PasswordPolicy is the immutable password-hashing projection composition
// supplies so password code does not import deployment config.
type PasswordPolicy struct {
	MinimumLength    int
	MaximumLength    int
	ArgonMemoryKiB   int
	ArgonIterations  int
	ArgonParallelism int
	ArgonSaltBytes   int
	ArgonKeyBytes    int
}

// AccountRecoveryPolicy is the operator projection for email verification and
// password reset TTLs and rate limits.
type AccountRecoveryPolicy struct {
	EmailVerificationTTL time.Duration
	PasswordResetTTL     time.Duration
	RateLimit            LoginRateLimitPolicy
}

// MFAPolicy is the cryptographic and enrollment projection for TOTP MFA.
type MFAPolicy struct {
	Enabled           bool
	Issuer            string
	EncryptionKey     string
	DecryptionKeys    []string
	SetupTTL          time.Duration
	RecoveryCodeCount int
}

// AccountMailer is the narrow outbound mail port for account recovery.
type AccountMailer interface {
	Enabled() bool
	SendCredentialMail(
		ctx context.Context,
		displayName string,
		email string,
		subject string,
		textBody string,
		htmlBody string,
		at time.Time,
	) error
}

// recoveryDiagnostics reports non-fatal recovery failures without depending on
// a concrete logger package.
type recoveryDiagnostics interface {
	ErrorContext(ctx context.Context, message string, err error)
}
