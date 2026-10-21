// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestTransferAllowanceUsesMinimumThroughput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size int64
		want time.Duration
	}{
		{0, 0},
		{1, time.Second},
		{minimumTransferBytesPerSecond, time.Second},
		{minimumTransferBytesPerSecond + 1, 2 * time.Second},
		{100 << 20, 1600 * time.Second},
	}
	for _, test := range tests {
		if got := transferAllowance(test.size); got != test.want {
			t.Fatalf("transferAllowance(%d) = %s, want %s", test.size, got, test.want)
		}
	}
}

// TestTransferRoutesOutliveServerTimeouts runs a real HTTP server whose
// connection timeouts are shorter than a declared transfer. Ordinary routes
// keep those timeouts; upload and download routes must finish.
func TestTransferRoutesOutliveServerTimeouts(t *testing.T) {
	t.Parallel()

	const serverTimeout = 300 * time.Millisecond
	const uploadLimit = minimumTransferBytesPerSecond
	downloadBody := bytes.Repeat([]byte("a"), 4096)

	authenticator := classRouteAuthenticator{principal: model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}}
	upload := func(request operationRequest) (protocolResult, error) {
		body, err := io.ReadAll(request.request.Body)
		return streamingUploadProtocolResult(http.StatusCreated, map[string]any{"read": err == nil, "bytes": len(body)}), nil
	}
	download := func(operationRequest) (protocolResult, error) {
		// The operation outlasts the server-wide write timeout before the
		// response body starts.
		time.Sleep(serverTimeout + 100*time.Millisecond)
		return binaryDownloadProtocolResult(io.NopCloser(bytes.NewReader(downloadBody)), int64(len(downloadBody))), nil
	}
	resources := []resource{newResource(
		"transfers",
		idempotentProtocolRoute(
			IdempotencyNone, uploadLimit, "slow-upload", RouteProtocolStreamingUpload,
			AuthPrincipalRequired, http.MethodPost, apiPath(literal("upload")), nil, upload,
		),
		protocolRoute(
			"slow-download", RouteProtocolBinaryDownload,
			AuthPrincipalRequired, http.MethodGet, apiPath(literal("download")), nil, download,
		),
	)}
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := &API{
		authenticator: authenticator, logger: discardLogger{}, localizer: newTestLocalizer(t), cookies: cookies,
		recentAuthenticationTTL: time.Minute,
		transferDeadlines:       newTransferDeadlines(serverTimeout, serverTimeout),
	}
	if err := httpAPI.buildRoutingKernel(model.APIURLSuffix, 1024, func() error {
		return httpAPI.collectResources(model.APIURLSuffix, resources...)
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(httpAPI)
	server.Config.ReadTimeout = serverTimeout
	server.Config.WriteTimeout = serverTimeout
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 10 * time.Second

	t.Run("upload", func(t *testing.T) {
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			_, _ = writer.Write([]byte("first-"))
			time.Sleep(serverTimeout + 200*time.Millisecond)
			_, _ = writer.Write([]byte("second"))
			_ = writer.Close()
		}()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/v1/upload", reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("slow upload failed: %v", err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"read":true`) || !strings.Contains(string(body), `"bytes":12`) {
			t.Fatalf("slow upload = %d %s", response.StatusCode, body)
		}
	})

	t.Run("download", func(t *testing.T) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/download", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("slow download failed: %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, downloadBody) {
			t.Fatalf("slow download = %d, %d bytes, %v", response.StatusCode, len(body), err)
		}
	})
}

type discardLogger struct{}

func (discardLogger) InfoContext(context.Context, string, ...LogField)  {}
func (discardLogger) ErrorContext(context.Context, string, ...LogField) {}
