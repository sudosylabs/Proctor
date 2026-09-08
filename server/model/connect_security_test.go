// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectSecurityClosedUnion(t *testing.T) {
	preflight := ConnectSecurity{Kind: "preflight", PreflightID: NewId(), ReportDigest: SHA256Fingerprint([]byte("report"))}
	resume := ConnectSecurity{Kind: "resume", ParticipationID: NewAttemptParticipationID(), Generation: 1, PolicyDigest: SHA256Fingerprint([]byte("policy"))}
	for _, value := range []ConnectSecurity{preflight, resume} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ConnectSecurity
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != value {
			t.Fatalf("roundtrip: %v", err)
		}
		for _, bad := range []string{strings.Replace(string(raw), "\"kind\"", "\"Kind\"", 1), strings.TrimSuffix(string(raw), "}") + `,"unknown":false}`, strings.TrimSuffix(string(raw), "}") + `,"kind":"resume"}`, strings.TrimSuffix(string(raw), "}") + `,"extra":null}`} {
			if json.Unmarshal([]byte(bad), &decoded) == nil {
				t.Fatalf("accepted %s", bad)
			}
		}
	}
	raw, _ := json.Marshal(preflight)
	for _, suffix := range []string{`,"generation":0}`, `,"participation_id":null}`, `,"policy_digest":""}`} {
		var decoded ConnectSecurity
		if json.Unmarshal([]byte(strings.TrimSuffix(string(raw), "}")+suffix), &decoded) == nil {
			t.Fatal("accepted contradictory union")
		}
	}
	for _, raw := range []string{`{}`, `null`, `{"kind":"resume","generation":9007199254740992}`, `{"kind":"preflight"}`} {
		var decoded ConnectSecurity
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
