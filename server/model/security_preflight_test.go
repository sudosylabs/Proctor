// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func preflightFixture(t *testing.T) (NativePolicyResolution, ResolvedNativePolicy, SecurityPreflightChallenge, SecurityPreflightReport) {
	t.Helper()
	input := nativeResolutionFixture(t)
	setNativeFamily(t, &input, 2, `{"id":"clipboard","mode":"observe","strategy":"disabled","observe_os_clipboard_changes":true}`)
	resolved, err := ResolveNativePolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	challenge := SecurityPreflightChallenge{PreflightID: "synthetic-preflight", Challenge: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), IssuedAt: input.ActivationTime, ExpiresAt: input.ActivationTime.Add(SecurityPreflightLifetime)}
	report := SecurityPreflightReport{Challenge: challenge.Challenge, PolicyDigest: resolved.Policy.Digest, PolicyContentDigest: resolved.PolicyContentDigest, CapabilityMatrixDigest: resolved.CapabilityMatrixDigest, SecuritySessionID: "synthetic-security-session", SourceManifestDigest: input.Build.NativeAgreement.SourceManifestDigest(), SelectedSourceCategories: resolved.Categories, Sources: []NativeSourceCoverage{}, Coverage: []NativeCoverageClaim{}, Baseline: NativeBaselineReport{ConstrainedWindow: "verified", SinglePhysicalDisplay: "verified", ContentProtection: "verified"}, Posture: "degraded", ReportedAt: input.ActivationTime}
	for _, source := range resolved.Sources {
		for _, r := range resolved.Requirements {
			if r.SourceID == source {
				report.Sources = append(report.Sources, NativeSourceCoverage{SourceID: source, SourceInstanceID: "synthetic-" + string(source), Sequence: 0, SourceSchemaDigest: r.Entry.SourceSchemaDigest, AdapterVersion: r.Entry.AdapterVersion, Health: "healthy", Permission: "granted", Complete: true, GapCount: 0})
				break
			}
		}
	}
	for _, r := range resolved.Requirements {
		state := "ready"
		if r.Entry.Claim == NativeClaimUnavailable {
			state = "unavailable"
		}
		report.Coverage = append(report.Coverage, NativeCoverageClaim{CoverageKey: r.CoverageKey, SourceSchemaDigest: r.Entry.SourceSchemaDigest, Claim: r.Entry.Claim, State: state})
	}
	return input, resolved, challenge, report
}
func TestSecurityPreflightEligibleDegradedObservation(t *testing.T) {
	input, resolved, challenge, report := preflightFixture(t)
	result, err := EvaluateSecurityPreflight(challenge, resolved, input.Build.NativeAgreement, report, input.ActivationTime)
	if err != nil || result.Admission != "eligible" || len(result.ReasonCodes) != 0 {
		t.Fatalf("result = %#v, %v", result, err)
	}
	document, err := report.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if result.ReportDigest != SHA256Fingerprint(document) {
		t.Fatal("client-selected digest")
	}
	var decoded SecurityPreflightReport
	if json.Unmarshal(document, &decoded) != nil {
		t.Fatal("strict report round trip")
	}
	// Client time is provenance; it cannot extend the server's challenge lifetime.
	report.ReportedAt = report.ReportedAt.Add(24 * time.Hour)
	result, err = EvaluateSecurityPreflight(challenge, resolved, input.Build.NativeAgreement, report, challenge.ExpiresAt)
	if err != nil || result.Admission != "blocked" || !slices.Contains(result.ReasonCodes, SecurityReasonPreflightExpired) || !result.ExpiresAt.Equal(challenge.ExpiresAt) {
		t.Fatal("client time extended the challenge")
	}
}
func TestSecurityPreflightBlocksWithoutInventingEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*SecurityPreflightReport){"baseline": func(r *SecurityPreflightReport) { r.Baseline.ContentProtection = "unavailable" }, "source health": func(r *SecurityPreflightReport) { r.Sources[0].Health = "failed" }, "incomplete source": func(r *SecurityPreflightReport) { r.Sources[0].Complete = false }, "source gap": func(r *SecurityPreflightReport) { r.Sources[0].GapCount = 1 }, "enforced coverage": func(r *SecurityPreflightReport) { r.Coverage[0].State = "degraded" }, "contained": func(r *SecurityPreflightReport) { r.Posture = "contained" }} {
		t.Run(name, func(t *testing.T) {
			input, resolved, challenge, report := preflightFixture(t)
			mutate(&report)
			result, err := EvaluateSecurityPreflight(challenge, resolved, input.Build.NativeAgreement, report, input.ActivationTime)
			if err != nil || result.Admission != "blocked" || len(result.ReasonCodes) == 0 {
				t.Fatalf("result = %#v, %v", result, err)
			}
		})
	}
}
func TestSecurityPreflightRejectsForgedAgreementAndSourceSets(t *testing.T) {
	for name, mutate := range map[string]func(*SecurityPreflightReport){"challenge": func(r *SecurityPreflightReport) {
		r.Challenge = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	}, "policy": func(r *SecurityPreflightReport) { r.PolicyDigest = SHA256Fingerprint([]byte("other")) }, "content": func(r *SecurityPreflightReport) { r.PolicyContentDigest = SHA256Fingerprint([]byte("other")) }, "matrix": func(r *SecurityPreflightReport) { r.CapabilityMatrixDigest = SHA256Fingerprint([]byte("other")) }, "manifest": func(r *SecurityPreflightReport) { r.SourceManifestDigest = SHA256Fingerprint([]byte("other")) }, "missing source": func(r *SecurityPreflightReport) { r.Sources = r.Sources[:2] }, "missing claim": func(r *SecurityPreflightReport) { r.Coverage = r.Coverage[:3] }, "self selected categories": func(r *SecurityPreflightReport) { r.SelectedSourceCategories = []NativeSourceCategory{} }, "adapter": func(r *SecurityPreflightReport) { r.Sources[0].AdapterVersion = "other" }, "schema": func(r *SecurityPreflightReport) { r.Sources[0].SourceSchemaDigest = SHA256Fingerprint([]byte("other")) }, "unavailable made ready": func(r *SecurityPreflightReport) {
		r.Coverage[3].Claim = NativeClaimEnforce
		r.Coverage[3].State = "ready"
	}} {
		t.Run(name, func(t *testing.T) {
			input, resolved, challenge, report := preflightFixture(t)
			mutate(&report)
			if _, err := EvaluateSecurityPreflight(challenge, resolved, input.Build.NativeAgreement, report, input.ActivationTime); err == nil {
				t.Fatal("forged report accepted")
			}
		})
	}
}
func TestSecurityPreflightExactCodec(t *testing.T) {
	_, _, _, report := preflightFixture(t)
	raw, err := report.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for name, document := range map[string]string{"missing boolean": strings.Replace(string(raw), `"complete":true,`, "", 1), "null boolean": strings.Replace(string(raw), `"complete":true`, `"complete":null`, 1), "unknown member": strings.Replace(string(raw), `"sequence":0`, `"sequence":0,"raw_inventory":[]`, 1), "unsafe integer": strings.Replace(string(raw), `"sequence":0`, `"sequence":9007199254740992`, 1), "integer exponent": strings.Replace(string(raw), `"sequence":0`, `"sequence":0e0`, 1), "duplicate": strings.Replace(string(raw), `"sequence":0`, `"sequence":0,"sequence":0`, 1), "uppercase": strings.Replace(string(raw), `"complete":`, `"COMPLETE":`, 1), "null category list": strings.Replace(string(raw), `"selected_source_categories":["os-clipboard-change"]`, `"selected_source_categories":null`, 1)} {
		t.Run(name, func(t *testing.T) {
			var value SecurityPreflightReport
			if json.Unmarshal([]byte(document), &value) == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
}
