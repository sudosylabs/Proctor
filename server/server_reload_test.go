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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/logging"
)

type reloadingPlatform struct {
	lifecyclePlatform
	reloads int
	err     error
}

func (p *reloadingPlatform) ReloadConfiguration(context.Context) (config.ReloadResult, error) {
	p.reloads++
	return config.ReloadResult{}, p.err
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
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	configurationErr := errors.New("invalid configuration file")
	platform := &reloadingPlatform{err: configurationErr}
	httpServer := &reloadingHTTPRuntime{}
	node := &Server{components: runtimeComponents{platform: platform, logger: logger}, http: httpServer}

	err = node.Reload(context.Background())
	require.ErrorIs(t, err, configurationErr, "Reload()")
	require.Equal(t, 1, platform.reloads, "reloads configuration")
	require.Equal(t, 1, httpServer.reloads, "reloads configuration")

	platform.err = nil
	certificateErr := errors.New("unreadable certificate")
	httpServer.err = certificateErr
	err = node.Reload(context.Background())
	require.ErrorIs(t, err, certificateErr, "Reload()")
	require.NotErrorIs(t, err, configurationErr, "Reload()")

	inert := &Server{components: runtimeComponents{platform: &reloadingPlatform{}, logger: logger}}
	require.NoError(t, inert.Reload(context.Background()))
}
