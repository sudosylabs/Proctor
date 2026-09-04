// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

func testJobExecution(record *model.Job, reserveWork func(context.Context, int, int) (bool, error), checkpoint func(context.Context, jobengine.CheckpointValue) error) jobengine.Execution {
	return jobengine.NewExecution(record, nil, checkpoint, reserveWork)
}

func allowJobWorkReservation() func(context.Context, int, int) (bool, error) {
	consumed := 0
	return func(_ context.Context, units, limit int) (bool, error) {
		if consumed+units > limit {
			return false, nil
		}
		consumed += units
		return true, nil
	}
}
