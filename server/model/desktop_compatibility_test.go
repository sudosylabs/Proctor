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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryFingerprintIsDistinctFromSHA256(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "registered format", value: "fnv1a64:35ec2f0722dc930e", valid: true},
		{name: "zero value", value: "fnv1a64:0000000000000000", valid: true},
		{name: "missing prefix", value: "35ec2f0722dc930e"},
		{name: "cryptographic identity", value: "sha256:" + strings.Repeat("a", 64)},
		{name: "wrong width", value: "fnv1a64:" + strings.Repeat("a", 64)},
		{name: "uppercase hex", value: "fnv1a64:35EC2F0722DC930E"},
		{name: "non hex", value: "fnv1a64:35ec2f0722dc930g"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.valid, IsValidRegistryFingerprint(test.value), "registry valid")
			if test.valid {
				require.False(t, IsValidSHA256Fingerprint(test.value), "registry selector was accepted as a cryptographic fingerprint")
			}
		})
	}
}

func TestInitialDesktopCompatibilityPolicyIsValid(t *testing.T) {
	t.Parallel()

	policy := NewInitialDesktopCompatibilityPolicy(NewInstitutionID(), time.UnixMilli(100))
	require.NoError(t, policy.Validate())
	require.Equal(t, int64(1), policy.Revision)
	require.Empty(t, policy.MinimumDesktopRelease)
	require.Empty(t, policy.RevokedDesktopBuildIDs)
	require.Empty(t, policy.AdministratorMessage)
	require.Equal(t, DesktopAvailabilityReady, policy.Availability)
	require.False(t, policy.RetryAt.Valid)
}

func TestDesktopCompatibilityPolicyReplacementCanonicalizesEmptyBuildSet(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0)
	policy := NewInitialDesktopCompatibilityPolicy(NewInstitutionID(), at)
	settings := DesktopCompatibilityPolicySettings{MinimumDesktopRelease: "1.2.0", Availability: DesktopAvailabilityReady}
	require.NoError(t, policy.Replace(1, settings, at.Add(time.Second)))
	require.NotNil(t, policy.RevokedDesktopBuildIDs, "empty revoked build set must remain an array for persistence and HTTP")
	require.Empty(t, policy.RevokedDesktopBuildIDs, "empty revoked build set must remain an array for persistence and HTTP")
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
			require.Error(t, candidate.Validate(), "Validate() accepted invalid settings")
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
	require.NoError(t, policy.Replace(1, settings, time.UnixMilli(200)))
	settings.RevokedDesktopBuildIDs[0] = "mutated"
	require.Equal(t, int64(2), policy.Revision)
	require.Equal(t, "build-1", policy.RevokedDesktopBuildIDs[0])
	require.Equal(t, DesktopAvailabilityMaintenance, policy.Availability)
	require.True(t, policy.RetryAt.Valid)
	require.Equal(t, ErrDesktopCompatibilityPolicyRevisionConflict, policy.Replace(1, policy.Settings(), time.UnixMilli(300)))
	staleRetry := settings
	staleRetry.RetryAt = OptionalTimeFrom(time.UnixMilli(300))
	require.Error(t, policy.Replace(policy.Revision, staleRetry, time.UnixMilli(300)), "Replace() accepted a retry time at the policy decision time")
	malformed := policy.Clone()
	malformed.RetryAt = OptionalTimeFrom(malformed.UpdatedAt)
	require.Error(t, malformed.Validate(), "Validate() accepted a nonfuture persisted retry time")
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
		DesktopSettingsRegistryFingerprint:      "fnv1a64:" + strings.Repeat("b", 16),
		DesktopTarget:                           "darwin-arm64", ConfigurationManifest: EmptyAttemptConfigurationManifest(),
		CapabilityMatrixIdentity: "matrix-1.2.3",
	}
	require.NoError(t, valid.Validate())

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
			require.Error(t, candidate.Validate(), "Validate() accepted invalid tuple")
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
		assert.True(t, IsValidDesktopRelease(release), "IsValidDesktopRelease")
	}

	invalid := []string{
		"", "v1.2.3", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-alpha..1", "1.2.3-01", "1.2.3+", "1.2.3+build..1",
		"1.2.3+build+other", "1.2.3-α", " 1.2.3",
	}
	for _, release := range invalid {
		assert.False(t, IsValidDesktopRelease(release), "IsValidDesktopRelease")
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
		require.NoError(t, err)
		assert.Negative(t, comparison, "CompareDesktopReleases")
	}
	comparison, err := CompareDesktopReleases("1.2.3+darwin.1", "1.2.3+linux.9")
	require.NoError(t, err)
	require.Equal(t, 0, comparison, "build metadata changed precedence")
	_, err = CompareDesktopReleases("1.2", "1.2.3")
	require.Error(t, err, "CompareDesktopReleases() accepted an invalid release")
}
