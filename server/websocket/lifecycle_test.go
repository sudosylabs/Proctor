// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/websocket"
)

type lifecycleApplication struct{}

func (lifecycleApplication) AuthorizeWebSocketSubscription(
	context.Context,
	model.Principal,
	model.RequestMetadata,
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
	)
	if err != nil {
		t.Fatalf("NewHub() error = %v", err)
	}
	return hub
}

func TestHubConstructionIsInertAndCloseWithoutStart(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	if hub.Started() {
		t.Fatal("NewHub() started the hub")
	}
	done := make(chan error, 1)
	go func() {
		done <- hub.Close()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() without Start error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() without Start blocked; construction likely started a goroutine")
	}
	if hub.Started() {
		t.Fatal("Close() left the hub started")
	}
}

func TestHubStartCloseIdempotent(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	ctx := context.Background()
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !hub.Started() {
		t.Fatal("Start() left hub unstarted")
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("idempotent Start() error = %v", err)
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if hub.Started() {
		t.Fatal("Close() left hub started")
	}
	if err := hub.Close(); err != nil {
		t.Fatalf("idempotent Close() error = %v", err)
	}
	if err := hub.Start(ctx); err == nil {
		t.Fatal("Start() after Close() succeeded")
	}
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
			CredentialID:   model.PrincipalCredentialID(model.NewId()),
			CredentialType: model.CredentialSessionAccess,
		},
		model.RequestMetadata{},
		"",
		0,
		false,
	)
	if !app.Is(err, "websocket.unavailable") {
		t.Fatalf("Accept() before Start error = %v, want websocket.unavailable", err)
	}
}

func TestHubOriginAllowed(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	if !hub.OriginAllowed("https://proctor.example", false) {
		t.Fatal("matching origin rejected")
	}
	if hub.OriginAllowed("https://evil.example", false) {
		t.Fatal("cross-origin allowed")
	}
	if hub.OriginAllowed("", false) {
		t.Fatal("missing origin allowed for cookie clients")
	}
	if !hub.OriginAllowed("", true) {
		t.Fatal("missing origin rejected for bearer clients")
	}
}

func TestHubStartRespectsCanceledContext(t *testing.T) {
	t.Parallel()

	hub := newTestHub(t)
	t.Cleanup(func() { _ = hub.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hub.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start(canceled) error = %v, want context.Canceled", err)
	}
	if hub.Started() {
		t.Fatal("Start(canceled) started the hub")
	}
}
