// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestDefaultProfilePictureCommandIsVersionedAndTyped(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	document, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommandV1{UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDefaultProfilePictureCommand(1, document)
	if err != nil || decoded.UserID != userID {
		t.Fatalf("DecodeDefaultProfilePictureCommand() = %#v, %v", decoded, err)
	}
	if _, err = DecodeDefaultProfilePictureCommand(2, document); err == nil {
		t.Fatal("DecodeDefaultProfilePictureCommand() accepted unsupported version")
	}
	if _, err = DecodeDefaultProfilePictureCommand(1, json.RawMessage(`{"user_id":"bad","extra":true}`)); err == nil {
		t.Fatal("DecodeDefaultProfilePictureCommand() accepted invalid/unknown fields")
	}
}
