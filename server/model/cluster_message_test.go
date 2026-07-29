// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"testing"
)

func TestClusterMessageValidateAndClone(t *testing.T) {
	t.Parallel()

	message := &ClusterMessage{
		Event:    "session.invalidate",
		SendType: ClusterSendReliable,
		Data:     []byte("payload"),
		Props:    map[string]string{"user_id": "user"},
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	cloned := message.Clone()
	cloned.Data[0] = 'P'
	cloned.Props["user_id"] = "changed"
	if string(message.Data) != "payload" || message.Props["user_id"] != "user" {
		t.Fatal("Clone exposed mutable message state")
	}
}

func TestClusterMessageValidationBoundsUntrustedContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message *ClusterMessage
	}{
		{name: "nil", message: nil},
		{name: "none event", message: &ClusterMessage{Event: ClusterEventNone, SendType: ClusterSendBestEffort}},
		{name: "invalid event", message: &ClusterMessage{Event: "bad event", SendType: ClusterSendBestEffort}},
		{name: "invalid send type", message: &ClusterMessage{Event: "valid", SendType: "unknown"}},
		{
			name: "oversized data",
			message: &ClusterMessage{
				Event: "valid", SendType: ClusterSendBestEffort,
				Data: make([]byte, MaxClusterMessageBytes+1),
			},
		},
		{
			name: "invalid property",
			message: &ClusterMessage{
				Event: "valid", SendType: ClusterSendBestEffort,
				Props: map[string]string{"bad key": "value"},
			},
		},
		{
			name: "oversized property value",
			message: &ClusterMessage{
				Event: "valid", SendType: ClusterSendBestEffort,
				Props: map[string]string{"key": strings.Repeat("x", MaxClusterPropValueBytes+1)},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.message.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid message")
			}
		})
	}
}
