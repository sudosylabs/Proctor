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

	"github.com/stretchr/testify/require"
)

func TestConnectSecurityClosedUnion(t *testing.T) {
	preflight := ConnectSecurity{Kind: "preflight", PreflightID: NewID(), ReportDigest: SHA256Fingerprint([]byte("report"))}
	resume := ConnectSecurity{Kind: "resume", ParticipationID: NewAttemptParticipationID(), Generation: 1, PolicyDigest: SHA256Fingerprint([]byte("policy"))}
	for _, value := range []ConnectSecurity{preflight, resume} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var decoded ConnectSecurity
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, value, decoded)
		for _, bad := range []string{strings.Replace(string(raw), "\"kind\"", "\"Kind\"", 1), strings.TrimSuffix(string(raw), "}") + `,"unknown":false}`, strings.TrimSuffix(string(raw), "}") + `,"kind":"resume"}`, strings.TrimSuffix(string(raw), "}") + `,"extra":null}`} {
			require.Error(t, json.Unmarshal([]byte(bad), &decoded), "accepted %s", bad)
		}
	}
	raw, _ := json.Marshal(preflight)
	for _, suffix := range []string{`,"generation":0}`, `,"participation_id":null}`, `,"policy_digest":""}`} {
		var decoded ConnectSecurity
		require.Error(t, json.Unmarshal([]byte(strings.TrimSuffix(string(raw), "}")+suffix), &decoded), "accepted contradictory union")
	}
	for _, raw := range []string{`{}`, `null`, `{"kind":"resume","generation":9007199254740992}`, `{"kind":"preflight"}`} {
		var decoded ConnectSecurity
		require.Error(t, json.Unmarshal([]byte(raw), &decoded), "accepted %s", raw)
	}
}
