// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/websocket"
)

type lifecycleApplication struct{}

func (lifecycleApplication) AuthorizeWebSocketSubscription(
	context.Context,
	app.Invocation,
	model.Action,
	model.Resource,
) error {
	return nil
}

func (lifecycleApplication) ValidateWebSocketPrincipal(
	context.Context,
	model.Principal,
) error {
	return nil
}

type lifecycleLogger struct{}

func (lifecycleLogger) WarnContext(context.Context, string, error) {}

func newTestHub(t *testing.T) *websocket.Hub {
	t.Helper()
	hub, err := websocket.NewHub(
		lifecycleApplication{},
		lifecycleLogger{},
		"https://proctor.example",
		"node-a",
		nil,
		15*time.Second,
	)
	require.NoError(t, err)
	return hub
}

func TestHubConstructionIsInertAndCloseWithoutStart(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	require.False(t, hub.Started(), "NewHub() started the hub")
	done := make(chan error, 1)
	go func() {
		done <- hub.Close()
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "Close() without Start blocked; construction likely started a goroutine")
	}
	require.False(t, hub.Started(), "Close() left the hub started")
}

func TestHubStartCloseIdempotent(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	ctx := context.Background()
	require.NoError(t, hub.Start(ctx))
	require.True(t, hub.Started(), "Start() left hub unstarted")
	require.NoError(t, hub.Start(ctx))
	require.NoError(t, hub.Close())
	require.False(t, hub.Started(), "Close() left hub started")
	require.NoError(t, hub.Close())
	require.Error(t, hub.Start(ctx), "Start() after Close() succeeded")
}

func TestHubAcceptRequiresStart(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	request := httptest.NewRequest(http.MethodGet, "https://proctor.example/api/v1/websocket", nil)
	request.Header.Set("Origin", "https://proctor.example")
	recorder := httptest.NewRecorder()
	err := hub.Accept(
		recorder,
		request,
		model.Principal{
			UserID:         model.NewUserID(),
			SessionID:      model.NewSessionID(),
			CredentialID:   model.PrincipalCredentialID(model.NewID()),
			CredentialType: model.CredentialSessionAccess,
		},
		model.RequestMetadata{},
		"",
		0,
		false,
	)
	require.True(t, apperror.Is(err, "websocket.unavailable"), "Accept() before Start error = %v, want websocket.unavailable", err)
}

// TestHubAcceptReturnsRefusedHandshakeUncommitted keeps a malformed upgrade
// request on the HTTP Problem Details path instead of the upgrader's
// plain-text error body.
func TestHubAcceptReturnsRefusedHandshakeUncommitted(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	require.NoError(t, hub.Start(t.Context()))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://proctor.example/api/v1/websocket", nil)
	request.Header.Set("Origin", "https://proctor.example")
	recorder := httptest.NewRecorder()
	err := hub.Accept(recorder, request, model.Principal{
		UserID:         model.NewUserID(),
		SessionID:      model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess,
	}, model.RequestMetadata{}, "", 0, false)
	require.True(t, apperror.Is(err, "request.invalid"), "Accept() without upgrade headers error = %v, want request.invalid", err)
	require.Equal(t, 0, recorder.Body.Len(), "refused handshake committed a response: %d %q", recorder.Code, recorder.Body.String())
	require.Equal(t, http.StatusOK, recorder.Code, "refused handshake committed a response: %d %q", recorder.Code, recorder.Body.String())
	require.False(t, recorder.Flushed, "refused handshake committed a response: %d %q", recorder.Code, recorder.Body.String())
}

func TestHubOriginAllowed(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	require.True(t, hub.OriginAllowed("https://proctor.example", false), "matching origin rejected")
	require.False(t, hub.OriginAllowed("https://evil.example", false), "cross-origin allowed")
	require.False(t, hub.OriginAllowed("", false), "missing origin allowed for cookie clients")
	require.True(t, hub.OriginAllowed("", true), "missing origin rejected for bearer clients")
}

// TestHubOriginAllowedMatchesHTTPOriginNormalization keeps browser
// WebSocket upgrades consistent with JSON request origin checks when the
// public URL names a default port or mixed-case host.
func TestHubOriginAllowedMatchesHTTPOriginNormalization(t *testing.T) {
	t.Parallel()

	for _, publicURL := range []string{"https://Proctor.Example:443/", "https://proctor.example", "HTTPS://PROCTOR.EXAMPLE"} {
		hub, err := websocket.NewHub(lifecycleApplication{}, lifecycleLogger{}, publicURL, "node-a", nil, 15*time.Second)
		require.NoError(t, err, "NewHub(%q) error", publicURL)
		t.Cleanup(func() { _ = hub.Close() })
		for _, origin := range []string{"https://proctor.example", "https://PROCTOR.example", "https://proctor.example:443"} {
			require.True(t, hub.OriginAllowed(origin, false), "public URL %q rejected browser origin %q", publicURL, origin)
		}
		for _, origin := range []string{"http://proctor.example", "https://proctor.example:8443", "https://proctor.example/path", "null", " https://proctor.example"} {
			require.False(t, hub.OriginAllowed(origin, false), "public URL %q allowed origin %q", publicURL, origin)
		}
	}
}

func TestHubStartRespectsCanceledContext(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, hub.Start(ctx), context.Canceled, "Start(canceled) error")
	require.False(t, hub.Started(), "Start(canceled) started the hub")
}
