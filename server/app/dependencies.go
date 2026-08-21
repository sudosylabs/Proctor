// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"io"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
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
	OnboardingImportFiles
}

type OnboardingImportFiles interface {
	StageOnboardingImport(context.Context, model.OnboardingImportID, io.Reader, int64) (string, int64, error)
	IsOnboardingImportTooLarge(error) bool
	OpenOnboardingImport(context.Context, model.OnboardingImportID) (io.ReadCloser, error)
	RemoveOnboardingImport(context.Context, model.OnboardingImportID) error
	ListOnboardingImportFiles(context.Context, string, int, time.Time) ([]model.OnboardingImportID, string, error)
}

// Dependencies are the explicit capabilities package app needs. The module-root
// composition package builds these from deployment configuration and concrete
// adapters; App never holds platform.Service.
type Dependencies struct {
	Store                store.Catalog
	Cache                authenticationCache
	MailDeliverySender   appmail.Sender
	MailTemplateRenderer appmail.Renderer
	MailDeliveryRecorder MailDeliveryRecorder
	// MailSecretSealer is the concrete in-process cryptographic module for
	// recoverable mail payloads. It is nil until an independent ring is configured.
	MailSecretSealer *secretseal.Sealer
	Registry         externalProviderSource
	FileContent      FileContent

	NodeID    string
	PublicURL string
	// LoopbackHTTPDevelopment is the immutable composition-owned projection of
	// explicit local development. It never relaxes non-loopback issuer rules.
	LoopbackHTTPDevelopment bool

	Password                PasswordPolicy
	Sessions                SessionPolicy
	LoginRateLimit          LoginRateLimitPolicy
	BootstrapProtection     BootstrapProtectionPolicy
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

// recoveryDiagnostics reports non-fatal recovery failures without depending on
// a concrete logger package.
type recoveryDiagnostics interface {
	ErrorContext(ctx context.Context, message string, err error)
}
