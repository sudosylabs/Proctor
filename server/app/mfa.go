// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/platform/shared/mfa/mfa.go and
// server/channels/app/user.go. Proctor retains the proven TOTP setup,
// activation, validation, and replay-prevention flow while separating the
// encrypted credential aggregate, hashed recovery codes, session assurance,
// and durable security audit.

package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mfaSecretBytes       = 20
	mfaRecoveryCodeBytes = 15
	mfaTOTPWindow        = int64(1)

	actionMFASetup                   model.Action = "mfa.setup"
	actionMFAActivate                model.Action = "mfa.activate"
	actionMFAChallenge               model.Action = "mfa.challenge"
	actionMFARecoveryCodesRegenerate model.Action = "mfa.recovery_codes.regenerate"
	actionMFADisable                 model.Action = "mfa.disable"
)

type mfaMechanics struct {
	settings         MFAPolicy
	keys             map[string][]byte
	primary          string
	newSecret        func() (string, error)
	newRecoveryCodes func(string, int) ([]string, []*model.MFARecoveryCode, error)
}

func newMFAMechanics(settings MFAPolicy) (*mfaMechanics, error) {
	service := &mfaMechanics{
		settings:         settings,
		keys:             make(map[string][]byte),
		newSecret:        randomMFASecret,
		newRecoveryCodes: generateMFARecoveryCodes,
	}
	if !settings.Enabled {
		return service, nil
	}
	encodedKeys := append(
		[]string{settings.EncryptionKey},
		settings.DecryptionKeys...,
	)
	for index, encoded := range encodedKeys {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("configure MFA encryption key %d: invalid AES-256 key", index)
		}
		keyID := mfaEncryptionKeyID(key)
		service.keys[keyID] = key
		if index == 0 {
			service.primary = keyID
		}
	}
	return service, nil
}

// GetMFAStatusQuery returns the caller's MFA enrollment status.
type GetMFAStatusQuery struct{}

// SetupMFACommand starts pending TOTP enrollment.
type SetupMFACommand struct {
	AccountName string
}

// ActivateMFACommand activates pending MFA with a TOTP code.
type ActivateMFACommand struct {
	Code string
}

// ChallengeMFACommand upgrades the current session to multi-factor assurance.
type ChallengeMFACommand struct {
	Code string
}

// RegenerateMFARecoveryCodesCommand replaces recovery codes.
type RegenerateMFARecoveryCodesCommand struct{}

// DisableMFACommand disables MFA for the caller.
type DisableMFACommand struct {
	Code string
}

// MFASetup is one-time setup material. It must never be logged or audited.
type MFASetup struct {
	Secret          string
	ProvisioningURI string
	ExpiresAt       time.Time
}

// MFAActivation is the one-time recovery-code delivery after activation.
type MFAActivation struct {
	RecoveryCodes []string
}

// MFAStatus is the caller's enrollment status.
type MFAStatus struct {
	Enabled                bool
	Pending                bool
	PendingExpiresAt       model.OptionalTime
	RecoveryCodesRemaining int
}

func (a *App) GetMFAStatus(
	ctx context.Context,
	invocation Invocation,
	_ GetMFAStatusQuery,
) (*MFAStatus, error) {
	return a.mfaApplication.GetStatus(ctx, invocation)
}

func (a *App) SetupMFA(
	ctx context.Context,
	invocation Invocation,
	command SetupMFACommand,
) (*MFASetup, error) {
	return a.mfaApplication.Setup(ctx, invocation, command)
}

func (a *App) ActivateMFA(
	ctx context.Context,
	invocation Invocation,
	command ActivateMFACommand,
) (*MFAActivation, error) {
	return a.mfaApplication.Activate(ctx, invocation, command)
}

func (a *App) ChallengeMFA(
	ctx context.Context,
	invocation Invocation,
	command ChallengeMFACommand,
) (*model.Session, error) {
	return a.mfaApplication.Challenge(ctx, invocation, command)
}

func (a *App) RegenerateMFARecoveryCodes(
	ctx context.Context,
	invocation Invocation,
	_ RegenerateMFARecoveryCodesCommand,
) ([]string, error) {
	return a.mfaApplication.RegenerateRecoveryCodes(ctx, invocation)
}

func (a *App) DisableMFA(
	ctx context.Context,
	invocation Invocation,
	_ DisableMFACommand,
) error {
	return a.mfaApplication.Disable(ctx, invocation)
}

func (s *mfaMechanics) consumeSecondFactor(
	ctx context.Context,
	mfaPersistence store.MFAStore,
	userID string,
	code string,
	now time.Time,
) error {
	if mfaPersistence == nil {
		return mfaStoreError(
			"ConsumeMFASecondFactor",
			store.NewErrNotFound("mfa_store", ""),
		)
	}
	credential, err := mfaPersistence.GetByUser(ctx, userID)
	if err != nil || !credential.IsActive() {
		return mfaInvalidCodeError("ConsumeMFASecondFactor")
	}
	normalized := normalizeMFARecoveryCode(code)
	if len(strings.TrimSpace(code)) == 6 {
		secret, decryptErr := s.decrypt(userID, credential)
		if decryptErr != nil {
			return authenticationUnavailable(decryptErr)
		}
		timeStep, valid := verifyTOTP(
			secret,
			strings.TrimSpace(code),
			credential.LastUsedTimeStep,
			now,
		)
		if !valid {
			return mfaInvalidCodeError("ConsumeMFASecondFactor")
		}
		err = mfaPersistence.ConsumeSecondFactor(
			ctx,
			userID,
			timeStep,
			"",
			now.UnixMilli(),
		)
	} else if normalized != "" {
		err = mfaPersistence.ConsumeSecondFactor(
			ctx,
			userID,
			0,
			model.HashToken(normalized),
			now.UnixMilli(),
		)
	} else {
		return mfaInvalidCodeError("ConsumeMFASecondFactor")
	}
	if err != nil {
		if store.IsNotFound(err) {
			return mfaInvalidCodeError("ConsumeMFASecondFactor")
		}
		return mfaStoreError("ConsumeMFASecondFactor.consume", err)
	}
	return nil
}

func (s *mfaMechanics) encrypt(userID string, secret string) (string, error) {
	key, ok := s.keys[s.primary]
	if !ok {
		return "", errors.New("MFA primary encryption key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(
		append([]byte(nil), nonce...),
		nonce,
		[]byte(secret),
		[]byte("proctor:mfa:"+userID),
	)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *mfaMechanics) decrypt(
	userID string,
	credential *model.MFACredential,
) (string, error) {
	key, ok := s.keys[credential.EncryptionKeyID]
	if !ok {
		return "", errors.New("MFA decryption key is unavailable")
	}
	encoded, err := base64.RawURLEncoding.Strict().
		DecodeString(credential.EncryptedSecret)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encoded) <= aead.NonceSize() {
		return "", errors.New("MFA encrypted secret is truncated")
	}
	plaintext, err := aead.Open(
		nil,
		encoded[:aead.NonceSize()],
		encoded[aead.NonceSize():],
		[]byte("proctor:mfa:"+userID),
	)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func mfaEncryptionKeyID(key []byte) string {
	digest := sha256.Sum256(key)
	return hex.EncodeToString(digest[:8])
}

func randomMFASecret() (string, error) {
	raw := make([]byte, mfaSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(raw), nil
}

func generateMFARecoveryCodes(
	userID string,
	count int,
) ([]string, []*model.MFARecoveryCode, error) {
	parsedUserID, err := model.ParseUserID(userID)
	if err != nil {
		return nil, nil, err
	}
	rawCodes := make([]string, 0, count)
	models := make([]*model.MFARecoveryCode, 0, count)
	seen := make(map[string]struct{}, count)
	for len(rawCodes) < count {
		random := make([]byte, mfaRecoveryCodeBytes)
		if _, err := rand.Read(random); err != nil {
			return nil, nil, err
		}
		normalized := strings.ToLower(
			base32.StdEncoding.WithPadding(base32.NoPadding).
				EncodeToString(random),
		)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		rawCodes = append(rawCodes, formatMFARecoveryCode(normalized))
		models = append(models, &model.MFARecoveryCode{
			UserID:   parsedUserID,
			CodeHash: model.HashToken(normalized),
		})
	}
	return rawCodes, models, nil
}

func formatMFARecoveryCode(normalized string) string {
	parts := make([]string, 0, (len(normalized)+3)/4)
	for len(normalized) > 4 {
		parts = append(parts, normalized[:4])
		normalized = normalized[4:]
	}
	if normalized != "" {
		parts = append(parts, normalized)
	}
	return strings.Join(parts, "-")
}

func normalizeMFARecoveryCode(code string) string {
	var builder strings.Builder
	for _, value := range strings.ToLower(strings.TrimSpace(code)) {
		switch {
		case value >= 'a' && value <= 'z':
			builder.WriteRune(value)
		case value >= '2' && value <= '7':
			builder.WriteRune(value)
		case value == '-' || value == ' ':
		default:
			return ""
		}
	}
	normalized := builder.String()
	if len(normalized) != base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodedLen(mfaRecoveryCodeBytes) {
		return ""
	}
	return normalized
}

func verifyTOTP(
	secret string,
	code string,
	lastUsedTimeStep int64,
	now time.Time,
) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, value := range code {
		if value < '0' || value > '9' {
			return 0, false
		}
	}
	current := now.UTC().Unix() / 30
	for offset := -mfaTOTPWindow; offset <= mfaTOTPWindow; offset++ {
		timeStep := current + offset
		if timeStep <= lastUsedTimeStep {
			continue
		}
		expected, err := computeTOTP(secret, timeStep)
		if err != nil {
			return 0, false
		}
		if expected == code {
			return timeStep, true
		}
	}
	return 0, false
}

func computeTOTP(secret string, timeStep int64) (string, error) {
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(timeStep))
	mac := hmac.New(sha1.New, key)
	if _, err := mac.Write(counter[:]); err != nil {
		return "", err
	}
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(digest[offset : offset+4])
	value := (truncated & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value), nil
}

func mfaProvisioningURI(issuer string, accountName string, secret string) string {
	query := make(url.Values)
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	value := url.URL{
		Scheme:   "otpauth",
		Host:     "totp",
		Path:     "/" + issuer + ":" + accountName,
		RawQuery: query.Encode(),
	}
	return value.String()
}

func mfaInvalidCodeError(where string) error {
	_ = where
	return NewError("authentication.mfa.invalid_code")
}

func mfaStoreFailure(err error) error {
	return mfaStoreError("MFA", err)
}

func mfaStoreError(where string, err error) error {
	_ = where
	code := "authentication.mfa.unavailable"
	if store.IsNotFound(err) {
		code = "authentication.mfa.not_found"
	} else if store.IsConflict(err) {
		code = "authentication.mfa.conflict"
	}
	return NewError(code).Wrap(err)
}
