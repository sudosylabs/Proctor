//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/app/user_test.go MFA lifecycle
// coverage. Proctor additionally verifies encrypted persistence, one-time
// recovery credentials, login assurance, cache invalidation, and redaction.

package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

type wireMFASetup struct {
	Secret          string    `json:"secret"`
	ProvisioningURI string    `json:"provisioning_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type wireMFAActivation struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type wireMFAStatus struct {
	Enabled                bool      `json:"enabled"`
	Pending                bool      `json:"pending"`
	PendingExpiresAt       time.Time `json:"pending_expires_at,omitempty"`
	RecoveryCodesRemaining int       `json:"recovery_codes_remaining"`
}

func TestMFAIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	encryptionKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.MFA.Enabled = true
			cfg.Authentication.MFA.EncryptionKey = encryptionKey
		}),
		testlib.WithStore(persistence),
	)
	bootstrap := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "northbridge", "display_name": "Northbridge University"},
		"administrator":    map[string]any{"username": "mfa-admin", "email": "mfa-admin@example.edu"},
		"password":         "bootstrap correct horse battery staple",
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	startIntegrationServer(t, helper)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(
		context.Background(),
		app.CreateLocalUserCommand{User: &model.User{
			Username: "mfa-user", Email: "mfa-user@example.edu",
			DisplayName: "MFA User",
		}, Password: password},
	)
	require.NoError(t, appErr)
	initial := loginIntegrationUser(
		t,
		helper.Handler(),
		user.Username,
		password,
		model.SessionClientCLI,
		"mfa-initial",
	)
	setupResponse := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/setup",
		nil,
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusCreated, setupResponse.Code, "MFA setup status = %d: %s", setupResponse.Code, setupResponse.Body.String())
	var setup wireMFASetup
	require.NoError(t, json.Unmarshal(setupResponse.Body.Bytes(), &setup))
	require.NotEmpty(t, setup.Secret)
	require.True(t, strings.HasPrefix(setup.ProvisioningURI, "otpauth://totp/"))
	require.Equal(t, "no-store", setupResponse.Header().Get("Cache-Control"))
	persisted, err := persistence.MFA().GetByUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.NotEqual(t, setup.Secret, persisted.EncryptedSecret, "TOTP secret was not encrypted at rest")
	require.NotContains(t, persisted.EncryptedSecret, setup.Secret, "TOTP secret was not encrypted at rest")
	var envelope secretseal.Envelope
	require.NoError(t, json.Unmarshal([]byte(persisted.EncryptedSecret), &envelope))
	require.Equal(t, secretseal.EnvelopeVersion1, envelope.Version, "persisted MFA envelope = %#v; key reference", envelope)
	require.Equal(t, secretseal.AlgorithmAES256GCM, envelope.Algorithm, "persisted MFA envelope = %#v; key reference", envelope)
	require.Equal(t, persisted.EncryptionKeyID, envelope.KeyID, "persisted MFA envelope = %#v; key reference", envelope)
	code := integrationTOTP(t, setup.Secret, time.Now().UTC().Unix()/30)
	activateResponse := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/activate",
		map[string]any{"code": code},
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, activateResponse.Code, "MFA activation status = %d: %s; logs=%s", activateResponse.Code, activateResponse.Body.String(), helper.Logs.String())
	var activation wireMFAActivation
	require.NoError(t, json.Unmarshal(activateResponse.Body.Bytes(), &activation))
	require.Len(t, activation.RecoveryCodes, helper.ConfigStore.Get().Authentication.MFA.RecoveryCodeCount)
	statusResponse := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/mfa",
		nil,
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, statusResponse.Code, "MFA status = %d: %s", statusResponse.Code, statusResponse.Body.String())
	var status wireMFAStatus
	require.NoError(t, json.Unmarshal(statusResponse.Body.Bytes(), &status))
	require.True(t, status.Enabled)
	require.Len(t, activation.RecoveryCodes, status.RecoveryCodesRemaining)
	rechallenge := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/challenge",
		map[string]any{"code": activation.RecoveryCodes[1]},
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, rechallenge.Code, "MFA rechallenge status = %d: %s", rechallenge.Code, rechallenge.Body.String())
	var rechallengedWire wireSessionResponse
	require.NoError(t, json.Unmarshal(rechallenge.Body.Bytes(), &rechallengedWire))
	rechallenged := rechallengedWire.model()
	require.Equal(t, model.AuthenticationMultiFactor, rechallenged.AuthenticationStrength)
	require.True(t, rechallenged.MFACompletedAt.Valid)
	withoutSecondFactor := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	require.Equal(t, http.StatusUnauthorized, withoutSecondFactor.Code, "MFA-required login status = %d: %s", withoutSecondFactor.Code, withoutSecondFactor.Body.String())
	recoveryCode := activation.RecoveryCodes[0]
	recoveryLogin := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"mfa_code": recoveryCode, "client_type": model.SessionClientCLI,
			"device_id": "mfa-recovery",
		},
		"",
	)
	require.Equal(t, http.StatusOK, recoveryLogin.Code, "MFA recovery login status = %d: %s", recoveryLogin.Code, recoveryLogin.Body.String())
	recovered := decodeAuthenticationResponse(t, recoveryLogin)
	require.Equal(t, model.AuthenticationMultiFactor, recovered.Session.AuthenticationStrength)
	require.True(t, recovered.Session.MFACompletedAt.Valid)
	replay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"mfa_code": recoveryCode, "client_type": model.SessionClientCLI,
		},
		"",
	)
	require.Equal(t, http.StatusUnauthorized, replay.Code, "MFA recovery replay status = %d: %s", replay.Code, replay.Body.String())
	regenerate := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/recovery-codes/regenerate",
		nil,
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, regenerate.Code, "MFA recovery regeneration = %d: %s", regenerate.Code, regenerate.Body.String())
	var regenerated struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	require.NoError(t, json.Unmarshal(regenerate.Body.Bytes(), &regenerated))
	require.Len(t, regenerated.RecoveryCodes, len(activation.RecoveryCodes))
	require.NotEqual(t, activation.RecoveryCodes[0], regenerated.RecoveryCodes[0])
	disable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/disable",
		nil,
		initial.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, disable.Code, "MFA disable = %d: %s", disable.Code, disable.Body.String())
	afterDisable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	require.Equal(t, http.StatusOK, afterDisable.Code, "post-MFA login status = %d: %s", afterDisable.Code, afterDisable.Body.String())
	disabledLogin := decodeAuthenticationResponse(t, afterDisable)
	require.Equal(t, model.AuthenticationSingleFactor, disabledLogin.Session.AuthenticationStrength)

	transitionKeys := []model.MailTemplateKey{
		model.MailTemplateIdentityMFAEnabled,
		model.MailTemplateIdentityMFARecoveryCodesRegenerated,
		model.MailTemplateIdentityMFADisabled,
	}
	deadline := time.Now().Add(10 * time.Second)
	var transitionDeliveries []*model.MailDelivery
	for {
		transitionDeliveries, err = persistence.Mail().ListDeliveries(context.Background(), store.MailDeliveryListOptions{TemplateKeys: transitionKeys, Limit: 20})
		require.NoError(t, err)
		accepted := len(transitionDeliveries) == len(transitionKeys)
		for _, delivery := range transitionDeliveries {
			accepted = accepted && delivery.TargetUserID == user.ID && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0
		}
		if accepted {
			break
		}
		require.False(t, time.Now().After(deadline), "MFA transition deliveries did not reach accepted: %#v", transitionDeliveries)
		time.Sleep(20 * time.Millisecond)
	}
	captured := helper.Mailer.Deliveries()
	require.Len(t, captured, len(transitionKeys), "captured MFA notices")

	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	require.NoError(t, err)
	encodedAudits, err := json.Marshal(audits)
	require.NoError(t, err)
	sensitiveValues := append(
		[]string{setup.Secret, persisted.EncryptedSecret},
		activation.RecoveryCodes...,
	)
	sensitiveValues = append(sensitiveValues, regenerated.RecoveryCodes...)
	encodedDeliveries, err := json.Marshal(transitionDeliveries)
	require.NoError(t, err)
	var encodedJobs []byte
	for _, delivery := range transitionDeliveries {
		job, jobErr := persistence.Job().Get(context.Background(), delivery.JobID)
		require.NoError(t, jobErr)
		encodedJob, encodeErr := json.Marshal(job)
		require.NoError(t, encodeErr)
		encodedJobs = append(encodedJobs, encodedJob...)
	}
	for _, sensitive := range sensitiveValues {
		require.False(t, bytes.Contains(encodedAudits, []byte(sensitive)), "MFA secret or recovery credential leaked")
		require.False(t, bytes.Contains(encodedDeliveries, []byte(sensitive)), "MFA secret or recovery credential leaked")
		require.False(t, bytes.Contains(encodedJobs, []byte(sensitive)), "MFA secret or recovery credential leaked")
		require.NotContains(t, helper.Logs.String(), sensitive, "MFA secret or recovery credential leaked")
		for _, delivery := range helper.Mailer.Deliveries() {
			require.False(t, bytes.Contains(delivery.Data, []byte(sensitive)), "MFA secret or recovery credential appeared in security notice")
		}
	}
}

func integrationTOTP(t *testing.T, secret string, timeStep int64) string {
	t.Helper()
	key, err := base32.StdEncoding.DecodeString(secret)
	require.NoError(t, err)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(timeStep))
	mac := hmac.New(sha1.New, key)
	_, err = mac.Write(counter[:])
	require.NoError(t, err)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(digest[offset : offset+4])
	value := (truncated & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value)
}
