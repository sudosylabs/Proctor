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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	gwebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
	"github.com/sudosylabs/proctor/server/websocket"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestWebSocketIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
		}),
	)
	startIntegrationServer(t, helper)

	password := "correct horse battery staple"
	bootstrap := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
			"bootstrap_secret": testlib.BootstrapSecret,
			"institution": map[string]any{
				"name": "northbridge", "display_name": "Northbridge University",
			},
			"administrator": map[string]any{
				"username": "socket-owner", "email": "socket-owner@example.edu",
				"display_name": "Socket Owner",
			},
			"password": password,
		},
		"",
	)
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	var installation model.InstallationBootstrapResult
	require.NoError(t, json.Unmarshal(bootstrap.Body.Bytes(), &installation))
	login := loginIntegrationUser(
		t,
		helper.Handler(),
		installation.Administrator.Username,
		password,
		model.SessionClientCLI,
		"websocket-test",
	)

	server := httptest.NewServer(helper.Handler())
	t.Cleanup(server.Close)
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/websocket"
	headers := http.Header{"Authorization": []string{"Bearer " + login.Tokens.AccessToken}}
	invalidOriginHeaders := headers.Clone()
	invalidOriginHeaders.Set("Origin", "https://attacker.example")
	_, response, err := gwebsocket.DefaultDialer.Dial(socketURL, invalidOriginHeaders)
	require.Error(t, err, "cross-origin WebSocket response")
	require.NotNil(t, response, "cross-origin WebSocket response")
	require.Equal(t, http.StatusForbidden, response.StatusCode, "cross-origin WebSocket response")

	resyncURL := socketURL + "?connection_id=" + model.NewID() + "&sequence_number=0"
	resyncConnection, response, err := gwebsocket.DefaultDialer.Dial(resyncURL, headers)
	require.NoError(t, err, "WebSocket resync dial = %v, response = %#v", err, response)
	resyncHello := readWebSocketEvent(t, resyncConnection)
	require.Equal(t, "hello", resyncHello.Event)
	require.Equal(t, int64(1), resyncHello.Sequence)
	resyncRequired := readWebSocketEvent(t, resyncConnection)
	require.Equal(t, "resync_required", resyncRequired.Event)
	require.Equal(t, int64(2), resyncRequired.Sequence)
	_ = resyncConnection.Close()

	connection, response, err := gwebsocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		require.Nil(t, response, "WebSocket dial")
		require.NoError(t, err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	hello := readWebSocketEvent(t, connection)
	require.Equal(t, "hello", hello.Event)
	require.Equal(t, int64(1), hello.Sequence)
	require.True(t, model.IsValidOpaqueID(hello.ID))
	var helloData websocket.Hello
	require.NoError(t, json.Unmarshal(hello.Data, &helloData))
	require.True(t, model.IsValidOpaqueID(helloData.ConnectionID))
	require.Equal(t, helper.ConfigStore.Get().Cluster.NodeID, helloData.NodeID)
	require.False(t, helloData.Resumed)
	writeWebSocketRequest(t, connection, 1, "ping", nil)
	ping := readWebSocketResponse(t, connection)
	require.Equal(t, "ok", ping.Status)
	require.Equal(t, int64(1), ping.Sequence)
	require.Equal(t, `{"pong":true}`, string(ping.Data))

	subscription := websocket.Subscription{
		Action: model.ActionInstitutionManage,
		Resource: websocket.Resource{
			Type: model.ResourceInstitution,
			ID:   installation.Institution.ID.String(),
		},
	}
	writeWebSocketRequest(t, connection, 2, "subscribe", subscription)
	responseMessage := readWebSocketResponse(t, connection)
	require.Equal(t, "ok", responseMessage.Status)
	require.Equal(t, int64(2), responseMessage.Sequence)

	eventData := json.RawMessage(`{"version":1}`)
	require.NoError(t, helper.App.PublishRealtimeEvent(
		context.Background(),
		apprealtime.RealtimeEvent{
			Name:   "institution.updated",
			Action: subscription.Action,
			Resource: model.Resource{
				Type: subscription.Resource.Type,
				ID:   subscription.Resource.ID,
			},
			Data: eventData,
		},
	))
	event := readWebSocketEvent(t, connection)
	require.Equal(t, "institution.updated", event.Event)
	require.Equal(t, int64(2), event.Sequence)
	require.Equal(t, string(eventData), string(event.Data))
	_ = connection.WriteMessage(
		gwebsocket.CloseMessage,
		gwebsocket.FormatCloseMessage(gwebsocket.CloseNormalClosure, "reconnect"),
	)
	_, _, _ = connection.ReadMessage()
	_ = connection.Close()
	resumeURL := socketURL + "?connection_id=" +
		url.QueryEscape(helloData.ConnectionID) + "&sequence_number=1"
	var resumed *gwebsocket.Conn
	for attempt := 0; attempt < 20; attempt++ {
		candidate, candidateResponse, dialErr := gwebsocket.DefaultDialer.Dial(resumeURL, headers)
		require.NoError(t, dialErr, "WebSocket resume dial = %v, response = %#v", dialErr, candidateResponse)
		first := readWebSocketEvent(t, candidate)
		if first.Event == "institution.updated" && first.Sequence == 2 {
			resumed = candidate
			break
		}
		_ = candidate.Close()
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, resumed, "WebSocket replay state was not available")
	connection = resumed
	resumedHello := readWebSocketEvent(t, connection)
	var resumedData websocket.Hello
	require.NoError(t, json.Unmarshal(resumedHello.Data, &resumedData))
	require.Equal(t, "hello", resumedHello.Event, "resumed hello = %#v, data = %#v", resumedHello, resumedData)
	require.Equal(t, int64(3), resumedHello.Sequence, "resumed hello = %#v, data = %#v", resumedHello, resumedData)
	require.True(t, resumedData.Resumed, "resumed hello = %#v, data = %#v", resumedHello, resumedData)
	require.Equal(t, helloData.ConnectionID, resumedData.ConnectionID, "resumed hello = %#v, data = %#v", resumedHello, resumedData)

	writeWebSocketRequest(t, connection, 3, "unsubscribe", subscription)
	responseMessage = readWebSocketResponse(t, connection)
	require.Equal(t, "ok", responseMessage.Status)

	principal, appErr := helper.App.AuthenticateAccess(
		context.Background(),
		login.Tokens.AccessToken,
	)
	require.NoError(t, appErr)
	require.NoError(t, helper.App.RevokeSession(
		context.Background(),
		invocation.New(*principal, model.RequestMetadata{}),
		app.RevokeSessionCommand{SessionID: login.Session.ID.String()},
	))
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *gwebsocket.CloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, websocket.CloseSessionRevoked, closeError.Code)

	unauthenticatedURL, err := url.Parse(socketURL)
	require.NoError(t, err)
	_, response, err = gwebsocket.DefaultDialer.Dial(unauthenticatedURL.String(), nil)
	require.Error(t, err, "unauthenticated WebSocket connection succeeded")
	require.NotNil(t, response, "unauthenticated WebSocket response = %#v, %v", response, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode, "unauthenticated WebSocket response = %#v, %v", response, err)
}

func TestWebSocketTwoNodeConformance(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required")
	redisAddress := os.Getenv("PROCTOR_TEST_REDIS_ADDRESS")
	require.NotEmpty(t, redisAddress, "PROCTOR_TEST_REDIS_ADDRESS is required for clustered cache coherence")
	persistenceA := openAuthenticationStore(t, dataSource)
	database := config.Default().Database
	database.DataSource = dataSource
	persistenceB, err := sqlstore.New(
		context.Background(),
		sqlstore.SettingsFromConfig(database),
	)
	require.NoError(t, err)
	clusterKeyMaterial := make([]byte, 32)
	clusterKeyMaterial[0] = 1
	clusterKey := base64.StdEncoding.EncodeToString(clusterKeyMaterial)
	// Distinct loopback ports so both nodes can bind Memberlist in one process.
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)
	nodeConfig := func(nodeID string, port int) testlib.Option {
		return testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Cluster.Backend = "memberlist"
			cfg.Cluster.NodeID = nodeID
			address := fmt.Sprintf("127.0.0.1:%d", port)
			cfg.Cluster.Memberlist.BindAddress = address
			cfg.Cluster.Memberlist.AdvertiseAddress = address
			cfg.Cluster.Memberlist.EncryptionKey = clusterKey
			cfg.Cluster.Memberlist.SeedAddresses = []string{
				fmt.Sprintf("127.0.0.1:%d", portA),
				fmt.Sprintf("127.0.0.1:%d", portB),
			}
			cfg.Cluster.Memberlist.DiscoveryTTL.Duration = 5 * time.Second
			cfg.Cluster.Memberlist.DiscoveryHeartbeat.Duration = time.Second
			cfg.Cache.Backend = "redis"
			cfg.Cache.Redis.Addresses = []string{redisAddress}
			cfg.VFS.Backend = "s3"
			cfg.VFS.S3.Endpoint = "127.0.0.1:19000"
			cfg.VFS.S3.Bucket = "proctor-test"
		})
	}
	nodeA := testlib.Setup(
		t,
		nodeConfig("node-a", portA),
		testlib.WithStore(persistenceA),
	)
	nodeB := testlib.Setup(
		t,
		nodeConfig("node-b", portB),
		testlib.WithStore(persistenceB),
	)
	startIntegrationServer(t, nodeA)
	startIntegrationServer(t, nodeB)

	password := "correct horse battery staple"
	bootstrap := performJSONRequest(
		nodeA.Handler(),
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
			"bootstrap_secret": testlib.BootstrapSecret,
			"institution": map[string]any{
				"name": "northbridge", "display_name": "Northbridge University",
			},
			"administrator": map[string]any{
				"username": "cluster-owner", "email": "cluster-owner@example.edu",
				"display_name": "Cluster Owner",
			},
			"password": password,
		},
		"",
	)
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	var installation model.InstallationBootstrapResult
	require.NoError(t, json.Unmarshal(bootstrap.Body.Bytes(), &installation))
	login := loginIntegrationUser(
		t,
		nodeA.Handler(),
		installation.Administrator.Username,
		password,
		model.SessionClientCLI,
		"two-node-websocket",
	)

	serverB := httptest.NewServer(nodeB.Handler())
	t.Cleanup(serverB.Close)
	socketURL := "ws" + strings.TrimPrefix(serverB.URL, "http") + "/api/v1/websocket"
	headers := http.Header{"Authorization": []string{"Bearer " + login.Tokens.AccessToken}}
	connection, response, err := gwebsocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		require.Nil(t, response, "node B WebSocket dial")
		require.NoError(t, err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = readWebSocketEvent(t, connection)

	subscription := websocket.Subscription{
		Action: model.ActionInstitutionManage,
		Resource: websocket.Resource{
			Type: model.ResourceInstitution,
			ID:   installation.Institution.ID.String(),
		},
	}
	writeWebSocketRequest(t, connection, 1, "subscribe", subscription)
	require.Equal(t, "ok", readWebSocketResponse(t, connection).Status)

	require.NoError(t, nodeA.App.PublishRealtimeEvent(
		context.Background(),
		apprealtime.RealtimeEvent{
			Name:   "institution.cluster_updated",
			Action: subscription.Action,
			Resource: model.Resource{
				Type: subscription.Resource.Type,
				ID:   subscription.Resource.ID,
			},
			Data: json.RawMessage(`{"source":"node-a"}`),
		},
	))
	event := readWebSocketEvent(t, connection)
	require.Equal(t, "institution.cluster_updated", event.Event)
	require.Equal(t, `{"source":"node-a"}`, string(event.Data))

	principal, appErr := nodeA.App.AuthenticateAccess(
		context.Background(),
		login.Tokens.AccessToken,
	)
	require.NoError(t, appErr)
	role, err := nodeA.App.CreateRole(
		context.Background(),
		invocation.New(*principal, model.RequestMetadata{RequestID: "two-node-role-create"}),
		app.CreateRoleCommand{
			Name: "cluster_observer", DisplayName: "Cluster Observer",
			Permissions: []string{string(model.ActionUserView)},
		},
	)
	require.NoError(t, err)
	updatedDisplayName := "Updated Cluster Observer"
	_, err = nodeA.App.UpdateRole(
		context.Background(),
		invocation.New(*principal, model.RequestMetadata{RequestID: "two-node-role-patch"}),
		app.UpdateRoleCommand{ID: role.ID, DisplayName: &updatedDisplayName},
	)
	require.NoError(t, err)
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *gwebsocket.CloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, websocket.CloseAuthorizationChanged, closeError.Code)

	connection, response, err = gwebsocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		require.Nil(t, response, "node B reconnect")
		require.NoError(t, err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = readWebSocketEvent(t, connection)
	require.NoError(t, nodeA.App.RevokeSession(
		context.Background(),
		invocation.New(*principal, model.RequestMetadata{}),
		app.RevokeSessionCommand{SessionID: login.Session.ID.String()},
	))
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	closeError = nil
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, websocket.CloseSessionRevoked, closeError.Code)
}

func startIntegrationServer(t *testing.T, helper *testlib.Helper) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- helper.Server.Run(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !helper.Server.Ready() {
		select {
		case err := <-done:
			require.FailNow(t, fmt.Sprintf("start integration server before readiness: %v", err))
		case <-deadline.C:
			require.FailNow(t, "integration server did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, (<-done))
	})
}

func writeWebSocketRequest(
	t *testing.T,
	connection *gwebsocket.Conn,
	sequence int64,
	action string,
	data any,
) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, connection.WriteJSON(&websocket.Request{
		Sequence: sequence, Action: action, Data: raw,
	}))
}

func readWebSocketEvent(
	t *testing.T,
	connection *gwebsocket.Conn,
) *websocket.Event {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := connection.ReadMessage()
	require.NoError(t, err)
	assertWebSocketJSONKeys(
		t,
		payload,
		[]string{"id", "event", "sequence"},
	)
	var event websocket.Event
	require.NoError(t, json.Unmarshal(payload, &event))
	return &event
}

func readWebSocketResponse(
	t *testing.T,
	connection *gwebsocket.Conn,
) *websocket.Response {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := connection.ReadMessage()
	require.NoError(t, err)
	assertWebSocketJSONKeys(
		t,
		payload,
		[]string{"status", "sequence"},
	)
	var response websocket.Response
	require.NoError(t, json.Unmarshal(payload, &response))
	return &response
}

func assertWebSocketJSONKeys(
	t *testing.T,
	payload []byte,
	required []string,
) {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &envelope))
	for _, key := range required {
		_, exists := envelope[key]
		require.True(t, exists, "WebSocket envelope %s is missing %q", payload, key)
	}
}
