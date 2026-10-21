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

	"github.com/stretchr/testify/require"
)

func configurationCandidate() AttemptConfigurationCandidate {
	return AttemptConfigurationCandidate{ManifestFingerprint: CurrentAttemptConfigurationManifestFingerprint(),
		RegistryFingerprint: "fnv1a64:" + strings.Repeat("a", 16), DesktopBuild: "desktop-test", DesktopTarget: "darwin-arm64",
		UserSettingsRevision: NewUserSettingsRevision(), Presentation: AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100,
			EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"},
		ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}
}

func TestAttemptConfigurationDigestCoversCompleteCandidate(t *testing.T) {
	t.Parallel()
	original := configurationCandidate()
	first, err := original.Freeze(NewID())
	require.NoError(t, err)
	for name, mutate := range map[string]func(*AttemptConfigurationCandidate){
		"source revision": func(c *AttemptConfigurationCandidate) { c.UserSettingsRevision = NewUserSettingsRevision() },
		"registry":        func(c *AttemptConfigurationCandidate) { c.RegistryFingerprint = "fnv1a64:" + strings.Repeat("b", 16) },
		"build":           func(c *AttemptConfigurationCandidate) { c.DesktopBuild = "patched" },
		"target":          func(c *AttemptConfigurationCandidate) { c.DesktopTarget = "win32-x64" },
		"manifest":        func(c *AttemptConfigurationCandidate) { c.ManifestFingerprint = SHA256Fingerprint([]byte("different")) },
		"presentation":    func(c *AttemptConfigurationCandidate) { c.Presentation.ColorTheme = "hcLight" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := original.Clone()
			mutate(&candidate)
			other, freezeErr := candidate.Freeze(NewID())
			require.NoError(t, freezeErr)
			require.NotEqual(t, first.Digest, other.Digest)
		})
	}
	second, err := original.Freeze(NewID())
	require.NoError(t, err, "server revision changed the candidate digest")
	require.NotEqual(t, first.Revision, second.Revision, "server revision changed the candidate digest")
	require.Equal(t, first.Digest, second.Digest, "server revision changed the candidate digest")
	canonical, _ := original.CanonicalAdmission()
	require.Equal(t, SHA256Fingerprint(canonical), first.Digest, "digest did not hash the complete candidate")
}

func TestAttemptConfigurationStrictCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	candidate := configurationCandidate()
	frozen, err := candidate.Freeze(NewID())
	require.NoError(t, err)
	document, err := frozen.CanonicalAdmission()
	require.NoError(t, err)
	decoded, err := DecodeAttemptConfiguration(document, frozen.Digest)
	require.NoError(t, err, "frozen round trip")
	require.Equal(t, frozen.Revision, decoded.Revision, "frozen round trip")
	candidateDocument, _ := candidate.CanonicalAdmission()
	for name, document := range map[string][]byte{
		"unknown":             bytes.Replace(candidateDocument, []byte(`"zoom_percent":100`), []byte(`"zoom_percent":100,"unknown":true`), 1),
		"duplicate":           bytes.Replace(candidateDocument, []byte(`"zoom_percent":100`), []byte(`"zoom_percent":100,"zoom_percent":100`), 1),
		"omitted boolean":     bytes.Replace(candidateDocument, []byte(`"prefer_high_contrast":false,`), nil, 1),
		"null boolean":        bytes.Replace(candidateDocument, []byte(`"prefer_high_contrast":false`), []byte(`"prefer_high_contrast":null`), 1),
		"case alias":          bytes.Replace(candidateDocument, []byte(`"prefer_high_contrast"`), []byte(`"PREFER_HIGH_CONTRAST"`), 1),
		"exponent":            bytes.Replace(candidateDocument, []byte(`"zoom_percent":100`), []byte(`"zoom_percent":1e2`), 1),
		"fraction":            bytes.Replace(candidateDocument, []byte(`"zoom_percent":100`), []byte(`"zoom_percent":100.0`), 1),
		"frozen in candidate": document,
		"null commands":       bytes.Replace(candidateDocument, []byte(`"approved_commands":[]`), []byte(`"approved_commands":null`), 1),
		"old format":          []byte(`{"schema_version":1,"preferences":{}}`),
	} {
		t.Run(name, func(t *testing.T) {
			var value AttemptConfigurationCandidate
			require.Error(t, json.Unmarshal(document, &value), "accepted invalid candidate")
		})
	}
	for name, bad := range map[string][]byte{"spacing": append([]byte(" "), document...), "candidate in storage": candidateDocument} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAttemptConfiguration(bad, frozen.Digest)
			require.Error(t, err, "accepted invalid frozen storage")
		})
	}
}

func TestAttemptConfigurationPresentationBounds(t *testing.T) {
	t.Parallel()
	for _, font := range []int{12, 24} {
		for _, line := range []int{16, 40} {
			c := configurationCandidate()
			c.Presentation.EditorFontSizePX = font
			c.Presentation.EditorLineHeightPX = line
			require.NoError(t, c.Validate(), "independent pixel bounds rejected")
		}
	}
	for name, mutate := range map[string]func(*AttemptConfigurationCandidate){
		"font low":         func(c *AttemptConfigurationCandidate) { c.Presentation.EditorFontSizePX = 11 },
		"font high":        func(c *AttemptConfigurationCandidate) { c.Presentation.EditorFontSizePX = 25 },
		"line low":         func(c *AttemptConfigurationCandidate) { c.Presentation.EditorLineHeightPX = 15 },
		"line high":        func(c *AttemptConfigurationCandidate) { c.Presentation.EditorLineHeightPX = 41 },
		"zoom low":         func(c *AttemptConfigurationCandidate) { c.Presentation.ZoomPercent = 79 },
		"zoom high":        func(c *AttemptConfigurationCandidate) { c.Presentation.ZoomPercent = 201 },
		"old theme":        func(c *AttemptConfigurationCandidate) { c.Presentation.ColorTheme = "follow_system" },
		"old announcement": func(c *AttemptConfigurationCandidate) { c.Presentation.AnnouncementMode = "standard" },
		"duplicate ID":     func(c *AttemptConfigurationCandidate) { c.ApprovedCommands = []string{"a", "a"} },
		"unsorted IDs":     func(c *AttemptConfigurationCandidate) { c.ApprovedCommands = []string{"z", "a"} },
		"key chord":        func(c *AttemptConfigurationCandidate) { c.ApprovedKeybindings = []string{"Ctrl+S"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := configurationCandidate()
			mutate(&c)
			require.Error(t, c.Validate(), "accepted invalid configuration")
		})
	}
	for _, theme := range []string{"light", "dark", "hcLight", "hcDark"} {
		for _, blink := range []string{"blink", "smooth", "phase", "expand", "solid"} {
			c := configurationCandidate()
			c.Presentation.ColorTheme = theme
			c.Presentation.CursorBlinking = blink
			_, err := c.Freeze(NewID())
			require.NoError(t, err)
		}
	}
}

func TestAttemptConfigurationManifestCatalogAndProvenance(t *testing.T) {
	t.Parallel()
	commands := []string{"editor.save"}
	bindings := []AttemptCommandBinding{{CommandID: "editor.save", KeybindingID: "primary-save"}}
	manifest, err := NewAttemptConfigurationManifest(commands, bindings)
	require.NoError(t, err)
	commands[0] = "malicious"
	bindings[0].CommandID = "malicious"
	canonical := manifest.Canonical()
	canonical[0] = 'x'
	require.NoError(t, manifest.ValidateSelection([]string{"editor.save"}, []string{"primary-save"}), "manifest mutated")
	require.Equal(t, SHA256Fingerprint(manifest.Canonical()), manifest.Fingerprint(), "manifest mutated")
	for name, selection := range map[string][2][]string{
		"unknown command": {[]string{"unknown"}, []string{}}, "unknown binding": {[]string{"editor.save"}, []string{"unknown"}},
		"missing command": {[]string{}, []string{"primary-save"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, manifest.ValidateSelection(selection[0], selection[1]), "accepted unapproved selection")
		})
	}
	c := configurationCandidate()
	c.ManifestFingerprint = manifest.Fingerprint()
	c.ApprovedCommands = []string{"editor.save"}
	c.ApprovedKeybindings = []string{"primary-save"}
	build := DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: c.DesktopBuild, Platform: DesktopPlatformDarwin, Architecture: DesktopArchitectureARM64, RealtimeProtocol: 1,
		AttemptConfigurationManifestFingerprint: manifest.Fingerprint(), DesktopSettingsRegistryFingerprint: c.RegistryFingerprint, CapabilityMatrixIdentity: "test-matrix", DesktopTarget: c.DesktopTarget, ConfigurationManifest: manifest}
	require.NoError(t, c.ValidateForBuild(build), "approved candidate rejected")
	build.DesktopBuildID = "foreign"
	require.Error(t, c.ValidateForBuild(build), "new freeze accepted foreign build")
	frozen, err := c.Freeze(NewID())
	require.NoError(t, err)
	c.ApprovedCommands[0] = "changed"
	require.Equal(t, "editor.save", frozen.ApprovedCommands[0], "freeze aliased caller")
	require.Error(t, EmptyAttemptConfigurationManifest().ValidateSelection(frozen.ApprovedCommands, frozen.ApprovedKeybindings), "empty catalog admitted arbitrary IDs")
}
