//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"net/url"
	"os"
	"testing"

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
	primary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(primaryStore))
	secondary := testlib.Setup(t, testlib.WithConfig(publicOrigin), testlib.WithStore(secondaryStore))

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
	started, err := primary.App.StartDesktopAuthorization(ctx, application.Invocation{}, application.StartDesktopAuthorizationCommand{
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		State:       state, CodeChallenge: model.PKCES256Challenge(verifier),
		AuthenticationMethod: "password", DeviceID: "desktop-device", DeviceName: "Exam laptop",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := secondary.App.ApproveDesktopAuthorization(ctx,
		application.NewInvocation(*principal, model.RequestMetadata{RequestID: "desktop-node-b"}),
		application.ApproveDesktopAuthorizationCommand{
			Handle:       authorizationURL.Query().Get("request"),
			BrowserProof: authorizationURL.Fragment[len("proof="):], State: state,
		})
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
	exchanged, err := primary.App.ExchangeDesktopAuthorization(ctx, application.Invocation{}, application.ExchangeDesktopAuthorizationCommand{
		Code: callback.Query().Get("code"), State: state, CodeVerifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if exchanged.Session.ClientType != model.SessionClientDesktop || exchanged.Session.UserID != user.ID ||
		exchanged.Tokens == nil || exchanged.Tokens.AccessToken == "" || exchanged.Tokens.RefreshToken == "" {
		t.Fatalf("desktop exchange = %#v", exchanged)
	}
	rotatedSession, rotated, err := secondary.App.RefreshSession(ctx, application.Invocation{}, application.RefreshSessionCommand{
		RefreshToken: exchanged.Tokens.RefreshToken,
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
