// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSittingMailExpansionCommandAndCheckpointAreStrictAndBounded(t *testing.T) {
	occurrenceID := NewMailOccurrenceID()
	command, err := EncodeSittingMailExpansionCommand(SittingMailExpansionCommandV1{OccurrenceID: occurrenceID})
	require.NoError(t, err)
	decoded, err := DecodeSittingMailExpansionCommand(1, command)
	require.NoError(t, err, "DecodeSittingMailExpansionCommand()=(%#v,%v)", decoded, err)
	require.Equal(t, occurrenceID, decoded.OccurrenceID, "DecodeSittingMailExpansionCommand()=(%#v,%v)", decoded, err)
	unknown := append(bytes.TrimSuffix(command, []byte("}")), []byte(`,"secret":"leak"}`)...)
	_, err = DecodeSittingMailExpansionCommand(1, unknown)
	require.Error(t, err, "unknown command field was accepted")

	checkpoint, err := EncodeSittingMailExpansionCheckpoint(SittingMailExpansionCheckpointV1{
		AfterUserID: NewUserID(), Expanded: 200, Suppressed: 3,
	})
	require.NoError(t, err)
	_, err = DecodeSittingMailExpansionCheckpoint(1, checkpoint)
	require.NoError(t, err)
	_, err = EncodeSittingMailExpansionCheckpoint(SittingMailExpansionCheckpointV1{Expanded: -1})
	require.Error(t, err, "negative checkpoint count was accepted")
}

func TestSittingMailFanoutBundleIsBoundedAndImmutableInShape(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	bundle := &MailFanoutBundle{ID: NewMailOccurrenceID(), EncryptedPayload: json.RawMessage(`{"key_id":"11111111111111111111111111111111"}`), CreatedAt: at, Revision: 1}
	require.NoError(t, bundle.Validate())
	bundle.EncryptedPayload = json.RawMessage(`"` + strings.Repeat("x", MailEncryptedFanoutBundleMaximumBytes) + `"`)
	require.Error(t, bundle.Validate(), "oversized fan-out bundle was accepted")
}
