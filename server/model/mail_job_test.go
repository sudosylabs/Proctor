// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailDeliveryCommandIsStrictAndBounded(t *testing.T) {
	id := NewMailDeliveryID()
	raw, err := EncodeMailDeliveryCommand(MailDeliveryCommand{DeliveryID: id})
	require.NoError(t, err)
	decoded, err := DecodeMailDeliveryCommand(raw)
	require.NoError(t, err, "Decode() = %#v", decoded)
	require.Equal(t, id, decoded.DeliveryID, "Decode() = %#v", decoded)
	for _, invalid := range [][]byte{[]byte(`{"delivery_id":"bad"}`), []byte(`{"delivery_id":"` + id.String() + `","extra":true}`), []byte(`{}`), append(append([]byte(nil), raw...), []byte(` {}`)...)} {
		_, err = DecodeMailDeliveryCommand(invalid)
		require.Error(t, err, "accepted %s", invalid)
	}
}
