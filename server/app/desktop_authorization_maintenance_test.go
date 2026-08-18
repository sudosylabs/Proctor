// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/store"
)

type desktopAuthorizationMaintainerFake struct {
	results []*store.DesktopAuthorizationMaintenanceResult
	limits  []int
	err     error
}

func (f *desktopAuthorizationMaintainerFake) Maintain(_ context.Context, limit int) (*store.DesktopAuthorizationMaintenanceResult, error) {
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestDesktopAuthorizationMaintenancePeriodicRunnerProcessesOnlyBoundedPages(t *testing.T) {
	t.Parallel()
	results := make([]*store.DesktopAuthorizationMaintenanceResult, desktopAuthorizationMaintenanceMaximumPages+1)
	for index := range results {
		results[index] = &store.DesktopAuthorizationMaintenanceResult{Expired: 1, More: true}
	}
	maintainer := &desktopAuthorizationMaintainerFake{results: results}
	err := (desktopAuthorizationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background())
	if err != nil || len(maintainer.limits) != desktopAuthorizationMaintenanceMaximumPages {
		t.Fatalf("Run() error=%v limits=%#v", err, maintainer.limits)
	}
	for _, limit := range maintainer.limits {
		if limit != desktopAuthorizationMaintenancePageSize {
			t.Fatalf("Maintain() limit=%d, want %d", limit, desktopAuthorizationMaintenancePageSize)
		}
	}
}

func TestDesktopAuthorizationMaintenancePeriodicRunnerStopsAndReportsFailure(t *testing.T) {
	t.Parallel()
	maintainer := &desktopAuthorizationMaintainerFake{results: []*store.DesktopAuthorizationMaintenanceResult{{More: false}}}
	if err := (desktopAuthorizationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()); err != nil || len(maintainer.limits) != 1 {
		t.Fatalf("completed Run() error=%v limits=%#v", err, maintainer.limits)
	}
	wantErr := errors.New("database unavailable")
	maintainer = &desktopAuthorizationMaintainerFake{err: wantErr}
	if err := (desktopAuthorizationMaintenancePeriodicRunner{transactions: maintainer}).Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("failed Run() error=%v, want %v", err, wantErr)
	}
}
