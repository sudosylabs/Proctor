// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package metrics

import "time"

// WorkStarted records admission to one of the two fixed node-local work pools.
func (m *Module) WorkStarted(pool string) {
	if validWorkPool(pool) {
		m.workActive.WithLabelValues(pool).Inc()
	}
}

// WorkFinished releases the active count after synchronous work has returned.
func (m *Module) WorkFinished(pool string, duration time.Duration) {
	if validWorkPool(pool) {
		m.workActive.WithLabelValues(pool).Dec()
		m.workDuration.WithLabelValues(pool).Observe(duration.Seconds())
	}
}

// WorkRejected records immediate refusal, without user, file, or error labels.
func (m *Module) WorkRejected(pool string) {
	if validWorkPool(pool) {
		m.workRejected.WithLabelValues(pool).Inc()
	}
}

func validWorkPool(pool string) bool { return pool == "password" || pool == "file_content" }
