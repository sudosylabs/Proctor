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

type MFAService struct {
	settings MFAPolicy
	keys     map[string][]byte
	primary  string
	now      func() time.Time
}

func newMFAService(settings MFAPolicy) (*MFAService, error) {
	service := &MFAService{
		settings: settings,
		keys:     make(map[string][]byte),
		now:      time.Now,
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
	principal := invocation.Principal()
	if err := a.requireInteractiveSession(principal, false); err != nil {
		return nil, err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return nil, err
	}
	now := a.mfa.now()
	credential, err := a.Store().MFA().GetByUser(ctx, principal.UserID.String())
	if store.IsNotFound(err) {
		return &MFAStatus{}, nil
	}
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	status := &MFAStatus{
		Enabled:          credential.IsActive(),
		Pending:          credential.IsPendingAt(now),
		PendingExpiresAt: credential.PendingExpiresAt,
	}
	if status.Enabled {
		status.RecoveryCodesRemaining, err = a.Store().MFA().
			CountRecoveryCodes(ctx, principal.UserID.String())
		if err != nil {
			return nil, mfaStoreFailure(err)
		}
	}
	return status, nil
}

func (a *App) SetupMFA(
	ctx context.Context,
	invocation Invocation,
	command SetupMFACommand,
) (*MFASetup, error) {
	principal := invocation.Principal()
	if err := a.requireInteractiveSession(principal, true); err != nil {
		return nil, err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return nil, err
	}
	user, err := a.Store().User().Get(ctx, principal.UserID.String())
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	accountName := command.AccountName
	if strings.TrimSpace(accountName) == "" {
		accountName = user.Email
	}
	secret, err := randomMFASecret()
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	encrypted, err := a.mfa.encrypt(principal.UserID.String(), secret)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	now := a.mfa.now()
	candidate := &model.MFACredential{
		UserID:           principal.UserID,
		State:            model.MFAStatePending,
		EncryptedSecret:  encrypted,
		EncryptionKeyID:  a.mfa.primary,
		PendingExpiresAt: model.OptionalTimeFrom(now.Add(a.mfa.settings.SetupTTL)),
		CreatedAt:        now,
	}
	resource, err := a.mfaAuditResourceNeutral(ctx)
	if err != nil {
		return nil, err
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionMFASetup, resource, invocation.RequestMetadata(), nil, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := a.Store().MFA().SavePending(ctx, candidate)
	if err != nil {
		return nil, a.failMFAMutationNeutral(ctx, attempt.ID.String(), err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.ID.String(), model.AuditStatusSuccess, "", saved.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return &MFASetup{
		Secret: secret,
		ProvisioningURI: mfaProvisioningURI(
			a.mfa.settings.Issuer,
			accountName,
			secret,
		),
		ExpiresAt: saved.PendingExpiresAt.Time,
	}, nil
}

func (a *App) ActivateMFA(
	ctx context.Context,
	invocation Invocation,
	command ActivateMFACommand,
) (*MFAActivation, error) {
	principal := invocation.Principal()
	code := command.Code
	metadata := invocation.RequestMetadata()
	if err := a.requireInteractiveSession(principal, true); err != nil {
		return nil, err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return nil, err
	}
	credential, err := a.Store().MFA().GetByUser(ctx, principal.UserID.String())
	if err != nil {
		return nil, mfaStoreFailure(err)
	}
	now := a.mfa.now()
	if !credential.IsPendingAt(now) {
		return nil, mfaInvalidCodeError("ActivateMFA")
	}
	secret, err := a.mfa.decrypt(principal.UserID.String(), credential)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	timeStep, valid := verifyTOTP(secret, code, 0, now)
	if !valid {
		return nil, mfaInvalidCodeError("ActivateMFA")
	}
	rawCodes, recoveryCodes, err := generateMFARecoveryCodes(
		principal.UserID.String(),
		a.mfa.settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	resource, err := a.mfaAuditResourceNeutral(ctx)
	if err != nil {
		return nil, err
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionMFAActivate, resource, metadata, nil,
		credential.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	activated, err := a.Store().MFA().Activate(
		ctx,
		credential.ID.String(),
		principal.UserID.String(),
		timeStep,
		recoveryCodes,
		principal.SessionID.String(),
		now.UnixMilli(),
	)
	if err != nil {
		return nil, a.failMFAMutationNeutral(ctx, attempt.ID.String(), err)
	}
	a.authenticationEffects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
		[]string{principal.SessionID.String()},
		activated.AccessTokenHashes,
	)
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.ID.String(), model.AuditStatusSuccess, "",
		map[string]any{
			"credential":          activated.Credential.Auditable(),
			"recovery_code_count": len(rawCodes),
		},
	); appErr != nil {
		return nil, appErr
	}
	return &MFAActivation{RecoveryCodes: rawCodes}, nil
}

func (a *App) ChallengeMFA(
	ctx context.Context,
	invocation Invocation,
	command ChallengeMFACommand,
) (*model.Session, error) {
	principal := invocation.Principal()
	code := command.Code
	metadata := invocation.RequestMetadata()
	if err := a.requireInteractiveSession(principal, false); err != nil {
		return nil, err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return nil, err
	}
	resource, appErr := a.mfaAuditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionMFAChallenge, resource, metadata, nil, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	now := a.mfa.now()
	if appErr := a.consumeMFASecondFactor(
		ctx,
		principal.UserID.String(),
		code,
		now,
	); appErr != nil {
		code := "authentication.mfa.invalid_code"
		if failure, ok := As(appErr); ok {
			code = failure.Code()
		}
		if _, auditErr := a.audit.CompleteCriticalAction(
			ctx,
			attempt.ID.String(),
			model.AuditStatusFail,
			code,
			nil,
		); auditErr != nil {
			return nil, auditErr
		}
		return nil, appErr
	}
	hashes, err := a.Store().MFA().UpgradeSession(
		ctx, principal.SessionID.String(), principal.UserID.String(), now.UnixMilli(),
	)
	if err != nil {
		return nil, a.failMFAMutation(ctx, attempt.ID.String(), "ChallengeMFA.upgrade", err)
	}
	a.authenticationEffects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
		[]string{principal.SessionID.String()},
		hashes,
	)
	session, err := a.Store().Session().Get(ctx, principal.SessionID.String())
	if err != nil {
		return nil, a.failMFAMutation(ctx, attempt.ID.String(), "ChallengeMFA.session", err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.ID.String(), model.AuditStatusSuccess, "", session.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return session, nil
}

func (a *App) RegenerateMFARecoveryCodes(
	ctx context.Context,
	invocation Invocation,
	_ RegenerateMFARecoveryCodesCommand,
) ([]string, error) {
	principal := invocation.Principal()
	metadata := invocation.RequestMetadata()
	if err := a.requireStrongRecentSessionNeutral(principal); err != nil {
		return nil, err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return nil, err
	}
	rawCodes, codes, err := generateMFARecoveryCodes(
		principal.UserID.String(),
		a.mfa.settings.RecoveryCodeCount,
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	resource, appErr := a.mfaAuditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionMFARecoveryCodesRegenerate, resource, metadata,
		nil, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	if err := a.Store().MFA().ReplaceRecoveryCodes(
		ctx,
		principal.UserID.String(),
		codes,
		a.mfa.now().UnixMilli(),
	); err != nil {
		return nil, a.failMFAMutation(
			ctx,
			attempt.ID.String(),
			"RegenerateMFARecoveryCodes.replace",
			err,
		)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.ID.String(), model.AuditStatusSuccess, "",
		map[string]any{"recovery_code_count": len(rawCodes)},
	); appErr != nil {
		return nil, appErr
	}
	return rawCodes, nil
}

func (a *App) DisableMFA(
	ctx context.Context,
	invocation Invocation,
	_ DisableMFACommand,
) error {
	principal := invocation.Principal()
	metadata := invocation.RequestMetadata()
	if err := a.requireStrongRecentSessionNeutral(principal); err != nil {
		return err
	}
	if err := a.requireMFAEnabled(); err != nil {
		return err
	}
	resource, appErr := a.mfaAuditResource(ctx)
	if appErr != nil {
		return appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionMFADisable, resource, metadata, nil, nil,
	)
	if appErr != nil {
		return appErr
	}
	result, err := a.Store().MFA().Disable(
		ctx,
		principal.UserID.String(),
		a.mfa.now().UnixMilli(),
	)
	if err != nil {
		return a.failMFAMutation(ctx, attempt.ID.String(), "DisableMFA.disable", err)
	}
	a.authenticationEffects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
		nil,
		result.AccessTokenHashes,
	)
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.ID.String(), model.AuditStatusSuccess, "",
		map[string]any{"disabled": true},
	); appErr != nil {
		return appErr
	}
	return nil
}

func (a *App) consumeMFASecondFactor(
	ctx context.Context,
	userID string,
	code string,
	now time.Time,
) error {
	return a.mfa.consumeSecondFactor(ctx, a.Store(), userID, code, now)
}

func (s *MFAService) consumeSecondFactor(
	ctx context.Context,
	persistence store.Store,
	userID string,
	code string,
	now time.Time,
) error {
	mfaPersistence := persistence.MFA()
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

func (a *App) requireMFAEnabled() error {
	if a.mfa != nil && a.mfa.settings.Enabled {
		return nil
	}
	return NewError("authentication.mfa.disabled")
}

func (a *App) requireStrongRecentSession(
	principal model.Principal,
	where string,
) error {
	_ = where
	if err := a.requireInteractiveSession(principal, true); err != nil {
		return err
	}
	if !principal.HasStrongAuthentication() {
		return NewError("authentication.strong_authentication_required")
	}
	return nil
}

func (a *App) requireStrongRecentSessionNeutral(principal model.Principal) error {
	return a.requireStrongRecentSession(principal, "MFA")
}

func (a *App) mfaAuditResourceNeutral(ctx context.Context) (model.Resource, error) {
	resource, appErr := a.mfaAuditResource(ctx)
	if appErr != nil {
		return model.Resource{}, appErr
	}
	return resource, nil
}

func (a *App) failMFAMutationNeutral(ctx context.Context, auditID string, err error) error {
	return a.failMFAMutation(ctx, auditID, "MFA", err)
}

func (a *App) mfaAuditResource(
	ctx context.Context,
) (model.Resource, error) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, mfaStoreError("MFA.audit_resource", err)
	}
	return model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, nil
}

func (a *App) failMFAMutation(
	ctx context.Context,
	auditID string,
	where string,
	err error,
) error {
	mapped := mfaStoreError(where, err)
	code := "authentication.mfa.unavailable"
	if failure, ok := As(mapped); ok {
		code = failure.Code()
	}
	if _, auditErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, code, nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

func (s *MFAService) encrypt(userID string, secret string) (string, error) {
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

func (s *MFAService) decrypt(
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
