//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestDesktopAuthorizationContinuesAcrossNodesAndCreatesAnOrdinaryRotatingSession(t *testing.T) {
	testDesktopAuthorizationRotatingSession(t, false)
}

func TestDesktopDevelopmentAuthorizationCreatesAnOrdinaryRotatingSession(t *testing.T) {
	testDesktopAuthorizationRotatingSession(t, true)
}

func testDesktopAuthorizationRotatingSession(t *testing.T, development bool) {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, primaryStore)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	origin := "https://proctor.example.edu"
	if development {
		origin = "http://localhost:8065"
	}
	publicOrigin := func(cfg *config.Config) { cfg.Server.PublicURL = origin }
	build := model.DesktopBuildTuple{
		DesktopRelease: "0.1.0", DesktopBuildID: "integration-build",
		Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64,
		RealtimeProtocol:                        1,
		AttemptConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(),
		DesktopSettingsRegistryFingerprint:      "fnv1a64:" + strings.Repeat("b", 16),
		DesktopTarget:                           "darwin-arm64", ConfigurationManifest: model.EmptyAttemptConfigurationManifest(),
		CapabilityMatrixIdentity: "integration-matrix",
	}
	behavior := testlib.WithDesktopBuildCatalog(build)
	if development {
		// Use the actual environment projection with an empty release catalog.
		behavior = testlib.WithServiceEnvironment(server.ServiceEnvironmentDev)
	}

	primary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(primaryStore), behavior)
	secondary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(secondaryStore), behavior)

	ctx := context.Background()
	institution, err := primaryStore.Institution().Save(ctx, &model.Institution{
		Name: "desktop-auth", DisplayName: "Desktop Authorization",
	})
	require.NoError(t, err, "save institution")
	require.NotNil(t, institution, "save institution")
	compatibility, err := primary.App.EvaluateDesktopCompatibility(t.Context(), app.DesktopCompatibilityQuery{
		DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID, Platform: string(build.Platform), Architecture: string(build.Architecture), RealtimeProtocol: build.RealtimeProtocol,
	})
	require.NoError(t, err, "configured build unavailable: %#v", compatibility)
	require.Equal(t, app.DesktopCompatibilityCompatible, compatibility.Compatibility, "configured build unavailable: %#v", compatibility)

	if development {
		// The test graph must ignore the host environment and default to strict
		// compatibility even when the test binary itself was built without production.
		strict := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(openAdditionalUserSettingsStore(t, dataSource)))
		result, err := strict.App.EvaluateDesktopCompatibility(t.Context(), app.DesktopCompatibilityQuery{
			DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID, Platform: string(build.Platform), Architecture: string(build.Architecture), RealtimeProtocol: build.RealtimeProtocol,
		})
		require.NoError(t, err, "default test graph bypassed compatibility: %#v", result)
		require.Equal(t, app.DesktopCompatibilityUnsupportedTarget, result.Compatibility, "default test graph bypassed compatibility: %#v", result)
	}

	user, err := primary.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "desktop-user", Email: "desktop-user@example.edu",
	}, Password: "correct horse battery staple"})
	require.NoError(t, err)
	web, err := primary.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{
		LoginID: user.Username, Password: "correct horse battery staple",
		ClientType: model.SessionClientWeb, DeviceID: "browser", Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := secondary.App.AuthenticateAccess(ctx, web.Tokens.AccessToken)
	require.NoError(t, err)

	state := model.NewCredentialToken()
	verifier := model.NewCredentialToken()
	privateKey, publicJWK := integrationDesktopDPoPKey(t)
	started, err := primary.App.StartDesktopAuthorization(ctx, invocation.Invocation{}, app.StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		State:       state, CodeChallenge: model.PKCES256Challenge(verifier),
		DeviceID: "desktop-device", DeviceName: "Exam laptop",
		PublicJWK: publicJWK, DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID,
		Platform: build.Platform, Architecture: build.Architecture, RealtimeProtocol: build.RealtimeProtocol,
		Source: "127.0.0.1:2",
	})
	require.NoError(t, err)
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	require.NoError(t, err)
	binding, err := secondary.App.BindDesktopAuthorization(ctx, invocation.Invocation{}, app.BindDesktopAuthorizationCommand{
		Handle: authorizationURL.Query().Get("request"), BrowserProof: authorizationURL.Fragment[len("proof="):], State: state,
	})
	require.NoError(t, err)
	authenticated, err := secondary.App.AuthenticateDesktopAuthorizationSession(ctx,
		invocation.New(*principal, model.RequestMetadata{RequestID: "desktop-node-b"}),
		app.AuthenticateDesktopAuthorizationCommand{Binding: binding.Binding})
	require.NoError(t, err, "authenticate Desktop authorization: %#v", authenticated)
	require.NotNil(t, authenticated.Account, "authenticate Desktop authorization: %#v", authenticated)
	require.Equal(t, user.ID, authenticated.Account.ID, "authenticate Desktop authorization: %#v", authenticated)
	approved, err := secondary.App.ApproveDesktopAuthorization(ctx, invocation.Invocation{},
		app.ApproveDesktopAuthorizationCommand{Binding: binding.Binding, State: state})
	require.NoError(t, err)
	callback, err := url.Parse(approved.RedirectURL)
	require.NoError(t, err)
	require.Equal(t, state, callback.Query().Get("state"))
	require.Len(t, callback.Query(), 2)
	proof := integrationDesktopDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": origin + "/api/v1/auth/desktop/token",
		"iat": time.Now().Unix(), "nonce": started.DPoPNonce,
	})
	exchanged, err := primary.App.ExchangeDesktopAuthorization(ctx, invocation.Invocation{}, app.ExchangeDesktopAuthorizationCommand{
		Code: callback.Query().Get("code"), State: state, CodeVerifier: verifier,
		DPoPProof: proof, PublicJWK: publicJWK,
		DesktopRelease: build.DesktopRelease, DesktopBuildID: build.DesktopBuildID,
		Platform: build.Platform, Architecture: build.Architecture, RealtimeProtocol: build.RealtimeProtocol,
		Source: "127.0.0.1:2",
	})
	require.NoError(t, err)
	require.Equal(t, model.SessionClientDesktop, exchanged.Session.ClientType)
	require.Equal(t, user.ID, exchanged.Session.UserID)
	require.NotNil(t, exchanged.Registration)
	require.Equal(t, exchanged.Registration.ID, exchanged.Session.DesktopRegistrationID)
	require.NotNil(t, exchanged.Tokens)
	require.Equal(t, "DPoP", exchanged.Tokens.TokenType)
	require.NotEmpty(t, exchanged.Tokens.AccessToken)
	require.NotEmpty(t, exchanged.Tokens.RefreshToken)
	for _, action := range []string{"authentication.desktop_authorization", "authentication.desktop_session_issue"} {
		events, listErr := primaryStore.Audit().List(ctx, store.AuditListOptions{Action: action, ActorID: user.ID,
			Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
		require.NoError(t, listErr, "%s audits for approving account", action)
		require.Len(t, events, 1, "%s audits for approving account", action)
		require.Equal(t, model.AuditStatusSuccess, events[0].Status, "%s audits for approving account", action)
	}
	_, _, bearerErr := secondary.App.RefreshSession(ctx, invocation.Invocation{}, app.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
	})
	require.True(t, apperror.Is(bearerErr, "authentication.token_invalid"), "Desktop refresh accepted as Bearer: %v", bearerErr)
	refreshProof := integrationDesktopDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": origin + "/api/v1/auth/refresh",
		"iat": time.Now().Unix(), "nonce": exchanged.DPoPNonce,
	})
	_, _, err = secondary.App.RefreshSession(ctx, invocation.Invocation{}, app.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
		DPoP:         &app.DPoPRequestProof{Proof: refreshProof, Method: "POST", Path: "/api/v1/auth/refresh"},
	})
	// The two test nodes have independent disposable caches, so the secondary
	// challenges the primary's nonce before the client retries with fresh proof.
	require.True(t, apperror.Is(err, "authentication.dpop.use_nonce"), "cross-node refresh error = %v, want nonce challenge", err)
	refreshNonce, ok := app.DPoPChallengeNonce(err)
	require.True(t, ok, "cross-node refresh challenge did not provide a replacement nonce")
	refreshProof = integrationDesktopDPoPProof(t, privateKey, publicJWK, map[string]any{
		"jti": model.NewCredentialToken(), "htm": "POST",
		"htu": origin + "/api/v1/auth/refresh",
		"iat": time.Now().Unix(), "nonce": refreshNonce,
	})
	rotatedSession, rotated, err := secondary.App.RefreshSession(ctx, invocation.Invocation{}, app.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
		DPoP:         &app.DPoPRequestProof{Proof: refreshProof, Method: "POST", Path: "/api/v1/auth/refresh"},
	})
	require.NoError(t, err)
	require.Equal(t, exchanged.Session.ID, rotatedSession.ID, "desktop refresh did not use ordinary rotation: session=%#v tokens=%#v", rotatedSession, rotated)
	require.NotEqual(t, exchanged.Tokens.AccessToken, rotated.AccessToken, "desktop refresh did not use ordinary rotation: session=%#v tokens=%#v", rotatedSession, rotated)
	require.NotEqual(t, exchanged.Tokens.RefreshToken, rotated.RefreshToken, "desktop refresh did not use ordinary rotation: session=%#v tokens=%#v", rotatedSession, rotated)
	_, err = primary.App.AuthenticateAccess(ctx, exchanged.Tokens.AccessToken)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "old desktop access after rotation error = %v", err)
}

func integrationDesktopDPoPKey(t *testing.T) (*ecdsa.PrivateKey, model.DesktopPublicJWK) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
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
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(encodedHeader + "." + encodedPayload))
	r, signatureS, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	require.NoError(t, err)
	signature := append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...)
	return encodedHeader + "." + encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}
