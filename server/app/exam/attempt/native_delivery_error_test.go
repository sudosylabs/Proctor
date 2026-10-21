// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryCorruptionRemainsInternal(t *testing.T) {
	for _, cause := range []error{model.ErrDeliveryInvalid, model.ErrNativeDeliveryInvalid, model.ErrDeliveryConflict, store.NewErrConflict("browser_delivery", "declaration_conflict", nil)} {
		corruption := errors.Join(store.ErrInvalidState, cause)
		for _, mapper := range []func(error) error{deliveryStoreError, func(err error) error { return storeError(storeErrors, err) }} {
			var fault *apperror.Error
			mapped := mapper(corruption)
			require.ErrorAs(t, mapped, &fault)
			require.Equal(t, "exam.attempt.unavailable", fault.Code())
			require.ErrorIs(t, mapped, cause)
		}
	}
	var invalid *apperror.Error
	require.ErrorAs(t, deliveryStoreError(model.ErrDeliveryInvalid), &invalid)
	require.Equal(t, "exam.attempt.invalid", invalid.Code())
}
