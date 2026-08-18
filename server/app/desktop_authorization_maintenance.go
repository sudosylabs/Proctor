// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

const (
	desktopAuthorizationMaintenancePageSize     = 500
	desktopAuthorizationMaintenanceMaximumPages = 20
	desktopAuthorizationMaintenanceInterval     = time.Minute
)

type desktopAuthorizationMaintainer interface {
	Maintain(context.Context, int) (*store.DesktopAuthorizationMaintenanceResult, error)
}

// desktopAuthorizationMaintenancePeriodicRunner performs one bounded runtime
// pass. Every node may run it: PostgreSQL row claiming converges safely without
// a durable Job, Attempt, occurrence, or permanent deduplication-ledger row.
type desktopAuthorizationMaintenancePeriodicRunner struct {
	transactions desktopAuthorizationMaintainer
}

func (runner desktopAuthorizationMaintenancePeriodicRunner) Run(ctx context.Context) error {
	if runner.transactions == nil {
		return errors.New("desktop authorization maintenance persistence is unavailable")
	}
	for page := 0; page < desktopAuthorizationMaintenanceMaximumPages; page++ {
		maintained, err := runner.transactions.Maintain(ctx, desktopAuthorizationMaintenancePageSize)
		if err != nil {
			return err
		}
		if maintained == nil || maintained.Expired < 0 || maintained.Purged < 0 {
			return errors.New("desktop authorization maintenance returned an invalid result")
		}
		if !maintained.More {
			return nil
		}
	}
	return nil
}
