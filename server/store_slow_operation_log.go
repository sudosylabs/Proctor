// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

// slowStoreOperationLog writes one warning per store operation that reached
// Database.SlowOperationThreshold. The record carries only the closed
// operation name and its duration: never SQL, arguments, rows, or error text.
type slowStoreOperationLog struct {
	logger *logging.Logger
}

var _ timerlayer.SlowRecorder = slowStoreOperationLog{}

func (l slowStoreOperationLog) ObserveSlow(ctx context.Context, operation timerlayer.Operation, duration time.Duration) {
	l.logger.WarnContext(
		ctx,
		"slow store operation",
		logging.String("component", "store"),
		logging.String("operation", operation.String()),
		logging.Int64("duration_ms", duration.Milliseconds()),
	)
}
