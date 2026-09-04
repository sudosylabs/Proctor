// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import "sync/atomic"

type Health struct {
	ready atomic.Bool
}

func (h *Health) Live() bool {
	return true
}

func (h *Health) Ready() bool {
	return h.ready.Load()
}

func (h *Health) SetReady(ready bool) {
	h.ready.Store(ready)
}
