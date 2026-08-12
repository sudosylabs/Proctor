// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
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
func (compositionWebSocket) PublishLocal(context.Context, app.RealtimeEvent) {}
func (compositionWebSocket) CloseSession(string, app.ConnectionCloseReason)  {}
func (compositionWebSocket) CloseUser(string, app.ConnectionCloseReason)     {}
func (compositionWebSocket) CloseAll(app.ConnectionCloseReason)              {}

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
		"attach-realtime", "websocket", "attach-sink", "http", "jobs",
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
				realtime:       func(borrowedCluster) (app.RealtimeClusterFanout, error) { return compositionFanout{}, nil },
				attachRealtime: func(*app.App, app.RealtimeClusterFanout) error { return nil },
				websocket: func(*app.App, runtimeLogger, string, string) (composedWebSocket, error) {
					return compositionWebSocket{closer: websocketCloser}, nil
				},
				attachSink: func(*app.App, app.RealtimeSink) error { return nil },
				http: func(api.Options) (runtimeTransport, http.Handler, error) {
					return compositionTransport{closer: transportCloser}, nil, nil
				},
				jobs: func(*app.App) runtimeJobs { return compositionJobs{} },
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
				constructors.realtime = func(borrowedCluster) (app.RealtimeClusterFanout, error) { return nil, primaryErr }
			case "attach-realtime":
				constructors.attachRealtime = func(*app.App, app.RealtimeClusterFanout) error { return primaryErr }
			case "websocket":
				constructors.websocket = func(*app.App, runtimeLogger, string, string) (composedWebSocket, error) { return nil, primaryErr }
			case "attach-sink":
				constructors.attachSink = func(*app.App, app.RealtimeSink) error { return primaryErr }
			case "http":
				constructors.http = func(api.Options) (runtimeTransport, http.Handler, error) { return nil, nil, primaryErr }
			case "jobs":
				constructors.jobs = func(*app.App) runtimeJobs { return nil }
				primaryErr = errDurableJobRuntimeUnavailable
			}

			result, err := composeConsumers(
				compositionPlatform{closer: platformCloser}, config.Default(),
				constructionCapabilities{}, compositionInput{}, constructors,
			)
			if result != nil {
				t.Fatalf("composeConsumers() result = %#v, want nil", result)
			}
			if !errors.Is(err, primaryErr) {
				t.Fatalf("composeConsumers() error = %v, want primary %v", err, primaryErr)
			}
			wantEvents := []string{"platform"}
			if phase == "attach-sink" || phase == "http" || phase == "jobs" {
				wantEvents = []string{"websocket", "platform"}
			}
			if phase == "jobs" {
				wantEvents = []string{"transport", "websocket", "platform"}
			}
			if !slices.Equal(events, wantEvents) {
				t.Fatalf("cleanup order = %v, want %v", events, wantEvents)
			}
			for name, closer := range map[string]*compositionCloser{
				"platform": platformCloser, "websocket": websocketCloser, "transport": transportCloser,
			} {
				want := 0
				if slices.Contains(wantEvents, name) {
					want = 1
				}
				if closer.count != want {
					t.Fatalf("%s close count = %d, want %d", name, closer.count, want)
				}
				if want == 1 && !errors.Is(err, closer.err) {
					t.Fatalf("error %v does not preserve %s cleanup error", err, name)
				}
			}
		})
	}
}
