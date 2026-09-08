// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package executionhost

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/execenv"
	"github.com/sudosylabs/execenv/memory"
	"github.com/sudosylabs/execenv/remote"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
)

func TestDirectoryDialsCatalogAndProjectsTree(t *testing.T) {
	fixture := newRemoteDirectoryFixture(t)
	directory := fixture.directory
	catalog, err := directory.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || !catalog[0].Usable || catalog[0].Isolated != fixture.directory.hosts["runner-a"].capabilities().Isolated || catalog[0].Slots != 2 ||
		len(catalog[0].Images) != 1 || catalog[0].Images[0] != "toolchain" {
		t.Fatalf("Catalog() = %#v", catalog)
	}
	spec := appexecution.Spec{ID: "grant-a", Image: "toolchain", Network: appexecution.NetworkNone}
	environment, err := directory.Ensure(t.Context(), "runner-a", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.ReplaceTree(t.Context(), appexecution.Tree{{Path: "main.go", Kind: appexecution.NodeFile,
		Version: "version-a", Data: []byte("package main\n")}}); err != nil {
		t.Fatal(err)
	}
	existing, err := directory.Existing(t.Context(), "runner-a", spec)
	if err != nil || existing != environment {
		t.Fatalf("Existing() = %v, %v; want original handle", existing, err)
	}
	reader, err := existing.Open(t.Context(), "main.go")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "package main\n" {
		t.Fatalf("Open() body = %q, %v", body, err)
	}
	observation, err := existing.Watch(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observation.Close() })
	terminal, err := existing.Attach(t.Context(), appexecution.Window{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	if err := existing.Freeze(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := existing.Thaw(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, mismatch := range []appexecution.Spec{
		{ID: "grant-b", Image: spec.Image, Network: spec.Network},
		{ID: spec.ID, Image: "another-image", Network: spec.Network},
		{ID: spec.ID, Image: spec.Image, Network: appexecution.NetworkAllowlist},
	} {
		if _, err := directory.Existing(t.Context(), "runner-a", mismatch); !errors.Is(err, appexecution.ErrUnavailable) {
			t.Fatalf("Existing(%#v) = %v, want unavailable", mismatch, err)
		}
	}
	if got := fixture.host.ensures.Load(); got != 1 {
		t.Fatalf("host Ensure calls = %d, want initialization only", got)
	}
	if err := directory.Revoke(t.Context(), "runner-a", "grant-a"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentDoesNotReconnectAfterProjection(t *testing.T) {
	for _, resetThrough := range []string{"watch", "catalog"} {
		t.Run(resetThrough, func(t *testing.T) {
			fixture := newRemoteDirectoryFixture(t)
			spec := appexecution.Spec{ID: "grant-a", Image: "toolchain", Network: appexecution.NetworkNone}
			environment, err := fixture.directory.Ensure(t.Context(), "runner-a", spec)
			if err != nil {
				t.Fatal(err)
			}
			if err := environment.ReplaceTree(t.Context(), appexecution.Tree{{Path: "answer", Kind: appexecution.NodeFile,
				Version: "v1", Data: []byte("acknowledged answer")}}); err != nil {
				t.Fatal(err)
			}
			fixture.disconnect(t)
			// A host restart also loses its guest. The listener remains available,
			// so an implicit reconnect/Ensure would create an empty replacement.
			if err := fixture.host.Revoke(t.Context(), execenv.ID(spec.ID)); err != nil {
				t.Fatal(err)
			}
			if resetThrough == "catalog" {
				if _, err := fixture.directory.Catalog(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			observation, err := environment.Watch(t.Context(), "")
			if observation != nil {
				_ = observation.Close()
			}
			if !errors.Is(err, appexecution.ErrUnavailable) {
				t.Fatalf("Watch after disconnect = %v, want unavailable", err)
			}
			if _, err := fixture.directory.Existing(t.Context(), "runner-a", spec); !errors.Is(err, appexecution.ErrUnavailable) {
				t.Fatalf("Existing after disconnect = %v, want unavailable", err)
			}
			catalog, err := fixture.directory.Catalog(t.Context())
			if err != nil || len(catalog) != 1 || !catalog[0].Usable || catalog[0].Slots != 2 {
				t.Fatalf("Catalog after reconnect = %#v, %v; want healthy host without a recreated guest", catalog, err)
			}
			if got := fixture.host.ensures.Load(); got != 1 {
				t.Fatalf("host Ensure calls = %d, want one before disconnect", got)
			}
		})
	}
}

func TestRetainedEnvironmentCannotRecreateAfterRevoke(t *testing.T) {
	fixture := newRemoteDirectoryFixture(t)
	spec := appexecution.Spec{ID: "grant-a", Image: "toolchain", Network: appexecution.NetworkNone}
	environment, err := fixture.directory.Ensure(t.Context(), "runner-a", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.directory.Revoke(t.Context(), "runner-a", spec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.directory.Existing(t.Context(), "runner-a", spec); !errors.Is(err, appexecution.ErrUnavailable) {
		t.Fatalf("Existing after Revoke = %v, want unavailable", err)
	}
	assertEnvironmentUnavailable(t, environment)
	catalog, err := fixture.directory.Catalog(t.Context())
	if err != nil || len(catalog) != 1 || catalog[0].Slots != 2 {
		t.Fatalf("Catalog after delayed calls = %#v, %v; want no recreated guest", catalog, err)
	}
	if got := fixture.host.ensures.Load(); got != 1 {
		t.Fatalf("host Ensure calls = %d, want initialization only", got)
	}
	// Even explicitly ensuring the ID again must not revive the old handle:
	// native remote handles address an ID within their original connection.
	replacement, err := fixture.directory.Ensure(t.Context(), "runner-a", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.ReplaceTree(t.Context(), appexecution.Tree{{Path: "answer", Kind: appexecution.NodeFile,
		Version: "v2", Data: []byte("replacement answer")}}); err != nil {
		t.Fatal(err)
	}
	assertEnvironmentUnavailable(t, environment)
	reader, err := replacement.Open(t.Context(), "answer")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "replacement answer" {
		t.Fatalf("replacement body after delayed calls = %q, %v", body, err)
	}
	if got := fixture.host.ensures.Load(); got != 2 {
		t.Fatalf("host Ensure calls = %d, want the two explicit initializations", got)
	}
}

func TestDirectoryCloseForgetsEnvironmentAndPreventsReconnect(t *testing.T) {
	fixture := newRemoteDirectoryFixture(t)
	spec := appexecution.Spec{ID: "grant-a", Image: "toolchain", Network: appexecution.NetworkNone}
	environment, err := fixture.directory.Ensure(t.Context(), "runner-a", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.directory.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.directory.Existing(t.Context(), "runner-a", spec); !errors.Is(err, appexecution.ErrUnavailable) {
		t.Fatalf("Existing after Close = %v, want unavailable", err)
	}
	assertEnvironmentUnavailable(t, environment)
	if _, err := fixture.directory.Ensure(t.Context(), "runner-a", spec); !errors.Is(err, appexecution.ErrUnavailable) {
		t.Fatalf("Ensure after Close = %v, want unavailable", err)
	}
	if got := fixture.host.ensures.Load(); got != 1 {
		t.Fatalf("host Ensure calls = %d, want initialization only", got)
	}
}

func assertEnvironmentUnavailable(t *testing.T, environment appexecution.Environment) {
	t.Helper()
	operations := map[string]func() error{
		"replace": func() error { return environment.ReplaceTree(t.Context(), appexecution.Tree{}) },
		"freeze":  func() error { return environment.Freeze(t.Context()) },
		"thaw":    func() error { return environment.Thaw(t.Context()) },
		"watch": func() error {
			observation, err := environment.Watch(t.Context(), "")
			if observation != nil {
				_ = observation.Close()
			}
			return err
		},
		"open": func() error {
			reader, err := environment.Open(t.Context(), "answer")
			if reader != nil {
				_ = reader.Close()
			}
			return err
		},
		"attach": func() error {
			terminal, err := environment.Attach(t.Context(), appexecution.Window{})
			if terminal != nil {
				_ = terminal.Close()
			}
			return err
		},
	}
	for name, operation := range operations {
		if err := operation(); !errors.Is(err, appexecution.ErrUnavailable) {
			t.Errorf("%s on forgotten environment = %v, want unavailable", name, err)
		}
	}
}

type remoteDirectoryFixture struct {
	directory   *Directory
	host        *countingExecutionHost
	connections <-chan net.Conn
}

func newRemoteDirectoryFixture(t *testing.T) remoteDirectoryFixture {
	t.Helper()
	inner, err := memory.New(memory.Config{Images: []execenv.Image{"toolchain"}, Slots: 2})
	if err != nil {
		t.Fatal(err)
	}
	host := &countingExecutionHost{Host: inner}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 8)
	serverContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- remote.Serve(serverContext, acceptedConnectionListener{Listener: listener, connections: connections}, host, remote.ServerConfig{
			Security: remote.SecurityInsecureLocal, Token: []byte("secret"),
		})
	}()
	t.Cleanup(func() {
		stop()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("remote Serve = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("remote Serve did not stop")
		}
	})
	directory, err := New(Settings{Enabled: true, DialTimeout: time.Second, OperationTimeout: time.Second,
		Hosts: []HostConfig{{ID: "runner-a", Address: listener.Addr().String(), Security: "insecure_local", Token: "secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })
	return remoteDirectoryFixture{directory: directory, host: host, connections: connections}
}

func (fixture remoteDirectoryFixture) disconnect(t *testing.T) {
	t.Helper()
	select {
	case connection := <-fixture.connections:
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("execution host did not accept the original connection")
	}
}

type acceptedConnectionListener struct {
	net.Listener
	connections chan<- net.Conn
}

func (listener acceptedConnectionListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err == nil {
		listener.connections <- connection
	}
	return connection, err
}

type countingExecutionHost struct {
	execenv.Host
	ensures atomic.Int64
}

func (host *countingExecutionHost) Ensure(ctx context.Context, spec execenv.Spec) (execenv.Env, error) {
	host.ensures.Add(1)
	return host.Host.Ensure(ctx, spec)
}

func TestDisabledDirectoryDoesNotReadTLSFiles(t *testing.T) {
	directory, err := New(Settings{Hosts: []HostConfig{{ID: "staged", Security: "tls", CAFile: "/missing"}}})
	if err != nil {
		t.Fatalf("New(disabled) = %v", err)
	}
	if err := directory.Check(t.Context()); err != nil {
		t.Fatalf("Check(disabled) = %v", err)
	}
}

func TestEnvironmentRefusesUnsafeIncrementalProjectionBeforeHostIO(t *testing.T) {
	t.Parallel()
	// No host is installed: touching the transport would panic. The refusal
	// prevents bypassing fenced journal projection, independent of host semantics.
	env := &environment{}
	if err := env.Apply(t.Context(), []appexecution.Mutation{{Operation: appexecution.OperationCreate, Kind: appexecution.NodeFile, Path: "saved", Version: "v1", Data: []byte("acknowledged")}}); !errors.Is(err, appexecution.ErrConflict) {
		t.Fatalf("Apply = %v, want an explicit projection conflict", err)
	}
}
