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
	require.NoError(t, (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()), "Run() error")
	require.Len(t, maintainer.limits, personalAccessTokenMaintenanceMaximumPages, "Run() error")
	for _, limit := range maintainer.limits {
		require.Equal(t, personalAccessTokenMaintenancePageSize, limit, "MaintainMutationPreparations() limit")
	}
}

func TestPersonalAccessTokenMaintenancePeriodicRunnerReportsFailure(t *testing.T) {
	t.Parallel()
	maintainer := &personalAccessTokenMaintainerFake{results: []*store.PersonalAccessTokenPreparationMaintenanceResult{{}}}
	require.NoError(t, (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()), "completed Run() error")
	require.Len(t, maintainer.limits, 1, "completed Run() error")
	want := errors.New("database unavailable")
	maintainer = &personalAccessTokenMaintainerFake{err: want}
	require.ErrorIs(t, (personalAccessTokenMaintenancePeriodicRunner{tokens: maintainer}).Run(context.Background()), want)
}
