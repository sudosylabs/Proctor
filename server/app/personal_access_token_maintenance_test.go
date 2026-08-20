// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/store"
)

type personalAccessTokenMaintainerFake struct {
	results []*store.PersonalAccessTokenPreparationMaintenanceResult
	limits  []int
	err     error
}

func (f *personalAccessTokenMaintainerFake) MaintainMutationPreparations(_ context.Context, limit int) (*store.PersonalAccessTokenPreparationMaintenanceResult, error) {
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestPersonalAccessTokenMaintenancePeriodicRunnerIsBounded(t *testing.T) {
	t.Parallel()
	results := make([]*store.PersonalAccessTokenPreparationMaintenanceResult, personalAccessTokenMaintenanceMaximumPages+1)
	for index := range results {
		results[index] = &store.PersonalAccessTokenPreparationMaintenanceResult{Failed: 1, More: true}
	}
	maintainer := &personalAccessTokenMaintainerFake{results: results}
	if err := (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()); err != nil || len(maintainer.limits) != personalAccessTokenMaintenanceMaximumPages {
		t.Fatalf("Run() error=%v limits=%#v", err, maintainer.limits)
	}
	for _, limit := range maintainer.limits {
		if limit != personalAccessTokenMaintenancePageSize {
			t.Fatalf("MaintainMutationPreparations() limit=%d", limit)
		}
	}
}

func TestPersonalAccessTokenMaintenancePeriodicRunnerReportsFailure(t *testing.T) {
	t.Parallel()
	maintainer := &personalAccessTokenMaintainerFake{results: []*store.PersonalAccessTokenPreparationMaintenanceResult{{}}}
	if err := (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()); err != nil || len(maintainer.limits) != 1 {
		t.Fatalf("completed Run() error=%v limits=%#v", err, maintainer.limits)
	}
	want := errors.New("database unavailable")
	maintainer = &personalAccessTokenMaintainerFake{err: want}
	if err := (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("failed Run() error=%v", err)
	}
}
