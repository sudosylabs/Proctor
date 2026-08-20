// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSittingMailExpansionCommandAndCheckpointAreStrictAndBounded(t *testing.T) {
	occurrenceID := NewMailOccurrenceID()
	command, err := EncodeSittingMailExpansionCommand(SittingMailExpansionCommandV1{OccurrenceID: occurrenceID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSittingMailExpansionCommand(1, command)
	if err != nil || decoded.OccurrenceID != occurrenceID {
		t.Fatalf("DecodeSittingMailExpansionCommand()=(%#v,%v)", decoded, err)
	}
	unknown := append(bytes.TrimSuffix(command, []byte("}")), []byte(`,"secret":"leak"}`)...)
	if _, err = DecodeSittingMailExpansionCommand(1, unknown); err == nil {
		t.Fatal("unknown command field was accepted")
	}

	checkpoint, err := EncodeSittingMailExpansionCheckpoint(SittingMailExpansionCheckpointV1{
		AfterUserID: NewUserID(), Expanded: 200, Suppressed: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeSittingMailExpansionCheckpoint(1, checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err = EncodeSittingMailExpansionCheckpoint(SittingMailExpansionCheckpointV1{Expanded: -1}); err == nil {
		t.Fatal("negative checkpoint count was accepted")
	}
}

func TestSittingMailFanoutBundleIsBoundedAndImmutableInShape(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	bundle := &MailFanoutBundle{ID: NewMailOccurrenceID(), EncryptedPayload: json.RawMessage(`{"key_id":"11111111111111111111111111111111"}`), CreatedAt: at, Revision: 1}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	bundle.EncryptedPayload = json.RawMessage(`"` + strings.Repeat("x", MailEncryptedFanoutBundleMaximumBytes) + `"`)
	if err := bundle.Validate(); err == nil {
		t.Fatal("oversized fan-out bundle was accepted")
	}
}
