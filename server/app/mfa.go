// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/platform/shared/mfa/mfa.go and
// server/channels/app/user.go. Proctor retains the proven TOTP setup,
// activation, validation, and replay-prevention flow while separating the
// encrypted credential aggregate, hashed recovery codes, session assurance,
// and durable security audit.

package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mfaSecretBytes       = 20
	mfaRecoveryCodeBytes = 15
	mfaTOTPWindow        = int64(1)
)

// mfaMechanicsDependencies are the collaborators of mfaMechanics;
// newMFAMechanics validates them once and mfaMechanics keeps them in deps.
type mfaMechanicsDependencies struct {
	Settings MFAPolicy
	Sealer   *secretseal.Sealer
}

type mfaMechanics struct {
	deps mfaMechanicsDependencies

	newSecret        func() (string, error)
	newRecoveryCodes func(string, int) ([]string, []*model.MFARecoveryCode, error)
}

func newMFAMechanics(deps mfaMechanicsDependencies) (*mfaMechanics, error) {
	mechanics := &mfaMechanics{deps: deps, newSecret: randomMFASecret, newRecoveryCodes: generateMFARecoveryCodes}
	if deps.Settings.Enabled && deps.Sealer == nil {
		return nil, errors.New("enabled MFA requires secret sealing")
	}
	return mechanics, nil
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
	ServiceEnabled           bool
	MFARecoveryRequired      bool
	AuthenticationMethod     string
	AuthenticationProviderID string
	AuthenticationStrength   model.AuthenticationStrength
	RecentlyAuthenticated    bool
	Enabled                  bool
	Pending                  bool
	PendingExpiresAt         model.OptionalTime
	RecoveryCodesRemaining   int
}

func (s *mfaMechanics) consumeSecondFactor(
	ctx context.Context,
	mfaPersistence store.MFAStore,
	userID string,
	code string,
	now time.Time,
) error {
	proof, err := s.verifySecondFactor(ctx, mfaPersistence, userID, code, now)
	if err != nil {
		return err
	}
	if err := mfaPersistence.ConsumeSecondFactor(ctx, &store.MFASecondFactorConsumption{UserID: model.UserID(userID), TimeStep: proof.step, RecoveryCodeHash: proof.recoveryHash, Now: now}); err != nil {
		if store.IsNotFound(err) {
			return mfaInvalidCodeError()
		}
		return mfaStoreErrors.Translate(err)
	}
	return nil
}

type mfaFactorProof struct {
	credentialID model.MFACredentialID
	step         int64
	recoveryHash string
}

// Verification does not consume a factor. The owning aggregate commits replay
// protection together with the Session transition and its required audit.
func (s *mfaMechanics) verifySecondFactor(ctx context.Context, persistence store.MFAStore, userID, code string, now time.Time) (mfaFactorProof, error) {
	credential, err := persistence.GetByUser(ctx, model.UserID(userID))
	if err != nil {
		if store.IsNotFound(err) {
			return mfaFactorProof{}, mfaInvalidCodeError()
		}
		return mfaFactorProof{}, mfaStoreErrors.Translate(err)
	}
	if credential == nil || !credential.IsActive() {
		return mfaFactorProof{}, mfaInvalidCodeError()
	}
	proof := mfaFactorProof{credentialID: credential.ID}
	if len(strings.TrimSpace(code)) == 6 {
		secret, err := s.decrypt(userID, credential)
		if err != nil {
			return mfaFactorProof{}, authenticationUnavailable(err)
		}
		step, valid := verifyTOTP(secret, strings.TrimSpace(code), credential.LastUsedTimeStep, now)
		if !valid {
			return mfaFactorProof{}, mfaInvalidCodeError()
		}
		proof.step = step
	} else if normalized := normalizeMFARecoveryCode(code); normalized != "" {
		proof.recoveryHash = model.HashToken(normalized)
	} else {
		return mfaFactorProof{}, mfaInvalidCodeError()
	}
	return proof, nil
}

type sealedMFASecret struct {
	encoded string
	keyID   string
}

func (s *mfaMechanics) sealTOTPSecret(userID string, secret string) (sealedMFASecret, error) {
	if s == nil || s.deps.Sealer == nil {
		return sealedMFASecret{}, errors.New("MFA secret sealing is unavailable")
	}
	envelope, err := s.deps.Sealer.Seal(
		secretseal.Binding{Purpose: jobhandler.MFASealingPurpose, Owner: userID},
		[]byte(secret),
	)
	if err != nil {
		return sealedMFASecret{}, err
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return sealedMFASecret{}, errors.New("encode MFA sealed secret")
	}
	return sealedMFASecret{encoded: string(encoded), keyID: envelope.KeyID}, nil
}

func (s *mfaMechanics) decrypt(
	userID string,
	credential *model.MFACredential,
) (string, error) {
	if s == nil || s.deps.Sealer == nil || credential == nil {
		return "", errors.New("MFA secret sealing is unavailable")
	}
	var envelope secretseal.Envelope
	if err := json.Unmarshal([]byte(credential.EncryptedSecret), &envelope); err != nil ||
		envelope.KeyID != credential.EncryptionKeyID {
		return "", secretseal.ErrInvalidEnvelope
	}
	plaintext, err := s.deps.Sealer.Open(
		secretseal.Binding{Purpose: jobhandler.MFASealingPurpose, Owner: userID},
		envelope,
	)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
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

func mfaInvalidCodeError() error {
	return apperror.New("authentication.mfa.code_invalid")
}

var mfaStoreErrors = apperror.StoreTable{
	Rules: []apperror.StoreRule{
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrAuthenticationGenerationChanged, Code: "authentication.mfa.conflict"},
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrMFAReenrollmentRequired, Code: "authentication.mfa.conflict"},
	},
	// A secret sealed with a primary the key-ring fence has moved past means
	// this node's MFA key configuration is stale: unavailable, not a conflict
	// the User could resolve.
	Constraints: map[string]apperror.Code{"stale_primary_key": "authentication.mfa.unavailable"},
	NotFound:    "authentication.mfa.not_found",
	Conflict:    "authentication.mfa.conflict",
	Unavailable: "authentication.mfa.unavailable",
}

type mfaSecretKeyStore interface {
	InspectKeyState(context.Context) (*store.MFAKeyState, error)
}

// validateMFASecretKeys fails startup when a stored TOTP secret is sealed with
// a key the configured ring no longer holds. Without it, dropping an old key
// from Authentication.MFA.DecryptionKeys silently strands every secret sealed
// with that key until each affected User fails to sign in.
func validateMFASecretKeys(ctx context.Context, persistence mfaSecretKeyStore, sealer interface{ HasKey(string) bool }) error {
	if persistence == nil {
		return errors.New("MFA secret-key persistence is unavailable")
	}
	state, err := persistence.InspectKeyState(ctx)
	if err != nil {
		return fmt.Errorf("inspect MFA secret keys: %w", err)
	}
	if state == nil {
		return errors.New("MFA secret-key state is unavailable")
	}
	// Every new secret must be sealed with the fenced primary, so a ring
	// without it could write nothing.
	if state.RequiredPrimaryKeyID != "" && (sealer == nil || !sealer.HasKey(state.RequiredPrimaryKeyID)) {
		return fmt.Errorf("MFA primary key %s required by the last rekey is not configured; restore it to Authentication.MFA", state.RequiredPrimaryKeyID)
	}
	for _, item := range state.Active {
		if sealer == nil || !sealer.HasKey(item.KeyID) {
			return fmt.Errorf("MFA secret key %s is not configured but seals %d stored credentials; restore it to Authentication.MFA.DecryptionKeys", item.KeyID, item.Credentials)
		}
	}
	return nil
}
