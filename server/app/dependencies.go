// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

// FileContent is the bounded composition contract. Each workflow receives a
// narrower consumer-owned capability; only the application constructor needs
// the complete concrete content adapter surface.
type FileContent interface {
	ProfilePictureUploadFiles
	ProfilePictureReadFiles
	DefaultProfilePictureRenderFiles
	DefaultProfilePictureGenerationFiles
	FileRevisionContentPurger
	starterWorkspaceObjectPurger
	attemptWorkspaceObjectPurger
	examattempt.Content
	examresource.FileContent
	examcorrection.Content
	examworkspace.Content
}

// Dependencies are the explicit capabilities package app needs. The module-root
// composition package builds these from deployment configuration and concrete
// adapters; App never holds platform.Service.
type Dependencies struct {
	Store  store.Catalog
	Cache  authenticationCache
	Mailer AccountMailer
	// MailSecretSealer is the concrete in-process cryptographic module for
	// recoverable mail payloads. It is nil until an independent ring is configured.
	MailSecretSealer *secretseal.Sealer
	Registry         externalProviderSource
	FileContent      FileContent

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
	RealtimeDiagnostics       realtimeDiagnostics
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
