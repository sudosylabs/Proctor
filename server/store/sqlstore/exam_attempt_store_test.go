// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"testing"
	"time"
)

func TestExamAttemptConnectOutcomeIsBoundedAndCredentialFree(t *testing.T) {
	outcome := examAttemptConnectOutcomeV1{
		AttemptID: "01J00000000000000000000001", WorkspaceID: "01J00000000000000000000002",
		ParticipationID: "01J00000000000000000000003", ConnectionID: "01J00000000000000000000004",
		ExamID: "01J00000000000000000000005", SittingID: "01J00000000000000000000006",
		CandidateID: "01J00000000000000000000007", RevisionID: "01J00000000000000000000008",
		SessionID: "01J00000000000000000000009", ClassID: "01J0000000000000000000000A",
		StartedAt:      time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
		LeaseExpiresAt: time.Date(2026, time.August, 15, 12, 0, 20, 0, time.UTC), EntryCount: 1000,
		Generation: 1, FirstAdmission: true, ConnectionOpened: true,
	}
	encoded, err := encodeCommandOutcome(outcome)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > examAttemptConnectOutcomeMaximumBytes {
		t.Fatalf("connect outcome size = %d, maximum = %d", len(encoded), examAttemptConnectOutcomeMaximumBytes)
	}
	credentialHash := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if bytes.Contains(encoded, credentialHash) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatalf("connect outcome exposed credential material: %s", encoded)
	}
}
