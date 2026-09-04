// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import "time"

type runtimeTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type runtimeClock interface {
	Now() time.Time
	NewTicker(time.Duration) runtimeTicker
}

type systemRuntimeClock struct{}

func (systemRuntimeClock) Now() time.Time { return time.Now() }

func (systemRuntimeClock) NewTicker(interval time.Duration) runtimeTicker {
	return systemRuntimeTicker{ticker: time.NewTicker(interval)}
}

type systemRuntimeTicker struct {
	ticker *time.Ticker
}

func (t systemRuntimeTicker) Chan() <-chan time.Time { return t.ticker.C }
func (t systemRuntimeTicker) Stop()                  { t.ticker.Stop() }
