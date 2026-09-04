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

	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalAuthenticationMaintenanceUsesBoundedPages(t *testing.T) {
	results := make([]*store.ExternalLoginStateMaintenanceResult, externalAuthenticationMaintenanceMaximumPages+1)
	for index := range results {
		results[index] = &store.ExternalLoginStateMaintenanceResult{More: true}
	}
	fake := &externalAuthenticationMaintainerFake{results: results}
	if err := (externalAuthenticationMaintenancePeriodicRunner{states: fake}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.limits) != externalAuthenticationMaintenanceMaximumPages {
		t.Fatalf("Maintain calls=%d", len(fake.limits))
	}
	for _, limit := range fake.limits {
		if limit != externalAuthenticationMaintenancePageSize {
			t.Fatalf("Maintain limit=%d", limit)
		}
	}
}

type externalAuthenticationMaintainerFake struct {
	results []*store.ExternalLoginStateMaintenanceResult
	limits  []int
}

func (fake *externalAuthenticationMaintainerFake) Maintain(_ context.Context, limit int) (*store.ExternalLoginStateMaintenanceResult, error) {
	fake.limits = append(fake.limits, limit)
	result := fake.results[0]
	fake.results = fake.results[1:]
	return result, nil
}
