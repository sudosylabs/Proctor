// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultProfilePictureCommandIsTypedAndStrict(t *testing.T) {
	t.Parallel()
	userID := NewUserID()
	document, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommand{UserID: userID})
	require.NoError(t, err)
	decoded, err := DecodeDefaultProfilePictureCommand(document)
	require.NoError(t, err, "DecodeDefaultProfilePictureCommand() = %#v", decoded)
	require.Equal(t, userID, decoded.UserID, "DecodeDefaultProfilePictureCommand() = %#v", decoded)

	tests := []struct {
		name     string
		document json.RawMessage
	}{
		{name: "unknown field", document: json.RawMessage(`{"user_id":"` + userID.String() + `","extra":true}`)},
		{name: "invalid user ID", document: json.RawMessage(`{"user_id":"bad"}`)},
		{name: "trailing document", document: append(append(json.RawMessage(nil), document...), []byte(` {}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeDefaultProfilePictureCommand(test.document)
			require.Error(t, err, "DecodeDefaultProfilePictureCommand() accepted invalid input")
		})
	}
}
