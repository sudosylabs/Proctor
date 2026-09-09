// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"
)

func TestSecurityPreflightResultValidation(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	valid := SecurityPreflightResult{PreflightID: NewId(), ReportDigest: SHA256Fingerprint([]byte("report")), Admission: "eligible", ReasonCodes: []SecurityReasonCode{}, ServerTime: now, ExpiresAt: now.Add(time.Minute)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*SecurityPreflightResult)
	}{
		{"missing timestamp", func(r *SecurityPreflightResult) { r.ExpiresAt = time.Time{} }},
		{"submillisecond timestamp", func(r *SecurityPreflightResult) { r.ServerTime = r.ServerTime.Add(time.Nanosecond) }},
		{"unknown reason", func(r *SecurityPreflightResult) {
			r.Admission = "blocked"
			r.ReasonCodes = []SecurityReasonCode{"unknown"}
		}},
		{"duplicate reason", func(r *SecurityPreflightResult) {
			r.Admission = "blocked"
			r.ReasonCodes = []SecurityReasonCode{SecurityReasonPreflightExpired, SecurityReasonPreflightExpired}
		}},
		{"inconsistent eligibility", func(r *SecurityPreflightResult) { r.ReasonCodes = []SecurityReasonCode{SecurityReasonPreflightExpired} }},
		{"missing reason", func(r *SecurityPreflightResult) { r.Admission = "blocked" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := valid
			tc.change(&value)
			if value.Validate() == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	expired := valid
	expired.Admission = "blocked"
	expired.ReasonCodes = []SecurityReasonCode{SecurityReasonPreflightExpired}
	expired.ServerTime = expired.ExpiresAt.Add(time.Minute)
	if err := expired.Validate(); err != nil {
		t.Fatalf("legitimate expired refusal rejected: %v", err)
	}
}
