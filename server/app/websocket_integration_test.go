//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	"github.com/sudosylabs/proctor/server/app"
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
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestWebSocketIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var installation model.InstallationBootstrapResult
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &installation); err != nil {
		t.Fatal(err)
	}
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
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin WebSocket response = %#v, %v", response, err)
	}

	resyncURL := socketURL + "?connection_id=" + model.NewId() + "&sequence_number=0"
	resyncConnection, response, err := gwebsocket.DefaultDialer.Dial(resyncURL, headers)
	if err != nil {
		t.Fatalf("WebSocket resync dial = %v, response = %#v", err, response)
	}
	resyncHello := readWebSocketEvent(t, resyncConnection)
	if resyncHello.Event != "hello" || resyncHello.Sequence != 1 {
		t.Fatalf("resync hello = %#v", resyncHello)
	}
	resyncRequired := readWebSocketEvent(t, resyncConnection)
	if resyncRequired.Event != "resync_required" ||
		resyncRequired.Sequence != 2 {
		t.Fatalf("resync event = %#v", resyncRequired)
	}
	_ = resyncConnection.Close()

	connection, response, err := gwebsocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("WebSocket dial = %v, status = %d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	hello := readWebSocketEvent(t, connection)
	if hello.Event != "hello" ||
		hello.Sequence != 1 ||
		!model.IsValidId(hello.Id) {
		t.Fatalf("hello event = %#v", hello)
	}
	var helloData websocket.Hello
	if err := json.Unmarshal(hello.Data, &helloData); err != nil {
		t.Fatal(err)
	}
	if !model.IsValidId(helloData.ConnectionId) ||
		helloData.NodeId != helper.ConfigStore.Get().Cluster.NodeID ||
		helloData.Resumed {
		t.Fatalf("hello data = %#v", helloData)
	}
	writeWebSocketRequest(t, connection, 1, "ping", nil)
	ping := readWebSocketResponse(t, connection)
	if ping.Status != "ok" || ping.Sequence != 1 || string(ping.Data) != `{"pong":true}` {
		t.Fatalf("ping response = %#v", ping)
	}

	subscription := websocket.Subscription{
		Action: model.ActionInstitutionManage,
		Resource: websocket.Resource{
			Type: model.ResourceInstitution,
			ID:   installation.Institution.ID.String(),
		},
	}
	writeWebSocketRequest(t, connection, 2, "subscribe", subscription)
	responseMessage := readWebSocketResponse(t, connection)
	if responseMessage.Status != "ok" || responseMessage.Sequence != 2 {
		t.Fatalf("subscribe response = %#v", responseMessage)
	}

	eventData := json.RawMessage(`{"version":1}`)
	if appErr := helper.App.PublishRealtimeEvent(
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
	); appErr != nil {
		t.Fatal(appErr)
	}
	event := readWebSocketEvent(t, connection)
	if event.Event != "institution.updated" ||
		event.Sequence != 2 ||
		string(event.Data) != string(eventData) {
		t.Fatalf("published event = %#v", event)
	}
	_ = connection.WriteMessage(
		gwebsocket.CloseMessage,
		gwebsocket.FormatCloseMessage(gwebsocket.CloseNormalClosure, "reconnect"),
	)
	_, _, _ = connection.ReadMessage()
	_ = connection.Close()
	resumeURL := socketURL + "?connection_id=" +
		url.QueryEscape(helloData.ConnectionId) + "&sequence_number=1"
	var resumed *gwebsocket.Conn
	for attempt := 0; attempt < 20; attempt++ {
		candidate, candidateResponse, dialErr := gwebsocket.DefaultDialer.Dial(resumeURL, headers)
		if dialErr != nil {
			t.Fatalf("WebSocket resume dial = %v, response = %#v", dialErr, candidateResponse)
		}
		first := readWebSocketEvent(t, candidate)
		if first.Event == "institution.updated" && first.Sequence == 2 {
			resumed = candidate
			break
		}
		_ = candidate.Close()
		time.Sleep(10 * time.Millisecond)
	}
	if resumed == nil {
		t.Fatal("WebSocket replay state was not available")
	}
	connection = resumed
	resumedHello := readWebSocketEvent(t, connection)
	var resumedData websocket.Hello
	if err := json.Unmarshal(resumedHello.Data, &resumedData); err != nil {
		t.Fatal(err)
	}
	if resumedHello.Event != "hello" ||
		resumedHello.Sequence != 3 || !resumedData.Resumed ||
		resumedData.ConnectionId != helloData.ConnectionId {
		t.Fatalf("resumed hello = %#v, data = %#v", resumedHello, resumedData)
	}

	writeWebSocketRequest(t, connection, 3, "unsubscribe", subscription)
	if responseMessage = readWebSocketResponse(t, connection); responseMessage.Status != "ok" {
		t.Fatalf("unsubscribe response = %#v", responseMessage)
	}

	principal, appErr := helper.App.AuthenticateAccess(
		context.Background(),
		login.Tokens.AccessToken,
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if err := helper.App.RevokeSession(
		context.Background(),
		app.NewInvocation(*principal, model.RequestMetadata{}),
		app.RevokeSessionCommand{SessionID: login.Session.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *gwebsocket.CloseError
	if !errors.As(err, &closeError) ||
		closeError.Code != websocket.CloseSessionRevoked {
		t.Fatalf("revoked WebSocket close = %v", err)
	}

	unauthenticatedURL, err := url.Parse(socketURL)
	if err != nil {
		t.Fatal(err)
	}
	_, response, err = gwebsocket.DefaultDialer.Dial(unauthenticatedURL.String(), nil)
	if err == nil {
		t.Fatal("unauthenticated WebSocket connection succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WebSocket response = %#v, %v", response, err)
	}
}

func TestWebSocketTwoNodeConformance(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is required")
	}
	// Optional Redis remains an independent cache backend; clustering itself
	// uses Memberlist and does not require Redis.
	redisAddress := os.Getenv("PROCTOR_TEST_REDIS_ADDRESS")
	persistenceA := openAuthenticationStore(t, dataSource)
	database := config.Default().Database
	database.DataSource = dataSource
	persistenceB, err := sqlstore.New(
		context.Background(),
		sqlstore.SettingsFromConfig(database),
	)
	if err != nil {
		t.Fatal(err)
	}
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
			if redisAddress != "" {
				cfg.Cache.Backend = "redis"
				cfg.Cache.Redis.Addresses = []string{redisAddress}
			} else {
				cfg.Cache.Backend = "memory"
			}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var installation model.InstallationBootstrapResult
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &installation); err != nil {
		t.Fatal(err)
	}
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
		if response != nil {
			t.Fatalf("node B WebSocket dial = %v, status = %d", err, response.StatusCode)
		}
		t.Fatal(err)
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
	if response := readWebSocketResponse(t, connection); response.Status != "ok" {
		t.Fatalf("node B subscription = %#v", response)
	}

	if appErr := nodeA.App.PublishRealtimeEvent(
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
	); appErr != nil {
		t.Fatal(appErr)
	}
	event := readWebSocketEvent(t, connection)
	if event.Event != "institution.cluster_updated" ||
		string(event.Data) != `{"source":"node-a"}` {
		t.Fatalf("node B cluster event = %#v", event)
	}

	principal, appErr := nodeA.App.AuthenticateAccess(
		context.Background(),
		login.Tokens.AccessToken,
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
	role, err := nodeA.App.CreateRole(
		context.Background(),
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "two-node-role-create"}),
		app.CreateRoleCommand{
			Name: "cluster_observer", DisplayName: "Cluster Observer",
			Permissions: []string{string(model.ActionUserView)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	updatedDisplayName := "Updated Cluster Observer"
	if _, err := nodeA.App.UpdateRole(
		context.Background(),
		app.NewInvocation(*principal, model.RequestMetadata{RequestID: "two-node-role-patch"}),
		app.UpdateRoleCommand{ID: role.ID.String(), DisplayName: &updatedDisplayName},
	); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *gwebsocket.CloseError
	if !errors.As(err, &closeError) ||
		closeError.Code != websocket.CloseAuthorizationChanged {
		t.Fatalf("node B authorization-change close = %v", err)
	}

	connection, response, err = gwebsocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("node B reconnect = %v, status = %d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = readWebSocketEvent(t, connection)
	if err := nodeA.App.RevokeSession(
		context.Background(),
		app.NewInvocation(*principal, model.RequestMetadata{}),
		app.RevokeSessionCommand{SessionID: login.Session.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	closeError = nil
	if !errors.As(err, &closeError) ||
		closeError.Code != websocket.CloseSessionRevoked {
		t.Fatalf("node B revocation close = %v", err)
	}
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
			t.Fatalf("start integration server before readiness: %v", err)
		case <-deadline.C:
			t.Fatal("integration server did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("stop integration server: %v", err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteJSON(&websocket.Request{
		Sequence: sequence, Action: action, Data: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func readWebSocketEvent(
	t *testing.T,
	connection *gwebsocket.Conn,
) *websocket.Event {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	assertWebSocketJSONKeys(
		t,
		payload,
		[]string{"id", "event", "sequence"},
	)
	var event websocket.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return &event
}

func readWebSocketResponse(
	t *testing.T,
	connection *gwebsocket.Conn,
) *websocket.Response {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	assertWebSocketJSONKeys(
		t,
		payload,
		[]string{"status", "sequence"},
	)
	var response websocket.Response
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func assertWebSocketJSONKeys(
	t *testing.T,
	payload []byte,
	required []string,
) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode WebSocket envelope: %v", err)
	}
	for _, key := range required {
		if _, exists := envelope[key]; !exists {
			t.Fatalf("WebSocket envelope %s is missing %q", payload, key)
		}
	}
}
