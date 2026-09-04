// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"
)

func TestBlockedBrowserActivityLocationShapes(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name     string
		reason   BrowserActivityBlockReason
		location BrowserLocation
	}{
		{"https origin", BrowserBlockOriginNotAllowed, BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/outside"}},
		{"http network", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "http", Host: "example.edu", Port: "8443", Path: "/outside"}},
		{"file scheme only", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "file"}},
		{"javascript scheme only", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "javascript"}},
		{"data scheme only", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "data"}},
		{"custom scheme only", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "proctor+tool"}},
		{"invalid URL sentinel", BrowserBlockInvalidURL, BrowserLocation{}},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			event := blockedBrowserActivityEventForTest(test.location, test.reason)
			if err := event.ValidateClientRecord(); err != nil {
				t.Fatalf("ValidateClientRecord() error = %v", err)
			}
		})
	}

	invalid := []struct {
		name     string
		reason   BrowserActivityBlockReason
		location BrowserLocation
	}{
		{"allowed scheme classified as blocked", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/"}},
		{"uppercase scheme", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "HTTP", Host: "example.edu", Path: "/"}},
		{"oversized scheme", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "x" + strings.Repeat("a", BrowserActivityLocationSchemeMaximumBytes)}},
		{"http without host", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "http", Path: "/"}},
		{"http default port retained", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "http", Host: "example.edu", Port: "80", Path: "/"}},
		{"hostless payload retained", BrowserBlockSchemeNotAllowed, BrowserLocation{Scheme: "javascript", Path: "/alert(secret)"}},
		{"invalid URL retained components", BrowserBlockInvalidURL, BrowserLocation{Scheme: "http"}},
		{"non-https origin block", BrowserBlockOriginNotAllowed, BrowserLocation{Scheme: "http", Host: "example.edu", Path: "/"}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			event := blockedBrowserActivityEventForTest(test.location, test.reason)
			if err := event.ValidateClientRecord(); err == nil {
				t.Fatal("ValidateClientRecord() accepted an invalid blocked location")
			}
		})
	}

	ruleID := "start"
	for _, reason := range []BrowserActivityBlockReason{BrowserBlockSchemeNotAllowed, BrowserBlockInvalidURL} {
		event := blockedBrowserActivityEventForTest(BrowserLocation{Scheme: "file"}, reason)
		if reason == BrowserBlockInvalidURL {
			event.Location = &BrowserLocation{}
		}
		event.MatchedRuleID = &ruleID
		if err := event.ValidateClientRecord(); err == nil {
			t.Fatalf("ValidateClientRecord() accepted matched rule for %s", reason)
		}
	}
}

func blockedBrowserActivityEventForTest(location BrowserLocation, reason BrowserActivityBlockReason) BrowserActivityEvent {
	return BrowserActivityEvent{Sequence: 1, Kind: BrowserActivityBlockedNavigation, PolicyRevisionID: NewExamRevisionID(),
		ClientOccurredAt: time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC), Location: &location, BlockReason: &reason}
}
