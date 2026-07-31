// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestWebSocketIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithServerOptions(app.WithStore(persistence)),
	)
	if err := helper.Platform.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	password := "correct horse battery staple"
	bootstrap := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
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
		helper.Server.Handler(),
		installation.Administrator.Username,
		password,
		model.SessionClientCLI,
		"websocket-test",
	)

	server := httptest.NewServer(helper.Server.Handler())
	t.Cleanup(server.Close)
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/websocket"
	headers := http.Header{"Authorization": []string{"Bearer " + login.Tokens.AccessToken}}
	invalidOriginHeaders := headers.Clone()
	invalidOriginHeaders.Set("Origin", "https://attacker.example")
	_, response, err := websocket.DefaultDialer.Dial(socketURL, invalidOriginHeaders)
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin WebSocket response = %#v, %v", response, err)
	}

	resyncURL := socketURL + "?connection_id=" + model.NewId() + "&sequence_number=0"
	resyncConnection, response, err := websocket.DefaultDialer.Dial(resyncURL, headers)
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

	connection, response, err := websocket.DefaultDialer.Dial(socketURL, headers)
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
	var helloData model.WebSocketHello
	if err := json.Unmarshal(hello.Data, &helloData); err != nil {
		t.Fatal(err)
	}
	if !model.IsValidId(helloData.ConnectionId) ||
		helloData.NodeId != helper.Cluster.NodeID() ||
		helloData.Resumed {
		t.Fatalf("hello data = %#v", helloData)
	}
	writeWebSocketRequest(t, connection, 1, "ping", nil)
	ping := readWebSocketResponse(t, connection)
	if ping.Status != "ok" || ping.Sequence != 1 || string(ping.Data) != `{"pong":true}` {
		t.Fatalf("ping response = %#v", ping)
	}

	subscription := model.WebSocketSubscription{
		Action: model.ActionInstitutionManage,
		Resource: model.Resource{
			Type: model.ResourceInstitution,
			Id:   installation.Institution.Id,
		},
	}
	writeWebSocketRequest(t, connection, 2, "subscribe", subscription)
	responseMessage := readWebSocketResponse(t, connection)
	if responseMessage.Status != "ok" || responseMessage.Sequence != 2 {
		t.Fatalf("subscribe response = %#v", responseMessage)
	}

	eventData := json.RawMessage(`{"version":1}`)
	if appErr := helper.App.PublishWebSocketEvent(
		context.Background(),
		&model.WebSocketEvent{
			Event:    "institution.updated",
			Action:   subscription.Action,
			Resource: subscription.Resource,
			Data:     eventData,
		},
		model.ClusterSendBestEffort,
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
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "reconnect"),
	)
	_, _, _ = connection.ReadMessage()
	_ = connection.Close()
	resumeURL := socketURL + "?connection_id=" +
		url.QueryEscape(helloData.ConnectionId) + "&sequence_number=1"
	var resumed *websocket.Conn
	for attempt := 0; attempt < 20; attempt++ {
		candidate, candidateResponse, dialErr := websocket.DefaultDialer.Dial(resumeURL, headers)
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
	var resumedData model.WebSocketHello
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
	if appErr := helper.App.RevokeSession(
		context.Background(),
		*principal,
		login.Session.Id,
	); appErr != nil {
		t.Fatal(appErr)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *websocket.CloseError
	if !errors.As(err, &closeError) ||
		closeError.Code != app.WebSocketCloseSessionRevoked {
		t.Fatalf("revoked WebSocket close = %v", err)
	}

	unauthenticatedURL, err := url.Parse(socketURL)
	if err != nil {
		t.Fatal(err)
	}
	_, response, err = websocket.DefaultDialer.Dial(unauthenticatedURL.String(), nil)
	if err == nil {
		t.Fatal("unauthenticated WebSocket connection succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WebSocket response = %#v, %v", response, err)
	}
}

func TestWebSocketTwoNodeConformance(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	redisAddress := os.Getenv("PROCTOR_TEST_REDIS_ADDRESS")
	if dataSource == "" || redisAddress == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL and PROCTOR_TEST_REDIS_ADDRESS are required")
	}
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
	namespace := "proctor_test_" + model.NewId()
	nodeConfig := func(nodeID string) testlib.Option {
		return testlib.WithConfig(func(cfg *config.Config) {
			cfg.Cluster.Backend = "redis"
			cfg.Cluster.NodeID = nodeID
			cfg.Cluster.Redis.Addresses = []string{redisAddress}
			cfg.Cluster.Redis.Namespace = namespace
			cfg.Cache.Backend = "redis"
			cfg.Cache.Redis.Addresses = []string{redisAddress}
			cfg.VFS.Backend = "s3"
			cfg.VFS.S3.Endpoint = "127.0.0.1:19000"
			cfg.VFS.S3.Bucket = "proctor-test"
		})
	}
	nodeA := testlib.Setup(
		t,
		nodeConfig("node-a"),
		testlib.WithServerOptions(app.WithStore(persistenceA)),
	)
	nodeB := testlib.Setup(
		t,
		nodeConfig("node-b"),
		testlib.WithServerOptions(app.WithStore(persistenceB)),
	)
	if err := nodeA.Platform.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Platform.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	password := "correct horse battery staple"
	bootstrap := performJSONRequest(
		nodeA.Server.Handler(),
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
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
		nodeA.Server.Handler(),
		installation.Administrator.Username,
		password,
		model.SessionClientCLI,
		"two-node-websocket",
	)

	serverB := httptest.NewServer(nodeB.Server.Handler())
	t.Cleanup(serverB.Close)
	socketURL := "ws" + strings.TrimPrefix(serverB.URL, "http") + "/api/v1/websocket"
	headers := http.Header{"Authorization": []string{"Bearer " + login.Tokens.AccessToken}}
	connection, response, err := websocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("node B WebSocket dial = %v, status = %d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = readWebSocketEvent(t, connection)

	subscription := model.WebSocketSubscription{
		Action: model.ActionInstitutionManage,
		Resource: model.Resource{
			Type: model.ResourceInstitution,
			Id:   installation.Institution.Id,
		},
	}
	writeWebSocketRequest(t, connection, 1, "subscribe", subscription)
	if response := readWebSocketResponse(t, connection); response.Status != "ok" {
		t.Fatalf("node B subscription = %#v", response)
	}

	if appErr := nodeA.App.PublishWebSocketEvent(
		context.Background(),
		&model.WebSocketEvent{
			Event:    "institution.cluster_updated",
			Action:   subscription.Action,
			Resource: subscription.Resource,
			Data:     json.RawMessage(`{"source":"node-a"}`),
		},
		model.ClusterSendReliable,
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
	role, appErr := nodeA.App.CreateRole(
		context.Background(),
		*principal,
		model.RequestMetadata{RequestId: "two-node-role-create"},
		&model.Role{
			Name: "cluster_observer", DisplayName: "Cluster Observer",
			Permissions: []string{string(model.ActionUserView)},
		},
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
	updatedDisplayName := "Updated Cluster Observer"
	if _, appErr := nodeA.App.PatchRole(
		context.Background(),
		*principal,
		model.RequestMetadata{RequestId: "two-node-role-patch"},
		role.Id,
		&model.RolePatch{DisplayName: &updatedDisplayName},
	); appErr != nil {
		t.Fatal(appErr)
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	var closeError *websocket.CloseError
	if !errors.As(err, &closeError) ||
		closeError.Code != app.WebSocketCloseAuthorizationChanged {
		t.Fatalf("node B authorization-change close = %v", err)
	}

	connection, response, err = websocket.DefaultDialer.Dial(socketURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("node B reconnect = %v, status = %d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = readWebSocketEvent(t, connection)
	if appErr := nodeA.App.RevokeSession(
		context.Background(),
		*principal,
		login.Session.Id,
	); appErr != nil {
		t.Fatal(appErr)
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = connection.ReadMessage()
	closeError = nil
	if !errors.As(err, &closeError) ||
		closeError.Code != app.WebSocketCloseSessionRevoked {
		t.Fatalf("node B revocation close = %v", err)
	}
}

func writeWebSocketRequest(
	t *testing.T,
	connection *websocket.Conn,
	sequence int64,
	action string,
	data any,
) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteJSON(&model.WebSocketRequest{
		Sequence: sequence, Action: action, Data: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func readWebSocketEvent(
	t *testing.T,
	connection *websocket.Conn,
) *model.WebSocketEvent {
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
	var event model.WebSocketEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return &event
}

func readWebSocketResponse(
	t *testing.T,
	connection *websocket.Conn,
) *model.WebSocketResponse {
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
	var response model.WebSocketResponse
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
