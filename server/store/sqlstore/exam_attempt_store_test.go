// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"testing"
	"time"
)

func TestExamAttemptConnectOutcomeIsBoundedAndCredentialFree(t *testing.T) {
	outcome := examAttemptConnectOutcomeV1{
		AttemptID: "01J00000000000000000000001", WorkspaceID: "01J00000000000000000000002",
		ParticipationID: "01J00000000000000000000003", ConnectionID: "01J00000000000000000000004",
		ExamID: "01J00000000000000000000005", SittingID: "01J00000000000000000000006",
		CandidateID: "01J00000000000000000000007", RevisionID: "01J00000000000000000000008",
		SessionID: "01J00000000000000000000009", ClassID: "01J0000000000000000000000A",
		StartedAt:      time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
		LeaseExpiresAt: time.Date(2026, time.August, 15, 12, 0, 20, 0, time.UTC), EntryCount: 1000,
		Generation: 1, FirstAdmission: true, ConnectionOpened: true,
	}
	encoded, err := encodeCommandOutcome(outcome)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > examAttemptConnectOutcomeMaximumBytes {
		t.Fatalf("connect outcome size = %d, maximum = %d", len(encoded), examAttemptConnectOutcomeMaximumBytes)
	}
	credentialHash := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if bytes.Contains(encoded, credentialHash) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatalf("connect outcome exposed credential material: %s", encoded)
	}
}

func TestExistingAttemptConfigurationRetainsOriginalProvenanceOnPatchedBuild(t *testing.T) {
	t.Parallel()
	manifest := model.EmptyAttemptConfigurationManifest()
	candidate := model.AttemptConfigurationCandidate{ManifestFingerprint: manifest.Fingerprint(), RegistryFingerprint: "fnv1a64:aaaaaaaaaaaaaaaa",
		DesktopBuild: "original-build", DesktopTarget: "original-target", UserSettingsRevision: model.NewUserSettingsRevision(),
		Presentation: model.AttemptConfigurationPresentation{ColorTheme: "hcDark", ZoomPercent: 100, EditorFontSizePX: 24, EditorLineHeightPX: 16,
			ScreenReaderMode: "on", AnnouncementMode: "minimal", CursorStyle: "underline", CursorBlinking: "phase"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}
	frozen, err := candidate.Freeze(model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := frozen.CanonicalAdmission()
	if err != nil {
		t.Fatal(err)
	}
	input := &store.ExamAttemptConnect{ConfigurationManifestFingerprint: manifest.Fingerprint(), InitialConfiguration: &candidate,
		DesktopBuild: model.DesktopBuildTuple{DesktopRelease: "1.0.1", DesktopBuildID: "patched-build", DesktopTarget: "patched-target",
			Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1,
			ConfigurationManifest: manifest, AttemptConfigurationManifestFingerprint: manifest.Fingerprint(),
			DesktopSettingsRegistryFingerprint: "fnv1a64:bbbbbbbbbbbbbbbb", CapabilityMatrixIdentity: "patched-matrix"}}
	if input.DesktopBuild.Validate() != nil {
		t.Fatal("invalid test build")
	}
	for _, proposal := range []*model.AttemptConfigurationCandidate{nil, &candidate} {
		input.InitialConfiguration = proposal
		retained, err := validateExistingAttemptConfiguration(canonical, frozen.Digest, input)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := retained.CanonicalAdmission()
		if !bytes.Equal(actual, canonical) {
			t.Fatal("rejoin rewrote original provenance")
		}
	}
	changed := candidate.Clone()
	changed.Presentation.ColorTheme = "light"
	input.InitialConfiguration = &changed
	if _, err := validateExistingAttemptConfiguration(canonical, frozen.Digest, input); err == nil {
		t.Fatal("rejoin changed the frozen candidate")
	}
	input.InitialConfiguration = nil
	input.ConfigurationManifestFingerprint = model.SHA256Fingerprint([]byte("unknown"))
	if _, err := validateExistingAttemptConfiguration(canonical, frozen.Digest, input); err == nil {
		t.Fatal("unknown manifest reproduced a frozen configuration")
	}
}
