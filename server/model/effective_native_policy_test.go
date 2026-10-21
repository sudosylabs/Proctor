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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Synthetic certification exercises resolution only, never a production claim.
func nativeResolutionFixture(t *testing.T) NativePolicyResolution {
	t.Helper()
	schema := SHA256Fingerprint([]byte("synthetic schema"))
	definitions := []NativeCoverageDefinition{}
	add := func(key, capability string, source NativeSourceID, baseline bool, claims ...NativeCapabilityClaim) {
		definitions = append(definitions, NativeCoverageDefinition{CoverageKey: key, CapabilityID: capability, SourceID: source, SourceSchemaDigest: schema, PermittedClaims: claims, RequiredPermissions: []string{}, Baseline: baseline})
	}
	add("baseline.capture", "baseline", NativeSourceCapture, true, NativeClaimEnforce)
	add("baseline.display", "baseline", NativeSourceDisplay, true, NativeClaimEnforce)
	add("baseline.window", "baseline", NativeSourceWindow, true, NativeClaimEnforce)
	add("native.candidate_private_clipboard.v1", "clipboard", "", false, NativeClaimEnforce)
	add("native.os_clipboard_change.v1", "clipboard", NativeSourceClipboard, false, NativeClaimObserve)
	add("native.route_change.v1", "network_configuration", NativeSourceNetwork, false, NativeClaimObserve)
	add("native.single_interactive_session.v1", "interactive_session", NativeSourceSession, false, NativeClaimObserve, NativeClaimEnforce)
	matrix := NativeCapabilityMatrix{RegistryDigest: NativeRegistryDigest, MatrixID: "synthetic-matrix", ReleaseID: "synthetic-release", TargetTuple: "synthetic-darwin-arm64"}
	for _, definition := range definitions {
		claim, verification := definition.PermittedClaims[0], "passed"
		if definition.CoverageKey == "native.os_clipboard_change.v1" {
			claim = NativeClaimUnavailable
			verification = "unavailable"
		}
		matrix.Entries = append(matrix.Entries, NativeCapabilityMatrixEntry{CoverageKey: definition.CoverageKey, SourceSchemaDigest: schema, Claim: claim, ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: verification})
	}
	agreement, err := NewDesktopNativeAgreement(NativeRegistryDigest, SHA256Fingerprint([]byte("manifest")), SHA256Fingerprint([]byte("matrix")), SHA256Fingerprint([]byte("detectors")), definitions, matrix, []NativeDetectorDefinition{})
	require.NoError(t, err)
	manifest := EmptyAttemptConfigurationManifest()
	at := time.Date(2026, 9, 8, 8, 0, 0, 123000000, time.UTC)
	return NativePolicyResolution{PolicyID: "synthetic-policy", Revision: "one", Ordinal: 1, InstitutionID: NewInstitutionID(), ExamRevisionID: NewExamRevisionID(), SittingID: NewExamSittingID(), Scope: SecurityPolicyScope{Kind: "admission", AdmissionScopeID: "synthetic-admission"}, IssuedAt: at, ActiveFrom: at, ActivationTime: at, Selections: DefaultExamPolicySet(), Build: DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: "synthetic-build", Platform: DesktopPlatformDarwin, Architecture: DesktopArchitectureARM64, RealtimeProtocol: 1, AttemptConfigurationManifestFingerprint: manifest.Fingerprint(), DesktopSettingsRegistryFingerprint: "fnv1a64:aaaaaaaaaaaaaaaa", CapabilityMatrixIdentity: matrix.MatrixID, ConfigurationManifest: manifest, DesktopTarget: matrix.TargetTuple, NativeAgreement: agreement}}
}
func setNativeFamily(t *testing.T, input *NativePolicyResolution, index int, raw string) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(raw), &input.Selections.Native.Families[index]))
}
func TestEffectiveNativePolicyContentDigestSurvivesOnlyScopeRebind(t *testing.T) {
	input := nativeResolutionFixture(t)
	resolved, err := ResolveNativePolicy(input)
	require.NoError(t, err)
	require.True(t, slices.Equal(resolved.Sources, []NativeSourceID{NativeSourceDisplay, NativeSourceWindow, NativeSourceCapture}), "baseline depends on optional selections")
	require.Len(t, resolved.Requirements, 3, "baseline depends on optional selections")
	rebound, err := resolved.Policy.RebindAttempt(NewExamAttemptID(), input.ActivationTime)
	require.NoError(t, err)
	digest, err := rebound.ContentDigest(resolved.CatalogBindings, resolved.CapabilityMatrixDigest)
	require.NoError(t, err, "scope rebinding changed content or retained full identity")
	require.Equal(t, resolved.PolicyContentDigest, digest, "scope rebinding changed content or retained full identity")
	require.NotEqual(t, resolved.Policy.Digest, rebound.Digest, "scope rebinding changed content or retained full identity")
	raw, err := encodeCanonicalExamDocument(rebound)
	require.NoError(t, err)
	require.False(t, bytes.Contains(raw, []byte("expires_at")), "policy carries an independent expiry")
	var decoded EffectiveExamSecurityPolicy
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NoError(t, decoded.Validate(input.ActivationTime.Add(48*time.Hour)), "provenance was incorrectly treated as live authority expiry")
	for _, field := range []string{"matrix_id", "evidence_class_id", "revision"} {
		changed := rebound
		document, _ := changed.document()
		document[field] = json.RawMessage(`"changed"`)
		raw, _ := encodeCanonicalExamDocument(document)
		require.Error(t, json.Unmarshal(raw, &decoded), "unbound %s", field)
	}
	changed := resolved.Policy
	changed.Revision = "two"
	require.NoError(t, changed.seal(input.ActivationTime), "seal")
	digest, _ = changed.ContentDigest(resolved.CatalogBindings, resolved.CapabilityMatrixDigest)
	require.NotEqual(t, resolved.PolicyContentDigest, digest, "revision omitted from content identity")
}
func TestNativePolicyResolutionTruthfulObservationAndEffects(t *testing.T) {
	input := nativeResolutionFixture(t)
	setNativeFamily(t, &input, 2, `{"id":"clipboard","mode":"enforce","strategy":"private_attempt","observe_os_clipboard_changes":true}`)
	setNativeFamily(t, &input, 6, `{"id":"network_configuration","mode":"enforce","enabled_sub_capabilities":["route_change"],"allowed_managed_profile_ids":[],"allowed_adapter_class_ids":[],"allowed_tunnel_class_ids":[]}`)
	resolved, err := ResolveNativePolicy(input)
	require.NoError(t, err)
	require.NotContains(t, resolved.Sources, NativeSourceClipboard, "unavailable observation was opened or route observation omitted")
	require.Contains(t, resolved.Sources, NativeSourceNetwork, "unavailable observation was opened or route observation omitted")
	foundEffect, foundUnavailable, foundRoute := false, false, false
	for _, r := range resolved.Requirements {
		switch r.CoverageKey {
		case "native.candidate_private_clipboard.v1":
			foundEffect = r.SourceID == "" && r.RequiredClaim == NativeClaimEnforce
		case "native.os_clipboard_change.v1":
			foundUnavailable = r.Entry.Claim == NativeClaimUnavailable
		case "native.route_change.v1":
			foundRoute = r.RequiredClaim == NativeClaimObserve
		}
	}
	require.True(t, foundEffect, "lost effect, unavailable claim or route observe rule")
	require.True(t, foundUnavailable, "lost effect, unavailable claim or route observe rule")
	require.True(t, foundRoute, "lost effect, unavailable claim or route observe rule")
	setNativeFamily(t, &input, 1, `{"id":"interactive_session","mode":"enforce"}`)
	_, err = ResolveNativePolicy(input)
	require.ErrorIs(t, err, ErrNativeCoverageUnsupported, "observe certification granted enforcement")
}
func TestNativePolicyResolutionRejectsUnknownCatalogsAndBadProvenance(t *testing.T) {
	input := nativeResolutionFixture(t)
	setNativeFamily(t, &input, 0, `{"id":"process_application","mode":"observe","allowed_application_ids":["application-id"]}`)
	_, err := ResolveNativePolicy(input)
	require.ErrorIs(t, err, ErrNativeCatalogUnavailable, "invented catalog ownership")
	for _, mutate := range []func(*NativePolicyResolution){func(i *NativePolicyResolution) { i.Build.NativeAgreement = nil }, func(i *NativePolicyResolution) { i.ActiveFrom = i.ActivationTime.Add(time.Millisecond) }, func(i *NativePolicyResolution) { i.IssuedAt = i.IssuedAt.Add(time.Nanosecond) }, func(i *NativePolicyResolution) { i.Scope.AttemptID = NewExamAttemptID() }, func(i *NativePolicyResolution) { i.Build.CapabilityMatrixIdentity = "other" }} {
		input = nativeResolutionFixture(t)
		mutate(&input)
		_, err := ResolveNativePolicy(input)
		require.Error(t, err, "invalid resolution accepted")
	}
}
func TestEffectivePolicyRejectsRemovedAndUnknownMembers(t *testing.T) {
	input := nativeResolutionFixture(t)
	resolved, err := ResolveNativePolicy(input)
	require.NoError(t, err)
	raw, err := encodeCanonicalExamDocument(resolved.Policy)
	require.NoError(t, err)
	for _, document := range []string{strings.Replace(string(raw), `"scope":`, `"expires_at":"2026-09-09T00:00:00Z","scope":`, 1), strings.Replace(string(raw), `"scope":`, `"scope":{},"scope":`, 1), strings.Replace(string(raw), `"scope":`, `"SCOPE":`, 1), strings.Replace(string(raw), `"resolved_exception_refs":[]`, `"resolved_exception_refs":["exception"]`, 1)} {
		var value EffectiveExamSecurityPolicy
		require.Error(t, json.Unmarshal([]byte(document), &value), "invalid policy document accepted")
	}
}
