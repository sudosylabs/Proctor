// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAttemptConnectOutcomeIsBoundedAndCredentialFree(t *testing.T) {
	outcome := examAttemptConnectOutcome{
		AttemptID: "01J00000000000000000000001", WorkspaceID: "01J00000000000000000000002",
		ParticipationID: "01J00000000000000000000003", ConnectionID: "01J00000000000000000000004",
		ExamID: "01J00000000000000000000005", SittingID: "01J00000000000000000000006",
		CandidateID: "01J00000000000000000000007", RevisionID: "01J00000000000000000000008",
		SessionID: "01J00000000000000000000009", ClassID: "01J0000000000000000000000A",
		StartedAt:      time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
		LeaseExpiresAt: time.Date(2026, time.August, 15, 12, 0, 20, 0, time.UTC), EntryCount: 1000,
		Generation: 1, FirstAdmission: true, ConnectionOpened: true,
	}
	encoded, err := encodeOutcome(outcome)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), examAttemptConnectOutcomeMaximumBytes, "connect outcome size")
	credentialHash := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.False(t, bytes.Contains(encoded, credentialHash))
	require.False(t, bytes.Contains(encoded, []byte("credential")))
}

func TestExistingAttemptConfigurationRetainsOriginalProvenanceOnPatchedBuild(t *testing.T) {
	t.Parallel()
	manifest := model.EmptyAttemptConfigurationManifest()
	candidate := model.AttemptConfigurationCandidate{ManifestFingerprint: manifest.Fingerprint(), RegistryFingerprint: "fnv1a64:aaaaaaaaaaaaaaaa",
		DesktopBuild: "original-build", DesktopTarget: "original-target", UserSettingsRevision: model.NewUserSettingsRevision(),
		Presentation: model.AttemptConfigurationPresentation{ColorTheme: "hcDark", ZoomPercent: 100, EditorFontSizePX: 24, EditorLineHeightPX: 16,
			ScreenReaderMode: "on", AnnouncementMode: "minimal", CursorStyle: "underline", CursorBlinking: "phase"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}
	frozen, err := candidate.Freeze(model.NewID())
	require.NoError(t, err)
	canonical, err := frozen.CanonicalAdmission()
	require.NoError(t, err)
	input := &store.ExamAttemptConnect{ConfigurationManifestFingerprint: manifest.Fingerprint(), InitialConfiguration: &candidate,
		DesktopBuild: model.DesktopBuildTuple{DesktopRelease: "1.0.1", DesktopBuildID: "patched-build", DesktopTarget: "patched-target",
			Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1,
			ConfigurationManifest: manifest, AttemptConfigurationManifestFingerprint: manifest.Fingerprint(),
			DesktopSettingsRegistryFingerprint: "fnv1a64:bbbbbbbbbbbbbbbb", CapabilityMatrixIdentity: "patched-matrix"}}
	require.NoError(t, input.DesktopBuild.Validate(), "invalid test build")
	for _, proposal := range []*model.AttemptConfigurationCandidate{nil, &candidate} {
		input.InitialConfiguration = proposal
		retained, err := validateExistingAttemptConfiguration(canonical, frozen.Digest, input)
		require.NoError(t, err)
		actual, _ := retained.CanonicalAdmission()
		require.True(t, bytes.Equal(actual, canonical), "rejoin rewrote original provenance")
	}
	changed := candidate.Clone()
	changed.Presentation.ColorTheme = "light"
	input.InitialConfiguration = &changed
	_, err = validateExistingAttemptConfiguration(canonical, frozen.Digest, input)
	require.Error(t, err, "rejoin changed the frozen candidate")
	input.InitialConfiguration = nil
	input.ConfigurationManifestFingerprint = model.SHA256Fingerprint([]byte("unknown"))
	_, err = validateExistingAttemptConfiguration(canonical, frozen.Digest, input)
	require.Error(t, err, "unknown manifest reproduced a frozen configuration")
}
