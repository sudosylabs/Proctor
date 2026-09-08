// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package desktoprelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
)

type releaseFixture struct {
	expected  Expectation
	artifacts Artifacts
	keys      map[string]ed25519.PublicKey
	private   ed25519.PrivateKey
	matrix    model.NativeCapabilityMatrix
}

func fixture(t *testing.T) releaseFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := model.EmptyAttemptConfigurationManifest()
	f := releaseFixture{private: private, keys: map[string]ed25519.PublicKey{"synthetic-test-only": public}}
	f.expected = Expectation{Build: model.DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: "synthetic-test-build",
		DesktopTarget: "synthetic-darwin-arm64", Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1,
		DesktopSettingsRegistryFingerprint: "fnv1a64:aaaaaaaaaaaaaaaa", ConfigurationManifest: manifest,
		AttemptConfigurationManifestFingerprint: manifest.Fingerprint(), CapabilityMatrixIdentity: "synthetic-test-matrix"}, ApplicationReleaseID: "synthetic-release",
		SourceSchemaDigests: map[model.NativeSourceID]string{}}
	f.artifacts = Artifacts{Registry: []byte(`{"synthetic_test_registry":true}`), SourceManifest: canonical(t, model.NativeSources()),
		Schemas: map[string][]byte{}, ConfigurationManifest: manifest.Canonical(), Matrix: SignedMatrix{KeyID: "synthetic-test-only"}}
	for _, source := range model.NativeSources() {
		schema := canonical(t, map[string]string{"synthetic_source": string(source)})
		digest := model.SHA256Fingerprint(schema)
		f.artifacts.Schemas[digest] = schema
		f.expected.SourceSchemaDigests[source] = digest
	}
	definitions := []struct {
		key      string
		source   model.NativeSourceID
		baseline bool
		claims   []model.NativeCapabilityClaim
	}{
		{"baseline.capture", model.NativeSourceCapture, true, []model.NativeCapabilityClaim{model.NativeClaimEnforce}},
		{"baseline.display", model.NativeSourceDisplay, true, []model.NativeCapabilityClaim{model.NativeClaimEnforce}},
		{"baseline.window", model.NativeSourceWindow, true, []model.NativeCapabilityClaim{model.NativeClaimEnforce}},
		{"native.effect", "", false, []model.NativeCapabilityClaim{model.NativeClaimEnforce}},
		{"native.observe", model.NativeSourceClipboard, false, []model.NativeCapabilityClaim{model.NativeClaimObserve}},
	}
	effectSchema := []byte(`{"synthetic_effect_schema":true}`)
	effectDigest := model.SHA256Fingerprint(effectSchema)
	f.artifacts.Schemas[effectDigest] = effectSchema
	f.expected.RegistryDigest = model.SHA256Fingerprint(f.artifacts.Registry)
	f.expected.SourceManifestDigest = model.SHA256Fingerprint(f.artifacts.SourceManifest)
	f.matrix = model.NativeCapabilityMatrix{RegistryDigest: f.expected.RegistryDigest, MatrixID: f.expected.Build.CapabilityMatrixIdentity, ReleaseID: f.expected.ApplicationReleaseID, TargetTuple: f.expected.Build.TargetTuple()}
	for _, d := range definitions {
		digest := f.expected.SourceSchemaDigests[d.source]
		if d.source == "" {
			digest = effectDigest
		}
		f.expected.Coverage = append(f.expected.Coverage, model.NativeCoverageDefinition{CoverageKey: d.key, CapabilityID: "clipboard", SourceID: d.source, SourceSchemaDigest: digest, PermittedClaims: d.claims, RequiredPermissions: []string{}, Baseline: d.baseline})
		claim, verification := model.NativeClaimEnforce, "passed"
		if d.key == "native.observe" {
			claim, verification = model.NativeClaimUnavailable, "unavailable"
		}
		f.matrix.Entries = append(f.matrix.Entries, model.NativeCapabilityMatrixEntry{CoverageKey: d.key, SourceSchemaDigest: digest, Claim: claim,
			ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: verification})
	}
	detectors := []model.NativeDetectorDefinition{{DetectorID: "synthetic-detector", Version: 1, CapabilityID: "clipboard", ConditionIDs: []string{"clipboard-changed"}, SourceIDs: []model.NativeSourceID{model.NativeSourceClipboard}, AllowedModes: []model.NativeCapabilityClaim{model.NativeClaimObserve}}}
	f.artifacts.DetectorCatalog = canonical(t, detectors)
	f.expected.DetectorCatalogDigest = model.SHA256Fingerprint(f.artifacts.DetectorCatalog)
	f.sign(t)
	return f
}
func canonical(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := canonicaljson.Canonicalize(raw, ArtifactMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *releaseFixture) sign(t *testing.T) {
	t.Helper()
	f.artifacts.Matrix.Payload = canonical(t, f.matrix)
	f.signPayload()
}
func (f *releaseFixture) signPayload() {
	f.artifacts.Matrix.Signature = ed25519.Sign(f.private, f.artifacts.Matrix.Payload)
	f.expected.MatrixDigest = model.SHA256Fingerprint(f.artifacts.Matrix.Payload)
}

func TestVerifyExactReleaseAndTruthfulCoverage(t *testing.T) {
	t.Parallel()
	f := fixture(t)
	build, err := Verify(f.expected, f.artifacts, f.keys)
	if err != nil {
		t.Fatal(err)
	}
	agreement := build.NativeAgreement
	observed, err := agreement.Coverage("native.observe", model.NativeClaimObserve)
	if err != nil || observed.Claim != model.NativeClaimUnavailable {
		t.Fatal("observe unavailability was hidden", err)
	}
	if _, err := agreement.Coverage("native.observe", model.NativeClaimEnforce); err == nil {
		t.Fatal("unavailable observe source granted enforce")
	}
	if _, err := agreement.Coverage("native.effect", model.NativeClaimEnforce); err != nil {
		t.Fatal("certified effect-only claim rejected", err)
	}
	if _, err := agreement.Coverage("unknown", model.NativeClaimObserve); err == nil {
		t.Fatal("unknown claim admitted")
	}
	if agreement.ValidateDetector("synthetic-detector", 1, "clipboard", "clipboard-changed", model.NativeClaimObserve, []model.NativeSourceID{model.NativeSourceClipboard}) != nil {
		t.Fatal("registered detector rejected")
	}
	if agreement.ValidateDetector("synthetic-detector", 1, "clipboard", "arbitrary-prose", model.NativeClaimObserve, []model.NativeSourceID{model.NativeSourceClipboard}) == nil {
		t.Fatal("unknown condition admitted")
	}
	if agreement.ValidateDetector("synthetic-detector", 2, "clipboard", "clipboard-changed", model.NativeClaimObserve, []model.NativeSourceID{model.NativeSourceClipboard}) == nil {
		t.Fatal("unknown detector version admitted")
	}
	f.expected.Coverage[0].PermittedClaims[0] = model.NativeClaimUnavailable
	copy := agreement.Matrix()
	copy.Entries[0].Claim = model.NativeClaimUnavailable
	if _, err := agreement.Coverage("baseline.capture", model.NativeClaimEnforce); err != nil {
		t.Fatal("verified agreement mutated through caller", err)
	}
}

func TestVerifyRejectsUntrustedOrInconsistentArtifacts(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*releaseFixture, *testing.T){
		"unknown key":        func(f *releaseFixture, t *testing.T) { f.artifacts.Matrix.KeyID = "unknown" },
		"malformed key":      func(f *releaseFixture, t *testing.T) { f.keys[f.artifacts.Matrix.KeyID] = []byte{1} },
		"tampered signature": func(f *releaseFixture, t *testing.T) { f.artifacts.Matrix.Signature[0] ^= 1 },
		"changed signed bytes": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Matrix.Payload = append(f.artifacts.Matrix.Payload, ' ')
		},
		"registry bytes": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Registry = []byte(`{"synthetic_test_registry":false}`)
		},
		"source manifest": func(f *releaseFixture, t *testing.T) { f.artifacts.SourceManifest = []byte(`[]`) },
		"source schema": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Schemas[f.expected.SourceSchemaDigests[model.NativeSourceWindow]] = []byte(`{}`)
		},
		"extra schema": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Schemas[model.SHA256Fingerprint([]byte(`{}`))] = []byte(`{}`)
		},
		"configuration manifest": func(f *releaseFixture, t *testing.T) { f.artifacts.ConfigurationManifest = []byte(`{}`) },
		"detector catalog":       func(f *releaseFixture, t *testing.T) { f.artifacts.DetectorCatalog = []byte(`[]`) },
		"release mismatch":       func(f *releaseFixture, t *testing.T) { f.matrix.ReleaseID = "another-release"; f.sign(t) },
		"target mismatch":        func(f *releaseFixture, t *testing.T) { f.matrix.TargetTuple = "another-target"; f.sign(t) },
		"matrix ID mismatch":     func(f *releaseFixture, t *testing.T) { f.matrix.MatrixID = "another-matrix"; f.sign(t) },
		"registry mismatch": func(f *releaseFixture, t *testing.T) {
			f.matrix.RegistryDigest = model.SHA256Fingerprint([]byte(`{}`))
			f.sign(t)
		},
		"schema binding": func(f *releaseFixture, t *testing.T) {
			f.matrix.Entries[0].SourceSchemaDigest = f.matrix.Entries[1].SourceSchemaDigest
			f.sign(t)
		},
		"unknown coverage key": func(f *releaseFixture, t *testing.T) { f.matrix.Entries[4].CoverageKey = "native.unknown"; f.sign(t) },
		"unsupported enforce": func(f *releaseFixture, t *testing.T) {
			f.matrix.Entries[4].Claim = model.NativeClaimEnforce
			f.matrix.Entries[4].Verification = "passed"
			f.sign(t)
		},
		"unpassed claim": func(f *releaseFixture, t *testing.T) { f.matrix.Entries[3].Verification = "unavailable"; f.sign(t) },
		"baseline unavailable": func(f *releaseFixture, t *testing.T) {
			f.matrix.Entries[0].Claim = model.NativeClaimUnavailable
			f.matrix.Entries[0].Verification = "unavailable"
			f.sign(t)
		},
		"baseline missing": func(f *releaseFixture, t *testing.T) { f.expected.Coverage[0].Baseline = false },
		"unknown permission": func(f *releaseFixture, t *testing.T) {
			f.matrix.Entries[3].RequiredPermissions = []string{"shell"}
			f.sign(t)
		},
		"case alias": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Matrix.Payload = []byte(strings.Replace(string(f.artifacts.Matrix.Payload), `"matrix_id"`, `"MATRIX_ID"`, 1))
			f.signPayload()
		},
		"duplicate key": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Matrix.Payload = []byte(strings.Replace(string(f.artifacts.Matrix.Payload), `"matrix_id":`, `"matrix_id":"duplicate","matrix_id":`, 1))
			f.signPayload()
		},
		"old version selector": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Matrix.Payload = append([]byte(`{"schema_version":2,`), f.artifacts.Matrix.Payload[1:]...)
			f.signPayload()
		},
		"omitted required field": func(f *releaseFixture, t *testing.T) {
			f.artifacts.Matrix.Payload = []byte(strings.Replace(string(f.artifacts.Matrix.Payload), `"required_permissions":[],`, "", 1))
			f.signPayload()
		},
		"null array": func(f *releaseFixture, t *testing.T) { f.matrix.Entries[0].RequiredPermissions = nil; f.sign(t) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			f := fixture(t)
			mutate(&f, t)
			if _, err := Verify(f.expected, f.artifacts, f.keys); err != ErrInvalidArtifact {
				t.Fatalf("invalid artifact result: %v", err)
			}
		})
	}
}
