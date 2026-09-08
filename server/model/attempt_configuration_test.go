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
	first, err := original.Freeze(NewId())
	if err != nil {
		t.Fatal(err)
	}
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
			other, err := candidate.Freeze(NewId())
			if err != nil || other.Digest == first.Digest {
				t.Fatalf("provenance did not affect digest: %v", err)
			}
		})
	}
	second, err := original.Freeze(NewId())
	if err != nil || second.Revision == first.Revision || second.Digest != first.Digest {
		t.Fatal("server revision changed the candidate digest", err)
	}
	canonical, _ := original.CanonicalAdmission()
	if first.Digest != SHA256Fingerprint(canonical) {
		t.Fatal("digest did not hash the complete candidate")
	}
}

func TestAttemptConfigurationStrictCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	candidate := configurationCandidate()
	frozen, err := candidate.Freeze(NewId())
	if err != nil {
		t.Fatal(err)
	}
	document, err := frozen.CanonicalAdmission()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAttemptConfiguration(document, frozen.Digest)
	if err != nil || decoded.Revision != frozen.Revision {
		t.Fatal("frozen round trip", err)
	}
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
			if json.Unmarshal(document, &value) == nil {
				t.Fatal("accepted invalid candidate")
			}
		})
	}
	for name, bad := range map[string][]byte{"spacing": append([]byte(" "), document...), "candidate in storage": candidateDocument} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAttemptConfiguration(bad, frozen.Digest); err == nil {
				t.Fatal("accepted invalid frozen storage")
			}
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
			if c.Validate() != nil {
				t.Fatal("independent pixel bounds rejected")
			}
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
			if c.Validate() == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	for _, theme := range []string{"light", "dark", "hcLight", "hcDark"} {
		for _, blink := range []string{"blink", "smooth", "phase", "expand", "solid"} {
			c := configurationCandidate()
			c.Presentation.ColorTheme = theme
			c.Presentation.CursorBlinking = blink
			if _, err := c.Freeze(NewId()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestAttemptConfigurationManifestCatalogAndProvenance(t *testing.T) {
	t.Parallel()
	commands := []string{"editor.save"}
	bindings := []AttemptCommandBinding{{CommandID: "editor.save", KeybindingID: "primary-save"}}
	manifest, err := NewAttemptConfigurationManifest(commands, bindings)
	if err != nil {
		t.Fatal(err)
	}
	commands[0] = "malicious"
	bindings[0].CommandID = "malicious"
	canonical := manifest.Canonical()
	canonical[0] = 'x'
	if manifest.ValidateSelection([]string{"editor.save"}, []string{"primary-save"}) != nil || manifest.Fingerprint() != SHA256Fingerprint(manifest.Canonical()) {
		t.Fatal("manifest mutated")
	}
	for name, selection := range map[string][2][]string{
		"unknown command": {[]string{"unknown"}, []string{}}, "unknown binding": {[]string{"editor.save"}, []string{"unknown"}},
		"missing command": {[]string{}, []string{"primary-save"}},
	} {
		t.Run(name, func(t *testing.T) {
			if manifest.ValidateSelection(selection[0], selection[1]) == nil {
				t.Fatal("accepted unapproved selection")
			}
		})
	}
	c := configurationCandidate()
	c.ManifestFingerprint = manifest.Fingerprint()
	c.ApprovedCommands = []string{"editor.save"}
	c.ApprovedKeybindings = []string{"primary-save"}
	build := DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: c.DesktopBuild, Platform: DesktopPlatformDarwin, Architecture: DesktopArchitectureARM64, RealtimeProtocol: 1,
		AttemptConfigurationManifestFingerprint: manifest.Fingerprint(), DesktopSettingsRegistryFingerprint: c.RegistryFingerprint, CapabilityMatrixIdentity: "test-matrix", DesktopTarget: c.DesktopTarget, ConfigurationManifest: manifest}
	if c.ValidateForBuild(build) != nil {
		t.Fatal("approved candidate rejected")
	}
	build.DesktopBuildID = "foreign"
	if c.ValidateForBuild(build) == nil {
		t.Fatal("new freeze accepted foreign build")
	}
	frozen, err := c.Freeze(NewId())
	if err != nil {
		t.Fatal(err)
	}
	c.ApprovedCommands[0] = "changed"
	if frozen.ApprovedCommands[0] != "editor.save" {
		t.Fatal("freeze aliased caller")
	}
	if EmptyAttemptConfigurationManifest().ValidateSelection(frozen.ApprovedCommands, frozen.ApprovedKeybindings) == nil {
		t.Fatal("empty catalog admitted arbitrary IDs")
	}
}
