//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestDesktopAuthorizationContinuesAcrossNodesAndCreatesAnOrdinaryRotatingSession(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	primaryStore := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, primaryStore)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	publicOrigin := func(cfg *config.Config) { cfg.Server.PublicURL = "https://proctor.example.edu" }
	build := model.DesktopBuildTuple{
		DesktopRelease: "0.1.0", DesktopBuildID: "integration-build",
		Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64,
		RealtimeProtocol:                        1,
		AttemptConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(),
		DesktopSettingsRegistryFingerprint:      "sha256:" + strings.Repeat("b", 64),
		CapabilityMatrixIdentity:                "integration-matrix",
	}
	primary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(primaryStore), testlib.WithDesktopBuildCatalog(build))
	secondary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(secondaryStore), testlib.WithDesktopBuildCatalog(build))

	ctx := context.Background()
	institution, err := primaryStore.Institution().Save(ctx, &model.Institution{
		Name: "desktop-auth", DisplayName: "Desktop Authorization",
	})
	if err != nil || institution == nil {
		t.Fatalf("save institution: %#v, %v", institution, err)
	}
	user, err := primary.App.CreateLocalUser(ctx, &model.User{
		Username: "desktop-user", Email: "desktop-user@example.edu",
	}, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	web, err := primary.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: user.Username, Password: "correct horse battery staple",
		ClientType: model.SessionClientWeb, DeviceID: "browser", Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := secondary.App.AuthenticateAccess(ctx, web.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	state := model.NewCredentialToken()
	verifier := model.NewCredentialToken()
	privateKey, publicJWK := integrationDesktopDPoPKey(t)
	started, err := primary.App.StartDesktopAuthorization(ctx, application.Invocation{}, application.StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		State:       state, CodeChallenge: model.PKCES256Challenge(verifier),
		DeviceID: "desktop-device", DeviceName: "Exam laptop",
		PublicJWK: publicJWK, DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID,
		Platform: build.Platform, Architecture: build.Architecture, RealtimeProtocol: build.RealtimeProtocol,
		Source: "127.0.0.1:2",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := secondary.App.BindDesktopAuthorization(ctx, application.Invocation{}, application.BindDesktopAuthorizationCommand{
		Handle: authorizationURL.Query().Get("request"), BrowserProof: authorizationURL.Fragment[len("proof="):], State: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := secondary.App.AuthenticateDesktopAuthorizationSession(ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "desktop-node-b"}),
		application.AuthenticateDesktopAuthorizationCommand{Binding: binding.Binding})
	if err != nil || authenticated.Account == nil || authenticated.Account.ID != user.ID {
		t.Fatalf("authenticate Desktop authorization: %#v, %v", authenticated, err)
	}
	approved, err := secondary.App.ApproveDesktopAuthorization(ctx, application.Invocation{},
		application.ApproveDesktopAuthorizationCommand{Binding: binding.Binding, State: state})
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(approved.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	if callback.Query().Get("state") != state || len(callback.Query()) != 2 {
		t.Fatalf("callback query = %#v", callback.Query())
	}
	proof := integrationDesktopDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": "https://proctor.example.edu/api/v1/auth/desktop/token",
		"iat": time.Now().Unix(), "nonce": started.DPoPNonce,
	})
	exchanged, err := primary.App.ExchangeDesktopAuthorization(ctx, application.Invocation{}, application.ExchangeDesktopAuthorizationCommand{
		Code: callback.Query().Get("code"), State: state, CodeVerifier: verifier,
		DPoPProof: proof, PublicJWK: publicJWK,
		DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID,
		Platform: build.Platform, Architecture: build.Architecture, RealtimeProtocol: build.RealtimeProtocol,
		Source: "127.0.0.1:2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exchanged.Session.ClientType != model.SessionClientDesktop || exchanged.Session.UserID != user.ID ||
		exchanged.Registration == nil || exchanged.Session.DesktopRegistrationID != exchanged.Registration.ID ||
		exchanged.Tokens == nil || exchanged.Tokens.TokenType != "DPoP" || exchanged.Tokens.AccessToken == "" || exchanged.Tokens.RefreshToken == "" {
		t.Fatalf("desktop exchange = %#v", exchanged)
	}
	if _, _, bearerErr := secondary.App.RefreshSession(ctx, application.Invocation{}, application.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
	}); !application.Is(bearerErr, "authentication.invalid_token") {
		t.Fatalf("Desktop refresh accepted as Bearer: %v", bearerErr)
	}
	refreshProof := integrationDesktopDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": "https://proctor.example.edu/api/v1/auth/refresh",
		"iat": time.Now().Unix(), "nonce": exchanged.DPoPNonce,
	})
	rotatedSession, rotated, err := secondary.App.RefreshSession(ctx, application.Invocation{}, application.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
		DPoP:         &application.DPoPRequestProof{Proof: refreshProof, Method: "POST", Path: "/api/v1/auth/refresh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotatedSession.ID != exchanged.Session.ID || rotated.AccessToken == exchanged.Tokens.AccessToken ||
		rotated.RefreshToken == exchanged.Tokens.RefreshToken {
		t.Fatalf("desktop refresh did not use ordinary rotation: session=%#v tokens=%#v", rotatedSession, rotated)
	}
	if _, err = primary.App.AuthenticateAccess(ctx, exchanged.Tokens.AccessToken); !application.Is(err, "authentication.invalid_token") {
		t.Fatalf("old desktop access after rotation error = %v", err)
	}
}

func integrationDesktopDPoPKey(t *testing.T) (*ecdsa.PrivateKey, model.DesktopPublicJWK) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, model.DesktopPublicJWK{
		Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	}
}

func integrationDesktopDPoPProof(
	t *testing.T,
	privateKey *ecdsa.PrivateKey,
	publicJWK model.DesktopPublicJWK,
	claims map[string]any,
) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": publicJWK})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encodedHeader + "." + encodedPayload))
	r, signatureS, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...)
	return encodedHeader + "." + encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}
