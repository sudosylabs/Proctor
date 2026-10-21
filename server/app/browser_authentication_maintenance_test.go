// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

type browserAuthenticationMaintainerFake struct {
	results []*store.BrowserAuthenticationMaintenanceResult
	limits  []int
	err     error
}

func (f *browserAuthenticationMaintainerFake) Maintain(_ context.Context, limit int) (*store.BrowserAuthenticationMaintenanceResult, error) {
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestBrowserAuthenticationMaintenancePeriodicRunnerProcessesOnlyBoundedPages(t *testing.T) {
	t.Parallel()
	results := make([]*store.BrowserAuthenticationMaintenanceResult, browserAuthenticationMaintenanceMaximumPages+1)
	for index := range results {
		results[index] = &store.BrowserAuthenticationMaintenanceResult{Expired: 1, More: true}
	}
	maintainer := &browserAuthenticationMaintainerFake{results: results}
	err := (browserAuthenticationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background())
	require.NoError(t, err, "Run() error")
	require.Len(t, maintainer.limits, browserAuthenticationMaintenanceMaximumPages, "Run() error")
	for _, limit := range maintainer.limits {
		require.Equal(t, browserAuthenticationMaintenancePageSize, limit, "Maintain() limit")
	}
}

func TestBrowserAuthenticationMaintenancePeriodicRunnerStopsAndReportsFailure(t *testing.T) {
	t.Parallel()
	maintainer := &browserAuthenticationMaintainerFake{results: []*store.BrowserAuthenticationMaintenanceResult{{More: false}}}
	require.NoError(t, (browserAuthenticationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()), "completed Run() error")
	require.Len(t, maintainer.limits, 1, "completed Run() error")
	wantErr := errors.New("database unavailable")
	maintainer = &browserAuthenticationMaintainerFake{err: wantErr}
	require.ErrorIs(t, (browserAuthenticationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()), wantErr, "failed Run() error")
}
