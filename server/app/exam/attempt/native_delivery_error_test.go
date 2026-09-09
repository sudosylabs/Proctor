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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryCorruptionRemainsInternal(t *testing.T) {
	for _, cause := range []error{model.ErrDeliveryInvalid, model.ErrNativeDeliveryInvalid, model.ErrDeliveryConflict, store.NewErrConflict("browser_delivery", "declaration_conflict", nil)} {
		corruption := errors.Join(store.ErrInvalidState, cause)
		for _, mapper := range []func(error) error{mapNativeDeliveryError, mapBrowserDeliveryError, mapStore} {
			var fault *Fault
			mapped := mapper(corruption)
			if !errors.As(mapped, &fault) || fault.Code != "exam.attempt.unavailable" || !errors.Is(mapped, cause) {
				t.Fatalf("corrupt retained value became client error: %v", mapped)
			}
		}
	}
	var invalid *Fault
	if mapped := mapNativeDeliveryError(model.ErrDeliveryInvalid); !errors.As(mapped, &invalid) || invalid.Code != "exam.attempt.invalid" {
		t.Fatalf("request validation changed: %v", mapped)
	}
}
