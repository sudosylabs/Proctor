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
	externalAuthenticationMaintenancePageSize     = 500
	externalAuthenticationMaintenanceMaximumPages = 20
	externalAuthenticationMaintenanceInterval     = time.Minute
)

type externalAuthenticationMaintainer interface {
	Maintain(context.Context, int) (*store.ExternalLoginStateMaintenanceResult, error)
}

// externalAuthenticationMaintenancePeriodicRunner reconciles abandoned
// provider-connection audit attempts and retained browser state in bounded,
// database-clock pages. Every node may run it; PostgreSQL row claiming makes
// overlapping passes safe without a durable maintenance Job.
type externalAuthenticationMaintenancePeriodicRunner struct {
	states externalAuthenticationMaintainer
}

func (runner externalAuthenticationMaintenancePeriodicRunner) Run(ctx context.Context) error {
	if runner.states == nil {
		return errors.New("external authentication maintenance persistence is unavailable")
	}
	for page := 0; page < externalAuthenticationMaintenanceMaximumPages; page++ {
		maintained, err := runner.states.Maintain(ctx, externalAuthenticationMaintenancePageSize)
		if err != nil {
			return err
		}
		if maintained == nil || maintained.Terminalized < 0 || maintained.Purged < 0 {
			return errors.New("external authentication maintenance returned an invalid result")
		}
		if !maintained.More {
			return nil
		}
	}
	return nil
}
