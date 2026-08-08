// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cluster

import (
	"strings"
	"testing"
)

func TestMessageValidateAndClone(t *testing.T) {
	t.Parallel()

	message := &Message{
		Event: "test.event",
		Data:  []byte("payload"),
		Props: map[string]string{"k": "v"},
	}
	if err := message.Validate(); err != nil {
		t.Fatal(err)
	}
	cloned := message.Clone()
	cloned.Data[0] = 'X'
	cloned.Props["k"] = "changed"
	if string(message.Data) != "payload" || message.Props["k"] != "v" {
		t.Fatal("Clone() did not deep-copy data and props")
	}
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
			if err := test.message.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
