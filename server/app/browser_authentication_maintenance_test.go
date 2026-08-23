// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"

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
	if err != nil || len(maintainer.limits) != browserAuthenticationMaintenanceMaximumPages {
		t.Fatalf("Run() error=%v limits=%#v", err, maintainer.limits)
	}
	for _, limit := range maintainer.limits {
		if limit != browserAuthenticationMaintenancePageSize {
			t.Fatalf("Maintain() limit=%d, want %d", limit, browserAuthenticationMaintenancePageSize)
		}
	}
}

func TestBrowserAuthenticationMaintenancePeriodicRunnerStopsAndReportsFailure(t *testing.T) {
	t.Parallel()
	maintainer := &browserAuthenticationMaintainerFake{results: []*store.BrowserAuthenticationMaintenanceResult{{More: false}}}
	if err := (browserAuthenticationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()); err != nil || len(maintainer.limits) != 1 {
		t.Fatalf("completed Run() error=%v limits=%#v", err, maintainer.limits)
	}
	wantErr := errors.New("database unavailable")
	maintainer = &browserAuthenticationMaintainerFake{err: wantErr}
	if err := (browserAuthenticationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("failed Run() error=%v, want %v", err, wantErr)
	}
}
