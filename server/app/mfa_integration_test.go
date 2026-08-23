//go:build integration

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
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

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

type wireMFASetup struct {
	Secret          string `json:"secret"`
	ProvisioningURI string `json:"provisioning_uri"`
	ExpiresAt       int64  `json:"expires_at"`
}

type wireMFAActivation struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type wireMFAStatus struct {
	Enabled                bool  `json:"enabled"`
	Pending                bool  `json:"pending"`
	PendingExpiresAt       int64 `json:"pending_expires_at,omitempty"`
	RecoveryCodesRemaining int   `json:"recovery_codes_remaining"`
}

func TestMFAIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	startIntegrationServer(t, helper)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(
		context.Background(),
		&model.User{
			Username: "mfa-user", Email: "mfa-user@example.edu",
			DisplayName: "MFA User",
		},
		password,
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
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
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf(
			"MFA setup status = %d: %s",
			setupResponse.Code,
			setupResponse.Body.String(),
		)
	}
	var setup wireMFASetup
	if err := json.Unmarshal(setupResponse.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.Secret == "" ||
		!strings.HasPrefix(setup.ProvisioningURI, "otpauth://totp/") ||
		setupResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("MFA setup = %#v", setup)
	}
	persisted, err := persistence.MFA().GetByUser(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.EncryptedSecret == setup.Secret ||
		strings.Contains(persisted.EncryptedSecret, setup.Secret) {
		t.Fatal("TOTP secret was not encrypted at rest")
	}
	var envelope secretseal.Envelope
	if err := json.Unmarshal([]byte(persisted.EncryptedSecret), &envelope); err != nil {
		t.Fatalf("persisted MFA secret is not a versioned envelope: %v", err)
	}
	if envelope.Version != secretseal.EnvelopeVersion1 ||
		envelope.Algorithm != secretseal.AlgorithmAES256GCM ||
		envelope.KeyID != persisted.EncryptionKeyID {
		t.Fatalf("persisted MFA envelope = %#v; key reference = %q", envelope, persisted.EncryptionKeyID)
	}
	code := integrationTOTP(t, setup.Secret, time.Now().UTC().Unix()/30)
	activateResponse := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/activate",
		map[string]any{"code": code},
		initial.Tokens.AccessToken,
	)
	if activateResponse.Code != http.StatusOK {
		t.Fatalf(
			"MFA activation status = %d: %s; logs=%s",
			activateResponse.Code,
			activateResponse.Body.String(),
			helper.Logs.String(),
		)
	}
	var activation wireMFAActivation
	if err := json.Unmarshal(activateResponse.Body.Bytes(), &activation); err != nil {
		t.Fatal(err)
	}
	if len(activation.RecoveryCodes) !=
		helper.ConfigStore.Get().Authentication.MFA.RecoveryCodeCount {
		t.Fatalf("MFA recovery codes = %#v", activation.RecoveryCodes)
	}
	statusResponse := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/mfa",
		nil,
		initial.Tokens.AccessToken,
	)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf(
			"MFA status = %d: %s",
			statusResponse.Code,
			statusResponse.Body.String(),
		)
	}
	var status wireMFAStatus
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled ||
		status.RecoveryCodesRemaining != len(activation.RecoveryCodes) {
		t.Fatalf("MFA status = %#v", status)
	}
	rechallenge := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/challenge",
		map[string]any{"code": activation.RecoveryCodes[1]},
		initial.Tokens.AccessToken,
	)
	if rechallenge.Code != http.StatusOK {
		t.Fatalf(
			"MFA rechallenge status = %d: %s",
			rechallenge.Code,
			rechallenge.Body.String(),
		)
	}
	var rechallengedWire wireSessionResponse
	if err := json.Unmarshal(rechallenge.Body.Bytes(), &rechallengedWire); err != nil {
		t.Fatal(err)
	}
	rechallenged := rechallengedWire.model()
	if rechallenged.AuthenticationStrength != model.AuthenticationMultiFactor ||
		!rechallenged.MFACompletedAt.Valid {
		t.Fatalf("MFA rechallenge session = %#v", rechallenged)
	}
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
	if withoutSecondFactor.Code != http.StatusUnauthorized {
		t.Fatalf(
			"MFA-required login status = %d: %s",
			withoutSecondFactor.Code,
			withoutSecondFactor.Body.String(),
		)
	}
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
	if recoveryLogin.Code != http.StatusOK {
		t.Fatalf(
			"MFA recovery login status = %d: %s",
			recoveryLogin.Code,
			recoveryLogin.Body.String(),
		)
	}
	recovered := decodeAuthenticationResponse(t, recoveryLogin)
	if recovered.Session.AuthenticationStrength != model.AuthenticationMultiFactor ||
		!recovered.Session.MFACompletedAt.Valid {
		t.Fatalf("MFA login session = %#v", recovered.Session)
	}
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
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf(
			"MFA recovery replay status = %d: %s",
			replay.Code,
			replay.Body.String(),
		)
	}
	regenerate := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/recovery-codes/regenerate",
		nil,
		initial.Tokens.AccessToken,
	)
	if regenerate.Code != http.StatusOK {
		t.Fatalf(
			"MFA recovery regeneration = %d: %s",
			regenerate.Code,
			regenerate.Body.String(),
		)
	}
	var regenerated struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(regenerate.Body.Bytes(), &regenerated); err != nil {
		t.Fatal(err)
	}
	if len(regenerated.RecoveryCodes) != len(activation.RecoveryCodes) ||
		regenerated.RecoveryCodes[0] == activation.RecoveryCodes[0] {
		t.Fatalf("regenerated MFA recovery codes = %#v", regenerated)
	}
	disable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/mfa/disable",
		nil,
		initial.Tokens.AccessToken,
	)
	if disable.Code != http.StatusNoContent {
		t.Fatalf("MFA disable = %d: %s", disable.Code, disable.Body.String())
	}
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
	if afterDisable.Code != http.StatusOK {
		t.Fatalf(
			"post-MFA login status = %d: %s",
			afterDisable.Code,
			afterDisable.Body.String(),
		)
	}
	disabledLogin := decodeAuthenticationResponse(t, afterDisable)
	if disabledLogin.Session.AuthenticationStrength != model.AuthenticationSingleFactor {
		t.Fatalf("post-MFA session = %#v", disabledLogin.Session)
	}

	transitionKeys := []model.MailTemplateKey{
		model.MailTemplateIdentityMFAEnabled,
		model.MailTemplateIdentityMFARecoveryCodesRegenerated,
		model.MailTemplateIdentityMFADisabled,
	}
	deadline := time.Now().Add(10 * time.Second)
	var transitionDeliveries []*model.MailDelivery
	for {
		transitionDeliveries, err = persistence.Mail().ListDeliveries(context.Background(), store.MailDeliveryListOptions{TemplateKeys: transitionKeys, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		accepted := len(transitionDeliveries) == len(transitionKeys)
		for _, delivery := range transitionDeliveries {
			accepted = accepted && delivery.TargetUserID == user.ID && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0
		}
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("MFA transition deliveries did not reach accepted: %#v", transitionDeliveries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if captured := helper.Mailer.Deliveries(); len(captured) != len(transitionKeys) {
		t.Fatalf("captured MFA notices = %d, want %d", len(captured), len(transitionKeys))
	}

	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedAudits, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	sensitiveValues := append(
		[]string{setup.Secret, persisted.EncryptedSecret},
		activation.RecoveryCodes...,
	)
	sensitiveValues = append(sensitiveValues, regenerated.RecoveryCodes...)
	encodedDeliveries, err := json.Marshal(transitionDeliveries)
	if err != nil {
		t.Fatal(err)
	}
	var encodedJobs []byte
	for _, delivery := range transitionDeliveries {
		job, jobErr := persistence.Job().Get(context.Background(), delivery.JobID)
		if jobErr != nil {
			t.Fatal(jobErr)
		}
		encodedJob, encodeErr := json.Marshal(job)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		encodedJobs = append(encodedJobs, encodedJob...)
	}
	for _, sensitive := range sensitiveValues {
		if bytes.Contains(encodedAudits, []byte(sensitive)) ||
			bytes.Contains(encodedDeliveries, []byte(sensitive)) ||
			bytes.Contains(encodedJobs, []byte(sensitive)) ||
			strings.Contains(helper.Logs.String(), sensitive) {
			t.Fatal("MFA secret or recovery credential leaked")
		}
		for _, delivery := range helper.Mailer.Deliveries() {
			if bytes.Contains(delivery.Data, []byte(sensitive)) {
				t.Fatal("MFA secret or recovery credential appeared in security notice")
			}
		}
	}
}

func integrationTOTP(t *testing.T, secret string, timeStep int64) string {
	t.Helper()
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(timeStep))
	mac := hmac.New(sha1.New, key)
	if _, err := mac.Write(counter[:]); err != nil {
		t.Fatal(err)
	}
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(digest[offset : offset+4])
	value := (truncated & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value)
}
