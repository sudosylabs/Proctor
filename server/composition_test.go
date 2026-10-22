// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/webui"
)

type compositionCloser struct {
	name   string
	events *[]string
	err    error
	count  int
}

func (c *compositionCloser) close() error {
	c.count++
	*c.events = append(*c.events, c.name)
	return c.err
}

type compositionPlatform struct{ closer *compositionCloser }

func (p compositionPlatform) Start(context.Context) error { return nil }
func (p compositionPlatform) Close() error                { return p.closer.close() }

type compositionTransport struct{ closer *compositionCloser }

func (t compositionTransport) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (t compositionTransport) Close() error                                 { return t.closer.close() }

type compositionWebSocket struct{ closer *compositionCloser }

func (w compositionWebSocket) Start(context.Context) error { return nil }
func (w compositionWebSocket) Close() error                { return w.closer.close() }
func (compositionWebSocket) Accept(http.ResponseWriter, *http.Request, model.Principal, model.RequestMetadata, string, int64, bool) error {
	return nil
}
func (compositionWebSocket) PublishLocal(context.Context, realtime.RealtimeEvent)  {}
func (compositionWebSocket) CloseSession(string, realtime.ConnectionCloseReason)   {}
func (compositionWebSocket) CloseUser(string, realtime.ConnectionCloseReason)      {}
func (compositionWebSocket) CloseAll(realtime.ConnectionCloseReason)               {}
func (compositionWebSocket) UnbindExamAttemptConnection(model.AttemptConnectionID) {}

type compositionFanout struct{}

func (compositionFanout) RegisterHandler(string, func(context.Context, []byte) error) error {
	return nil
}
func (compositionFanout) Broadcast(context.Context, string, []byte) error { return nil }

type compositionJobs struct{}

func (compositionJobs) Start(context.Context) error { return nil }
func (compositionJobs) Close() error                { return nil }

func TestConsumerConstructionFailuresUnwindExactlyOnce(t *testing.T) {
	t.Parallel()

	phases := []string{
		"file-content", "dependencies", "application", "realtime",
		"attach-realtime", "websocket", "attach-sink", "http", "webapp", "jobs",
	}
	for _, phase := range phases {
		phase := phase
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			primaryErr := errors.New(phase + " failed")
			platformErr := errors.New("platform cleanup failed")
			websocketErr := errors.New("websocket cleanup failed")
			transportErr := errors.New("transport cleanup failed")
			platformCloser := &compositionCloser{name: "platform", events: &events, err: platformErr}
			websocketCloser := &compositionCloser{name: "websocket", events: &events, err: websocketErr}
			transportCloser := &compositionCloser{name: "transport", events: &events, err: transportErr}

			constructors := consumerConstructors{
				fileContent: func(constructionCapabilities) (app.FileContent, error) { return nil, nil },
				dependencies: func(constructionCapabilities, app.FileContent) (app.Dependencies, error) {
					return app.Dependencies{}, nil
				},
				application:    func(app.Dependencies) (*app.App, error) { return nil, nil },
				realtime:       func(borrowedCluster) (realtime.ClusterFanout, error) { return compositionFanout{}, nil },
				attachRealtime: func(*app.App, realtime.ClusterFanout) error { return nil },
				websocket: func(*app.App, runtimeLogger, string, string) (composedWebSocket, error) {
					return compositionWebSocket{closer: websocketCloser}, nil
				},
				attachSink: func(*app.App, realtime.Sink) error { return nil },
				http: func(httpapi.Options) (runtimeTransport, http.Handler, error) {
					transport := compositionTransport{closer: transportCloser}
					return transport, transport, nil
				},
				webapp: func(webui.Options) (http.Handler, error) { return http.NotFoundHandler(), nil },
				jobs:   func(*app.App) runtimeComponent { return compositionJobs{} },
			}
			switch phase {
			case "file-content":
				constructors.fileContent = func(constructionCapabilities) (app.FileContent, error) { return nil, primaryErr }
			case "dependencies":
				constructors.dependencies = func(constructionCapabilities, app.FileContent) (app.Dependencies, error) {
					return app.Dependencies{}, primaryErr
				}
			case "application":
				constructors.application = func(app.Dependencies) (*app.App, error) { return nil, primaryErr }
			case "realtime":
				constructors.realtime = func(borrowedCluster) (realtime.ClusterFanout, error) { return nil, primaryErr }
			case "attach-realtime":
				constructors.attachRealtime = func(*app.App, realtime.ClusterFanout) error { return primaryErr }
			case "websocket":
				constructors.websocket = func(*app.App, runtimeLogger, string, string) (composedWebSocket, error) { return nil, primaryErr }
			case "attach-sink":
				constructors.attachSink = func(*app.App, realtime.Sink) error { return primaryErr }
			case "http":
				constructors.http = func(httpapi.Options) (runtimeTransport, http.Handler, error) { return nil, nil, primaryErr }
			case "webapp":
				constructors.webapp = func(webui.Options) (http.Handler, error) { return nil, primaryErr }
			case "jobs":
				constructors.jobs = func(*app.App) runtimeComponent { return nil }
				primaryErr = errDurableJobRuntimeUnavailable
			}

			result, err := composeConsumers(
				context.Background(), compositionPlatform{closer: platformCloser}, config.Default(),
				constructionCapabilities{}, compositionInput{}, constructors,
			)
			require.Nil(t, result, "composeConsumers() result")
			require.ErrorIs(t, err, primaryErr, "composeConsumers() error")
			wantEvents := []string{"platform"}
			if phase == "attach-sink" || phase == "http" || phase == "jobs" {
				wantEvents = []string{"websocket", "platform"}
			}
			if phase == "webapp" || phase == "jobs" {
				wantEvents = []string{"transport", "websocket", "platform"}
			}
			require.Equal(t, wantEvents, events, "cleanup order")
			for name, closer := range map[string]*compositionCloser{
				"platform": platformCloser, "websocket": websocketCloser, "transport": transportCloser,
			} {
				want := 0
				if slices.Contains(wantEvents, name) {
					want = 1
				}
				require.Equal(t, want, closer.count, "%s close count", name)
				if want == 1 {
					require.ErrorIs(t, err, closer.err, "error %v does not preserve %s cleanup error", err, name)
				}
			}
		})
	}
}

func TestRealtimeHandlerRegistrationFailureUnwindsComposition(t *testing.T) {
	t.Parallel()

	events := []string{}
	platformCloser := &compositionCloser{name: "platform", events: &events}
	registrationErr := errors.New("register realtime handler failed")
	transport := &recordingBorrowedCluster{registrationErr: map[cluster.Event]error{
		cluster.Event("authentication.session_revoked"): registrationErr,
	}}
	constructors := consumerConstructors{
		fileContent: func(constructionCapabilities) (app.FileContent, error) { return nil, nil },
		dependencies: func(constructionCapabilities, app.FileContent) (app.Dependencies, error) {
			return app.Dependencies{}, nil
		},
		application: func(app.Dependencies) (*app.App, error) { return nil, nil },
		realtime: func(cluster borrowedCluster) (realtime.ClusterFanout, error) {
			return newRealtimeClusterAdapter(cluster)
		},
		attachRealtime: func(_ *app.App, fanout realtime.ClusterFanout) error {
			service, err := realtime.New(realtime.Dependencies{AuthenticationInvalidator: noopRealtimeInvalidator{}, Diagnostics: silentClusterLogger{}})
			if err != nil {
				return err
			}
			return service.SetClusterFanout(fanout)
		},
	}

	result, err := composeConsumers(
		context.Background(), compositionPlatform{closer: platformCloser},
		config.Default(),
		constructionCapabilities{cluster: transport},
		compositionInput{},
		constructors,
	)
	require.Nil(t, result, "composeConsumers() result")
	require.ErrorIs(t, err, registrationErr, "composeConsumers() error")
	require.True(t, slices.Equal(events, []string{"platform"}), "cleanup events = %v, platform closes", events)
	require.Equal(t, 1, platformCloser.count, "cleanup events = %v, platform closes", events)
	wantRegistrations := []cluster.Event{
		cluster.Event("websocket.publish"),
		cluster.Event("exam_attempt.connection_unbound"),
		cluster.Event("authentication.session_revoked"),
	}
	require.True(t, slices.Equal(transport.registrations, wantRegistrations), "registrations = %v, want %v", transport.registrations, wantRegistrations)
}
