// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package cluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageValidateAndClone(t *testing.T) {
	t.Parallel()

	message := &Message{
		Event: "test.event",
		Data:  []byte("payload"),
		Props: map[string]string{"k": "v"},
	}
	require.NoError(t, message.Validate())
	cloned := message.Clone()
	cloned.Data[0] = 'X'
	cloned.Props["k"] = "changed"
	require.Equal(t, "payload", string(message.Data), "Clone() did not deep-copy data and props")
	require.Equal(t, "v", message.Props["k"], "Clone() did not deep-copy data and props")
}

func TestMessageValidationBoundsUntrustedContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message *Message
	}{
		{name: "none event", message: &Message{Event: EventNone}},
		{name: "invalid event", message: &Message{Event: "bad event"}},
		{
			name: "oversized data",
			message: &Message{
				Event: "valid",
				Data:  make([]byte, MaxMessageBytes+1),
			},
		},
		{
			name: "invalid prop key",
			message: &Message{
				Event: "valid",
				Props: map[string]string{"bad key": "v"},
			},
		},
		{
			name: "oversized prop value",
			message: &Message{
				Event: "valid",
				Props: map[string]string{"k": strings.Repeat("x", MaxPropValueBytes+1)},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, test.message.Validate(), "expected validation error")
		})
	}
}
