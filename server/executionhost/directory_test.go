// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package executionhost

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sudosylabs/execenv"
	"github.com/sudosylabs/execenv/memory"
	"github.com/sudosylabs/execenv/remote"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
)

func TestDirectoryDialsCatalogAndProjectsTree(t *testing.T) {
	inner, err := memory.New(memory.Config{Images: []execenv.Image{"toolchain"}, Slots: 2})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverContext, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go func() {
		_ = remote.Serve(serverContext, listener, inner, remote.ServerConfig{
			Security: remote.SecurityInsecureLocal, Token: []byte("secret"),
		})
	}()

	directory, err := New(Settings{Enabled: true, DialTimeout: time.Second, OperationTimeout: time.Second,
		Hosts: []HostConfig{{ID: "runner-a", Address: listener.Addr().String(), Security: "insecure_local", Token: "secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })
	catalog, err := directory.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || !catalog[0].Usable || !catalog[0].Isolated || catalog[0].Slots != 2 ||
		len(catalog[0].Images) != 1 || catalog[0].Images[0] != "toolchain" {
		t.Fatalf("Catalog() = %#v", catalog)
	}
	environment, err := directory.Ensure(t.Context(), "runner-a", appexecution.Spec{
		ID: "grant-a", Image: "toolchain", Network: appexecution.NetworkNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.ReplaceTree(t.Context(), appexecution.Tree{{Path: "main.go", Kind: appexecution.NodeFile,
		Version: "version-a", Data: []byte("package main\n")}}); err != nil {
		t.Fatal(err)
	}
	if err := directory.Revoke(t.Context(), "runner-a", "grant-a"); err != nil {
		t.Fatal(err)
	}
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
