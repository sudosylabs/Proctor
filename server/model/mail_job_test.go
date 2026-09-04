// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "testing"

func TestMailDeliveryCommandIsStrictAndBounded(t *testing.T) {
	id := NewMailDeliveryID()
	raw, err := EncodeMailDeliveryCommand(MailDeliveryCommandV1{DeliveryID: id})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMailDeliveryCommand(1, raw)
	if err != nil || decoded.DeliveryID != id {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	for _, invalid := range [][]byte{[]byte(`{"delivery_id":"bad"}`), []byte(`{"delivery_id":"` + id.String() + `","extra":true}`), []byte(`{}`), append(append([]byte(nil), raw...), []byte(` {}`)...)} {
		if _, err = DecodeMailDeliveryCommand(1, invalid); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
