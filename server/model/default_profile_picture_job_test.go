// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"testing"
)

func TestDefaultProfilePictureCommandIsVersionedTypedAndStrict(t *testing.T) {
	t.Parallel()
	userID := NewUserID()
	document, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommandV1{UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDefaultProfilePictureCommand(1, document)
	if err != nil || decoded.UserID != userID {
		t.Fatalf("DecodeDefaultProfilePictureCommand() = %#v, %v", decoded, err)
	}

	tests := []struct {
		name     string
		version  int
		document json.RawMessage
	}{
		{name: "unsupported version", version: 2, document: document},
		{name: "unknown field", version: 1, document: json.RawMessage(`{"user_id":"` + userID.String() + `","extra":true}`)},
		{name: "invalid user ID", version: 1, document: json.RawMessage(`{"user_id":"bad"}`)},
		{name: "trailing document", version: 1, document: append(append(json.RawMessage(nil), document...), []byte(` {}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeDefaultProfilePictureCommand(test.version, test.document); err == nil {
				t.Fatal("DecodeDefaultProfilePictureCommand() accepted invalid input")
			}
		})
	}
}
