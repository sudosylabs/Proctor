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
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

const (
	browserAuthenticationMaintenancePageSize     = 500
	browserAuthenticationMaintenanceMaximumPages = 20
	browserAuthenticationMaintenanceInterval     = time.Minute
)

type browserAuthenticationMaintainer interface {
	Maintain(context.Context, int) (*store.BrowserAuthenticationMaintenanceResult, error)
}

// browserAuthenticationMaintenancePeriodicRunner performs one bounded runtime
// pass. Every node may run it: PostgreSQL row claiming converges safely without
// a durable Job, Attempt, occurrence, or permanent deduplication-ledger row.
type browserAuthenticationMaintenancePeriodicRunner struct {
	transactions browserAuthenticationMaintainer
}

func (runner browserAuthenticationMaintenancePeriodicRunner) Run(ctx context.Context) error {
	if runner.transactions == nil {
		return errors.New("browser authentication maintenance persistence is unavailable")
	}
	for page := 0; page < browserAuthenticationMaintenanceMaximumPages; page++ {
		maintained, err := runner.transactions.Maintain(ctx, browserAuthenticationMaintenancePageSize)
		if err != nil {
			return err
		}
		if maintained == nil || maintained.Expired < 0 || maintained.Purged < 0 {
			return errors.New("browser authentication maintenance returned an invalid result")
		}
		if !maintained.More {
			return nil
		}
	}
	return nil
}
