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
	"net"
	"testing"

	"github.com/sudosylabs/proctor/server/logging"
)

type reloadingPlatform struct {
	lifecyclePlatform
	reloads int
	err     error
}

func (p *reloadingPlatform) ReloadConfiguration(context.Context) error {
	p.reloads++
	return p.err
}

type reloadingHTTPRuntime struct {
	reloads int
	err     error
}

func (r *reloadingHTTPRuntime) Serve(net.Listener, func() bool) error { return nil }
func (r *reloadingHTTPRuntime) Shutdown(context.Context) error        { return nil }
func (r *reloadingHTTPRuntime) Close() error                          { return nil }
func (r *reloadingHTTPRuntime) ReloadCertificates() error {
	r.reloads++
	return r.err
}

func TestServerReloadRunsConfigurationAndCertificateReloadIndependently(t *testing.T) {
	t.Parallel()

	logger, err := logging.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	configurationErr := errors.New("invalid configuration file")
	platform := &reloadingPlatform{err: configurationErr}
	httpServer := &reloadingHTTPRuntime{}
	node := &Server{components: runtimeComponents{platform: platform, logger: logger}, http: httpServer}

	err = node.Reload(context.Background())
	if !errors.Is(err, configurationErr) {
		t.Fatalf("Reload() = %v, want the configuration failure", err)
	}
	if platform.reloads != 1 || httpServer.reloads != 1 {
		t.Fatalf("reloads configuration=%d certificates=%d, want both attempted once", platform.reloads, httpServer.reloads)
	}

	platform.err = nil
	certificateErr := errors.New("unreadable certificate")
	httpServer.err = certificateErr
	if err := node.Reload(context.Background()); !errors.Is(err, certificateErr) || errors.Is(err, configurationErr) {
		t.Fatalf("Reload() = %v, want only the certificate failure", err)
	}

	inert := &Server{components: runtimeComponents{platform: &reloadingPlatform{}, logger: logger}}
	if err := inert.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() before HTTP serving = %v", err)
	}
}
