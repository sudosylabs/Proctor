// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalAuthenticationMaintenanceUsesBoundedPages(t *testing.T) {
	results := make([]*store.ExternalLoginStateMaintenanceResult, externalAuthenticationMaintenanceMaximumPages+1)
	for index := range results {
		results[index] = &store.ExternalLoginStateMaintenanceResult{More: true}
	}
	fake := &externalAuthenticationMaintainerFake{results: results}
	require.NoError(t, (externalAuthenticationMaintenancePeriodicRunner{states: fake}).Run(context.Background()))
	require.Len(t, fake.limits, externalAuthenticationMaintenanceMaximumPages, "Maintain calls")
	for _, limit := range fake.limits {
		require.Equal(t, externalAuthenticationMaintenancePageSize, limit, "Maintain limit")
	}
}

type externalAuthenticationMaintainerFake struct {
	results []*store.ExternalLoginStateMaintenanceResult
	limits  []int
}

func (f *externalAuthenticationMaintainerFake) Maintain(_ context.Context, limit int) (*store.ExternalLoginStateMaintenanceResult, error) {
	f.limits = append(f.limits, limit)
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}
