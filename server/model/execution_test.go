// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"testing"
	"time"
)

func TestExecutionProfileCanonicalRoundTripAndCapabilityValidation(t *testing.T) {
	t.Parallel()
	profile := ExecutionProfile{Enabled: true, Image: "universal-2026.08", Network: ExecutionNetworkAllowlist}
	encoded, err := EncodeExecutionProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecutionProfile(encoded)
	if err != nil || decoded != profile {
		t.Fatalf("round trip = %#v, %v", decoded, err)
	}
	if digest, err := ExecutionProfileDigest(profile); err != nil || len(digest) != 64 {
		t.Fatalf("digest = %q, %v", digest, err)
	}
	if !bytes.Contains(encoded, []byte(`"schema_version":1`)) {
		t.Fatalf("canonical profile = %s", encoded)
	}
	for _, invalid := range []ExecutionProfile{
		{Enabled: false, Image: "universal-2026.08", Network: ExecutionNetworkNone},
		{Enabled: true, Image: "", Network: ExecutionNetworkNone},
		{Enabled: true, Image: "universal", Network: ExecutionNetwork("open")},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("invalid profile accepted: %#v", invalid)
		}
	}
}

func TestExecutionGrantValidationFailsClosed(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	grant := &ExecutionGrant{ID: NewExecutionGrantID(), AttemptID: NewExamAttemptID(), HostID: "runner-a",
		Image: "go-1.26", Network: ExecutionNetworkNone, State: ExecutionGrantReserved,
		AppliedSittingState: ExamSittingOpen, AppliedSittingRevision: 1,
		CreatedAt: at, UpdatedAt: at, Revision: 1}
	if err := grant.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	invalid := *grant
	invalid.HostID = "runner/a"
	if err := invalid.Validate(); err == nil {
		t.Fatal("unsafe host ID was accepted")
	}
	invalid = *grant
	invalid.State = ExecutionGrantReleased
	if err := invalid.Validate(); err == nil {
		t.Fatal("released grant without released_at was accepted")
	}
}
