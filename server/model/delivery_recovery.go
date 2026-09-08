// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import "encoding/json"

// DeliveryRecovery is a bounded current observation, never an acknowledgement
// of the rejected request or permission to exceed a quota. Status can be absent
// after its independently retained family has retired.
type DeliveryRecovery struct {
	Family  string                      `json:"family"`
	Budget  DeliveryBudgetSnapshot      `json:"budget"`
	Native  *NativeSecurityStreamStatus `json:"native_status,omitempty"`
	Browser *BrowserSourceStatus        `json:"browser_status,omitempty"`
}

func (r DeliveryRecovery) Validate() error {
	if r.Budget.Validate() != nil {
		return ErrDeliveryInvalid
	}
	switch r.Family {
	case "native":
		if r.Browser != nil || r.Native != nil && r.Native.Validate() != nil {
			return ErrDeliveryInvalid
		}
	case "browser":
		if r.Native != nil || r.Browser != nil && (r.Browser.Validate() != nil || r.Browser.ParticipationID != r.Budget.ParticipationID || r.Browser.Generation != r.Budget.Generation) {
			return ErrDeliveryInvalid
		}
	default:
		return ErrDeliveryInvalid
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 32*1024 {
		return ErrDeliveryInvalid
	}
	return nil
}
