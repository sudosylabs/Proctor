// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package sqlstore

import (
	"context"

	"github.com/sudosylabs/proctor/server/store"
)

// No waiting goroutine or database connection is allocated when detail capacity
// is full. Both families and transports share this node's pool reservation.
func (s *SQLStore) enterDeliveryAppend(ctx context.Context, family string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.deliveryAppendSlots <- struct{}{}:
		return func() { <-s.deliveryAppendSlots }, nil
	default:
		return nil, store.NewErrConflict(family+"_delivery", "append_rate_limited", store.ErrDeliveryAdmissionFull)
	}
}
