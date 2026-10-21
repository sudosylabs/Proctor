// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"strings"
	"time"
	"unicode"
)

type Password struct {
	MaximumConcurrentOperations int `json:"MaximumConcurrentOperations"`
	MinimumLength               int `json:"MinimumLength"`
	MaximumLength               int `json:"MaximumLength"`
	ArgonMemoryKiB              int `json:"ArgonMemoryKiB"`
	ArgonIterations             int `json:"ArgonIterations"`
	ArgonParallelism            int `json:"ArgonParallelism"`
	ArgonSaltBytes              int `json:"ArgonSaltBytes"`
	ArgonKeyBytes               int `json:"ArgonKeyBytes"`
}

type Sessions struct {
	AccessTTL              Duration `json:"AccessTTL"`
	RefreshTTL             Duration `json:"RefreshTTL"`
	IdleTTL                Duration `json:"IdleTTL"`
	AbsoluteTTL            Duration `json:"AbsoluteTTL"`
	ActivityUpdateInterval Duration `json:"ActivityUpdateInterval"`
	MaximumPerUser         int      `json:"MaximumPerUser"`
}

// LoginRateLimit bounds local login attempts in fixed windows by account and
// source together and by source alone. Failed password verifications against
// one account from every source slow further attempts against it by a delay
// that starts after AccountFailuresBeforeDelay failures and doubles up to
// MaximumAccountFailureDelay; the delay never refuses a correct password.
type LoginRateLimit struct {
	Window                     Duration `json:"Window"`
	MaximumAttempts            int      `json:"MaximumAttempts"`
	MaximumSourceAttempts      int      `json:"MaximumSourceAttempts"`
	AccountFailuresBeforeDelay int      `json:"AccountFailuresBeforeDelay"`
	MaximumAccountFailureDelay Duration `json:"MaximumAccountFailureDelay"`
}

// AttemptRateLimit bounds attempts in fixed windows by one identity and by
// one source.
type AttemptRateLimit struct {
	Window                Duration `json:"Window"`
	MaximumAttempts       int      `json:"MaximumAttempts"`
	MaximumSourceAttempts int      `json:"MaximumSourceAttempts"`
}

type AccountRecovery struct {
	EmailVerificationTTL Duration         `json:"EmailVerificationTTL"`
	PasswordResetTTL     Duration         `json:"PasswordResetTTL"`
	RateLimit            AttemptRateLimit `json:"RateLimit"`
}

type PersonalAccessTokens struct {
	MinimumLifetime        Duration `json:"MinimumLifetime"`
	MaximumLifetime        Duration `json:"MaximumLifetime"`
	LastUsedUpdateInterval Duration `json:"LastUsedUpdateInterval"`
	MaximumPerUser         int      `json:"MaximumPerUser"`
}

// Bootstrap protects the one-time public installation initialization route.
// DevelopmentMode permits process-generated secret material only while the
// installation is pristine and both listener and public origin are loopback-only.
type Bootstrap struct {
	Secret          string `json:"Secret"`
	DevelopmentMode bool   `json:"DevelopmentMode"`
}

// MFA contains operator-owned enrollment policy and an independent key ring
// projected into the server's versioned secret-sealing module.
type MFA struct {
	Enabled           bool     `json:"Enabled"`
	Issuer            string   `json:"Issuer"`
	EncryptionKey     string   `json:"EncryptionKey"`
	DecryptionKeys    []string `json:"DecryptionKeys"`
	SetupTTL          Duration `json:"SetupTTL"`
	RecoveryCodeCount int      `json:"RecoveryCodeCount"`
}

type Authentication struct {
	Bootstrap               Bootstrap              `json:"Bootstrap"`
	Password                Password               `json:"Password"`
	Sessions                Sessions               `json:"Sessions"`
	RecentAuthenticationTTL Duration               `json:"RecentAuthenticationTTL"`
	LoginRateLimit          LoginRateLimit         `json:"LoginRateLimit"`
	AccountRecovery         AccountRecovery        `json:"AccountRecovery"`
	PersonalAccessTokens    PersonalAccessTokens   `json:"PersonalAccessTokens"`
	MFA                     MFA                    `json:"MFA"`
	External                ExternalAuthentication `json:"External"`
}

func validateAuthentication(authentication Authentication, add func(string, string)) {
	password := authentication.Password
	if password.MaximumConcurrentOperations < 1 {
		add("authentication.password.maximum_concurrent_operations", "must be greater than zero")
	}
	if password.MinimumLength < 8 || password.MinimumLength > 128 {
		add("authentication.password.minimum_length", "must be between 8 and 128")
	}
	if password.MaximumLength < password.MinimumLength || password.MaximumLength > 1024 {
		add("authentication.password.maximum_length", "must be between minimum_length and 1024")
	}
	if password.ArgonMemoryKiB < 19*1024 || password.ArgonMemoryKiB > 1024*1024 {
		add("authentication.password.argon_memory_kib", "must be between 19456 and 1048576")
	}
	if password.ArgonIterations < 1 || password.ArgonIterations > 20 {
		add("authentication.password.argon_iterations", "must be between 1 and 20")
	}
	if password.ArgonParallelism < 1 || password.ArgonParallelism > 64 {
		add("authentication.password.argon_parallelism", "must be between 1 and 64")
	}
	if password.ArgonSaltBytes < 16 || password.ArgonSaltBytes > 64 {
		add("authentication.password.argon_salt_bytes", "must be between 16 and 64")
	}
	if password.ArgonKeyBytes < 16 || password.ArgonKeyBytes > 64 {
		add("authentication.password.argon_key_bytes", "must be between 16 and 64")
	}

	sessions := authentication.Sessions
	for _, item := range []struct {
		field string
		value time.Duration
	}{
		{"authentication.sessions.access_ttl", sessions.AccessTTL.Duration},
		{"authentication.sessions.refresh_ttl", sessions.RefreshTTL.Duration},
		{"authentication.sessions.idle_ttl", sessions.IdleTTL.Duration},
		{"authentication.sessions.absolute_ttl", sessions.AbsoluteTTL.Duration},
		{"authentication.sessions.activity_update_interval", sessions.ActivityUpdateInterval.Duration},
	} {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
	if sessions.AccessTTL.Duration > sessions.IdleTTL.Duration {
		add("authentication.sessions.access_ttl", "must not exceed idle_ttl")
	}
	if sessions.IdleTTL.Duration > sessions.AbsoluteTTL.Duration {
		add("authentication.sessions.idle_ttl", "must not exceed absolute_ttl")
	}
	if sessions.RefreshTTL.Duration > sessions.AbsoluteTTL.Duration {
		add("authentication.sessions.refresh_ttl", "must not exceed absolute_ttl")
	}
	if sessions.ActivityUpdateInterval.Duration >= sessions.IdleTTL.Duration {
		add("authentication.sessions.activity_update_interval", "must be less than idle_ttl")
	}
	if sessions.MaximumPerUser < 1 || sessions.MaximumPerUser > 1000 {
		add("authentication.sessions.maximum_per_user", "must be between 1 and 1000")
	}
	if authentication.RecentAuthenticationTTL.Duration < time.Minute ||
		authentication.RecentAuthenticationTTL.Duration > 24*time.Hour {
		add(
			"authentication.recent_authentication_ttl",
			"must be between 1m and 24h",
		)
	}
	if authentication.RecentAuthenticationTTL.Duration >
		sessions.AbsoluteTTL.Duration {
		add(
			"authentication.recent_authentication_ttl",
			"must not exceed sessions.absolute_ttl",
		)
	}

	rateLimit := authentication.LoginRateLimit
	validateAuthenticationRateLimit(
		"authentication.login_rate_limit", AttemptRateLimit{
			Window: rateLimit.Window, MaximumAttempts: rateLimit.MaximumAttempts,
			MaximumSourceAttempts: rateLimit.MaximumSourceAttempts,
		}, add,
	)
	if rateLimit.AccountFailuresBeforeDelay < 1 || rateLimit.AccountFailuresBeforeDelay > 10_000 {
		add("authentication.login_rate_limit.account_failures_before_delay", "must be between 1 and 10000")
	}
	if rateLimit.MaximumAccountFailureDelay.Duration < 250*time.Millisecond ||
		rateLimit.MaximumAccountFailureDelay.Duration > 30*time.Second {
		add("authentication.login_rate_limit.maximum_account_failure_delay", "must be between 250ms and 30s")
	}

	recovery := authentication.AccountRecovery
	if recovery.EmailVerificationTTL.Duration < 5*time.Minute ||
		recovery.EmailVerificationTTL.Duration > 30*24*time.Hour {
		add(
			"authentication.account_recovery.email_verification_ttl",
			"must be between 5m and 720h",
		)
	}
	if recovery.PasswordResetTTL.Duration < 5*time.Minute ||
		recovery.PasswordResetTTL.Duration > 24*time.Hour {
		add(
			"authentication.account_recovery.password_reset_ttl",
			"must be between 5m and 24h",
		)
	}
	validateAuthenticationRateLimit(
		"authentication.account_recovery.rate_limit",
		recovery.RateLimit,
		add,
	)

	tokens := authentication.PersonalAccessTokens
	if tokens.MinimumLifetime.Duration < 5*time.Minute ||
		tokens.MinimumLifetime.Duration > 24*time.Hour {
		add(
			"authentication.personal_access_tokens.minimum_lifetime",
			"must be between 5m and 24h",
		)
	}
	if tokens.MaximumLifetime.Duration < tokens.MinimumLifetime.Duration ||
		tokens.MaximumLifetime.Duration > 365*24*time.Hour {
		add(
			"authentication.personal_access_tokens.maximum_lifetime",
			"must be between minimum_lifetime and 8760h",
		)
	}
	if tokens.LastUsedUpdateInterval.Duration <= 0 ||
		tokens.LastUsedUpdateInterval.Duration >= tokens.MinimumLifetime.Duration {
		add(
			"authentication.personal_access_tokens.last_used_update_interval",
			"must be greater than zero and less than minimum_lifetime",
		)
	}
	if tokens.MaximumPerUser < 1 || tokens.MaximumPerUser > 1000 {
		add(
			"authentication.personal_access_tokens.maximum_per_user",
			"must be between 1 and 1000",
		)
	}

	mfa := authentication.MFA
	if len(mfa.Issuer) == 0 || len(mfa.Issuer) > 128 ||
		strings.ContainsAny(mfa.Issuer, "\x00\r\n") {
		add("authentication.mfa.issuer", "must contain between 1 and 128 safe characters")
	}
	if mfa.SetupTTL.Duration < time.Minute || mfa.SetupTTL.Duration > time.Hour {
		add("authentication.mfa.setup_ttl", "must be between 1m and 1h")
	}
	if mfa.RecoveryCodeCount < 5 || mfa.RecoveryCodeCount > 20 {
		add("authentication.mfa.recovery_code_count", "must be between 5 and 20")
	}
	if mfa.Enabled && mfa.EncryptionKey == "" {
		add(
			"authentication.mfa.encryption_key",
			"is required when MFA is enabled",
		)
	}
	validateSecretSealing("authentication.mfa", SecretSealing{
		EncryptionKey: mfa.EncryptionKey, DecryptionKeys: mfa.DecryptionKeys,
	}, add)

	validateExternalAuthentication(authentication.External, add)
}

func validateAuthenticationRateLimit(
	prefix string,
	rateLimit AttemptRateLimit,
	add func(string, string),
) {
	if rateLimit.Window.Duration < time.Second || rateLimit.Window.Duration > 24*time.Hour {
		add(prefix+".window", "must be between 1s and 24h")
	}
	if rateLimit.MaximumAttempts < 1 || rateLimit.MaximumAttempts > 10000 {
		add(prefix+".maximum_attempts", "must be between 1 and 10000")
	}
	if rateLimit.MaximumSourceAttempts < rateLimit.MaximumAttempts ||
		rateLimit.MaximumSourceAttempts > 1_000_000 {
		add(prefix+".maximum_source_attempts", "must be between maximum_attempts and 1000000")
	}
}

func validateBootstrap(server Server, bootstrap Bootstrap, add func(string, string)) {
	secret := bootstrap.Secret
	if secret != "" {
		if strings.TrimSpace(secret) != secret {
			add("authentication.bootstrap.secret", "must not contain surrounding whitespace")
		}
		if len(secret) < 32 || len(secret) > 512 {
			add("authentication.bootstrap.secret", "must be between 32 and 512 bytes")
		}
		if strings.ContainsFunc(secret, unicode.IsControl) {
			add("authentication.bootstrap.secret", "must not contain control characters")
		}
	}

	loopbackDevelopment := bootstrap.DevelopmentMode &&
		isLiteralLoopbackListenAddress(server.ListenAddress) &&
		isLoopbackPublicURL(server.PublicURL)
	if bootstrap.DevelopmentMode && !loopbackDevelopment {
		add("authentication.bootstrap.development_mode", "requires a loopback listener and loopback public URL")
	}
}
