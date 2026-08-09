// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DefaultProfilePictureCommandV1 is the stable intent carried by a
// profile_picture.generate_default Job. It lives beside Job so aggregate
// persistence boundaries can verify that the immutable target agrees with the
// User they are creating without depending on the application package.
type DefaultProfilePictureCommandV1 struct {
	UserID UserID `json:"user_id"`
}

func EncodeDefaultProfilePictureCommand(command DefaultProfilePictureCommandV1) (json.RawMessage, error) {
	if !command.UserID.IsValid() {
		return nil, errors.New("default profile-picture command has invalid user ID")
	}
	return json.Marshal(command)
}

func DecodeDefaultProfilePictureCommand(version int, document json.RawMessage) (DefaultProfilePictureCommandV1, error) {
	var command DefaultProfilePictureCommandV1
	if version != 1 {
		return command, fmt.Errorf("unsupported default profile-picture command version %d", version)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, fmt.Errorf("decode default profile-picture command: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return command, errors.New("job command contains trailing JSON")
		}
		return command, fmt.Errorf("decode trailing job command: %w", err)
	}
	if !command.UserID.IsValid() {
		return command, errors.New("default profile-picture command has invalid user ID")
	}
	return command, nil
}
