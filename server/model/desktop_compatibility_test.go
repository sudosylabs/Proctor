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

func TestInitialDesktopCompatibilityPolicyIsValid(t *testing.T) {
	t.Parallel()

	policy := NewInitialDesktopCompatibilityPolicy(NewInstitutionID(), time.UnixMilli(100))
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 1 || policy.MinimumDesktopRelease != "" ||
		len(policy.RevokedDesktopBuildIDs) != 0 || policy.AdministratorMessage != "" ||
		policy.Availability != DesktopAvailabilityReady || policy.RetryAt.Valid {
		t.Fatalf("initial policy = %#v", policy)
	}
}

func TestDesktopCompatibilityPolicySettingsRejectInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings DesktopCompatibilityPolicySettings
	}{
		{name: "missing availability", settings: DesktopCompatibilityPolicySettings{}},
		{name: "invalid availability", settings: DesktopCompatibilityPolicySettings{Availability: "offline"}},
		{name: "ready with retry time", settings: DesktopCompatibilityPolicySettings{
			Availability: DesktopAvailabilityReady,
			RetryAt:      OptionalTimeFrom(time.UnixMilli(500)),
		}},
		{name: "noncanonical release", settings: DesktopCompatibilityPolicySettings{MinimumDesktopRelease: "v1.2.3"}},
		{name: "invalid release", settings: DesktopCompatibilityPolicySettings{MinimumDesktopRelease: "1.2"}},
		{name: "invalid build id", settings: DesktopCompatibilityPolicySettings{RevokedDesktopBuildIDs: []string{"build/id"}}},
		{name: "duplicate build id", settings: DesktopCompatibilityPolicySettings{RevokedDesktopBuildIDs: []string{"build-1", "build-1"}}},
		{name: "unordered build ids", settings: DesktopCompatibilityPolicySettings{RevokedDesktopBuildIDs: []string{"build-2", "build-1"}}},
		{name: "too many build ids", settings: DesktopCompatibilityPolicySettings{RevokedDesktopBuildIDs: make([]string, DesktopCompatibilityPolicyRevokedBuildMaxCount+1)}},
		{name: "untrimmed message", settings: DesktopCompatibilityPolicySettings{AdministratorMessage: " maintenance"}},
		{name: "control in message", settings: DesktopCompatibilityPolicySettings{AdministratorMessage: "maintenance\nnow"}},
		{name: "too many message characters", settings: DesktopCompatibilityPolicySettings{AdministratorMessage: strings.Repeat("a", DesktopCompatibilityPolicyMessageMaxRunes+1)}},
		{name: "too many message bytes", settings: DesktopCompatibilityPolicySettings{AdministratorMessage: strings.Repeat("é", DesktopCompatibilityPolicyMessageMaxBytes/2+1)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := test.settings
			if candidate.Availability == "" && test.name != "missing availability" {
				candidate.Availability = DesktopAvailabilityReady
			}
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid settings")
			}
		})
	}
}

func TestDesktopCompatibilityPolicyReplaceIsRevisionFencedAndDefensive(t *testing.T) {
	t.Parallel()

	policy := NewInitialDesktopCompatibilityPolicy(NewInstitutionID(), time.UnixMilli(100))
	settings := DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  "1.2.3",
		RevokedDesktopBuildIDs: []string{"build-1", "build-2"},
		AdministratorMessage:   "Update Proctor Desktop before your next exam.",
		Availability:           DesktopAvailabilityMaintenance,
		RetryAt:                OptionalTimeFrom(time.UnixMilli(500)),
	}
	if err := policy.Replace(1, settings, time.UnixMilli(200)); err != nil {
		t.Fatal(err)
	}
	settings.RevokedDesktopBuildIDs[0] = "mutated"
	if policy.Revision != 2 || policy.RevokedDesktopBuildIDs[0] != "build-1" ||
		policy.Availability != DesktopAvailabilityMaintenance || !policy.RetryAt.Valid {
		t.Fatalf("replacement = %#v", policy)
	}
	if err := policy.Replace(1, policy.Settings(), time.UnixMilli(300)); err != ErrDesktopCompatibilityPolicyRevisionConflict {
		t.Fatalf("stale Replace() error = %v", err)
	}
	staleRetry := settings
	staleRetry.RetryAt = OptionalTimeFrom(time.UnixMilli(300))
	if err := policy.Replace(policy.Revision, staleRetry, time.UnixMilli(300)); err == nil {
		t.Fatal("Replace() accepted a retry time at the policy decision time")
	}
	malformed := policy.Clone()
	malformed.RetryAt = OptionalTimeFrom(malformed.UpdatedAt)
	if err := malformed.Validate(); err == nil {
		t.Fatal("Validate() accepted a nonfuture persisted retry time")
	}
}

func TestDesktopBuildTupleValidatesSignedCatalogIdentity(t *testing.T) {
	t.Parallel()

	valid := DesktopBuildTuple{
		DesktopRelease:                          "1.2.3",
		DesktopBuildID:                          "desktop-1.2.3-darwin-arm64",
		Platform:                                DesktopPlatformDarwin,
		Architecture:                            DesktopArchitectureARM64,
		RealtimeProtocol:                        1,
		AttemptConfigurationManifestFingerprint: CurrentAttemptConfigurationManifestFingerprint(),
		DesktopSettingsRegistryFingerprint:      "sha256:" + strings.Repeat("b", 64),
		CapabilityMatrixIdentity:                "matrix-1.2.3",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*DesktopBuildTuple)
	}{
		{name: "release", mutate: func(value *DesktopBuildTuple) { value.DesktopRelease = "v1.2.3" }},
		{name: "build id", mutate: func(value *DesktopBuildTuple) { value.DesktopBuildID = "desktop/build" }},
		{name: "platform", mutate: func(value *DesktopBuildTuple) { value.Platform = "freebsd" }},
		{name: "architecture", mutate: func(value *DesktopBuildTuple) { value.Architecture = "amd64" }},
		{name: "realtime protocol", mutate: func(value *DesktopBuildTuple) { value.RealtimeProtocol = 0 }},
		{name: "manifest fingerprint", mutate: func(value *DesktopBuildTuple) {
			value.AttemptConfigurationManifestFingerprint = "sha256:" + strings.Repeat("A", 64)
		}},
		{name: "settings registry fingerprint", mutate: func(value *DesktopBuildTuple) {
			value.DesktopSettingsRegistryFingerprint = strings.Repeat("b", 64)
		}},
		{name: "matrix identity", mutate: func(value *DesktopBuildTuple) { value.CapabilityMatrixIdentity = "matrix/id" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid tuple")
			}
		})
	}
}

func TestDesktopReleaseValidationFollowsSemanticVersionTwo(t *testing.T) {
	t.Parallel()

	valid := []string{
		"0.0.0",
		"1.2.3",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0+desktop.001",
		"999999999999999999999999.2.3-rc.1+build-7",
	}
	for _, release := range valid {
		if !IsValidDesktopRelease(release) {
			t.Errorf("IsValidDesktopRelease(%q) = false", release)
		}
	}

	invalid := []string{
		"", "v1.2.3", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-alpha..1", "1.2.3-01", "1.2.3+", "1.2.3+build..1",
		"1.2.3+build+other", "1.2.3-α", " 1.2.3",
	}
	for _, release := range invalid {
		if IsValidDesktopRelease(release) {
			t.Errorf("IsValidDesktopRelease(%q) = true", release)
		}
	}
}

func TestDesktopReleaseComparisonUsesSemanticVersionPrecedence(t *testing.T) {
	t.Parallel()

	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"2.0.0",
		"10.0.0",
		"999999999999999999999999.0.0",
	}
	for index := 0; index < len(ordered)-1; index++ {
		comparison, err := CompareDesktopReleases(ordered[index], ordered[index+1])
		if err != nil {
			t.Fatal(err)
		}
		if comparison >= 0 {
			t.Errorf("CompareDesktopReleases(%q, %q) = %d, want < 0", ordered[index], ordered[index+1], comparison)
		}
	}
	comparison, err := CompareDesktopReleases("1.2.3+darwin.1", "1.2.3+linux.9")
	if err != nil {
		t.Fatal(err)
	}
	if comparison != 0 {
		t.Fatalf("build metadata changed precedence: %d", comparison)
	}
	if _, err := CompareDesktopReleases("1.2", "1.2.3"); err == nil {
		t.Fatal("CompareDesktopReleases() accepted an invalid release")
	}
}
