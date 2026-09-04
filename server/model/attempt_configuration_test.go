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
)

func TestAttemptConfigurationDigestExcludesAdmissionProvenance(t *testing.T) {
	t.Parallel()
	first := testAttemptConfiguration(t, NewUserSettingsRevision(), "sha256:"+strings.Repeat("a", 64))
	second := testAttemptConfiguration(t, NewUserSettingsRevision(), "sha256:"+strings.Repeat("b", 64))
	if first.Digest != second.Digest {
		t.Fatalf("semantic digest changed with admission provenance: %q != %q", first.Digest, second.Digest)
	}
	firstAdmission, _ := first.CanonicalAdmission()
	secondAdmission, _ := second.CanonicalAdmission()
	if string(firstAdmission) == string(secondAdmission) {
		t.Fatal("canonical admission omitted provenance")
	}
}

func TestAttemptConfigurationStrictCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	configuration := testAttemptConfiguration(t, NewUserSettingsRevision(), "sha256:"+strings.Repeat("a", 64))
	document, err := configuration.CanonicalAdmission()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAttemptConfiguration(document, configuration.Digest)
	if err != nil || decoded.Digest != configuration.Digest {
		t.Fatalf("DecodeAttemptConfiguration() = %#v, %v", decoded, err)
	}
	for name, malformed := range map[string][]byte{
		"unknown":   []byte(string(document[:len(document)-1]) + `,"unknown":true}`),
		"duplicate": []byte(strings.Replace(string(document), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)),
		"spacing":   append([]byte(" "), document...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, decodeErr := DecodeAttemptConfiguration(malformed, configuration.Digest); decodeErr == nil {
				t.Fatalf("accepted non-canonical document: %s", malformed)
			}
		})
	}
}

func TestAttemptConfigurationRejectsUnapprovedCommandBinding(t *testing.T) {
	t.Parallel()
	preferences := testAttemptConfigurationPreferences()
	preferences.CandidateCommandBindings = []AttemptCommandBinding{{CommandID: "editor.save", KeybindingID: "primary-save"}}
	if _, err := NewAttemptConfiguration(AttemptConfigurationSchemaVersion, CurrentAttemptConfigurationManifestFingerprint(),
		NewUserSettingsRevision(), "sha256:"+strings.Repeat("a", 64), preferences); err == nil {
		t.Fatal("accepted a command binding absent from the server-owned manifest")
	}
}

func testAttemptConfiguration(t *testing.T, revision UserSettingsRevision, registry string) AttemptConfiguration {
	t.Helper()
	configuration, err := NewAttemptConfiguration(AttemptConfigurationSchemaVersion,
		CurrentAttemptConfigurationManifestFingerprint(), revision, registry, testAttemptConfigurationPreferences())
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func testAttemptConfigurationPreferences() AttemptConfigurationPreferences {
	return AttemptConfigurationPreferences{ThemeMode: AttemptThemeFollowSystem, HighContrastMode: AttemptModeAuto,
		UIZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPercent: 150,
		ReducedMotionMode: AttemptModeAuto, ScreenReaderMode: AttemptModeAuto,
		AnnouncementDetail: AttemptAnnouncementStandard, CursorStyle: AttemptCursorLine,
		CursorBlinking: AttemptCursorBlink, CandidateCommandBindings: []AttemptCommandBinding{}}
}
