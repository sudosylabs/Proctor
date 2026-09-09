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
	"errors"
	"slices"
	"time"
)

const (
	SecurityPreflightLifetime           = 120 * time.Second
	SecurityPreflightAdmissionFreshness = 30 * time.Second
	SecurityPreflightReportMaxBytes     = 64 * 1024
)

var ErrSecurityPreflightInvalid = errors.New("security preflight is invalid")

type SecurityReasonCode string

const (
	SecurityReasonPolicyChanged            SecurityReasonCode = "policy_changed"
	SecurityReasonUnsupportedCapability    SecurityReasonCode = "unsupported_capability"
	SecurityReasonSourceUnavailable        SecurityReasonCode = "source_unavailable"
	SecurityReasonPermissionRequired       SecurityReasonCode = "permission_required"
	SecurityReasonBaselineUnavailable      SecurityReasonCode = "baseline_unavailable"
	SecurityReasonPreflightExpired         SecurityReasonCode = "preflight_expired"
	SecurityReasonPreflightSuperseded      SecurityReasonCode = "preflight_superseded"
	SecurityReasonPostureBlocked           SecurityReasonCode = "posture_blocked"
	SecurityReasonConfigurationUnsupported SecurityReasonCode = "configuration_unsupported"
	SecurityReasonSessionChanged           SecurityReasonCode = "session_changed"
)

func (s SecurityReasonCode) IsValid() bool {
	return slices.Contains([]SecurityReasonCode{SecurityReasonPolicyChanged, SecurityReasonUnsupportedCapability, SecurityReasonSourceUnavailable, SecurityReasonPermissionRequired, SecurityReasonBaselineUnavailable, SecurityReasonPreflightExpired, SecurityReasonPreflightSuperseded, SecurityReasonPostureBlocked, SecurityReasonConfigurationUnsupported, SecurityReasonSessionChanged}, s)
}

type SecurityPreflightChallenge struct {
	PreflightID string    `json:"preflight_id"`
	Challenge   string    `json:"challenge"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (c SecurityPreflightChallenge) Validate() error {
	raw, err := base64.RawURLEncoding.DecodeString(c.Challenge)
	if !IsValidAgreementID(c.PreflightID) || err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != c.Challenge || !securityInstant(c.IssuedAt) || !securityInstant(c.ExpiresAt) || c.ExpiresAt.Sub(c.IssuedAt) != SecurityPreflightLifetime {
		return ErrSecurityPreflightInvalid
	}
	return nil
}

type NativeSourceCoverage struct {
	SourceID           NativeSourceID `json:"source_id"`
	SourceInstanceID   string         `json:"source_instance_id"`
	Sequence           int64          `json:"sequence"`
	SourceSchemaDigest string         `json:"source_schema_digest"`
	AdapterVersion     string         `json:"adapter_version"`
	Health             string         `json:"health"`
	Permission         string         `json:"permission"`
	Complete           bool           `json:"complete"`
	GapCount           int64          `json:"gap_count"`
}
type NativeCoverageClaim struct {
	CoverageKey        string                `json:"coverage_key"`
	SourceSchemaDigest string                `json:"source_schema_digest"`
	Claim              NativeCapabilityClaim `json:"claim"`
	State              string                `json:"state"`
}
type NativeBaselineReport struct {
	ConstrainedWindow     string `json:"constrained_window"`
	SinglePhysicalDisplay string `json:"single_physical_display"`
	ContentProtection     string `json:"content_protection"`
}
type SecurityPreflightReport struct {
	reportedAtSpelling string

	Challenge                string                 `json:"challenge"`
	PolicyDigest             string                 `json:"policy_digest"`
	PolicyContentDigest      string                 `json:"policy_content_digest"`
	CapabilityMatrixDigest   string                 `json:"capability_matrix_digest"`
	SecuritySessionID        string                 `json:"security_session_id"`
	SourceManifestDigest     string                 `json:"source_manifest_digest"`
	SelectedSourceCategories []NativeSourceCategory `json:"selected_source_categories"`
	Sources                  []NativeSourceCoverage `json:"sources"`
	Coverage                 []NativeCoverageClaim  `json:"coverage"`
	Baseline                 NativeBaselineReport   `json:"baseline"`
	Posture                  string                 `json:"posture"`
	ReportedAt               time.Time              `json:"reported_at"`
}

type SecurityPreflightResult struct {
	PreflightID  string               `json:"preflight_id"`
	ReportDigest string               `json:"report_digest"`
	Admission    string               `json:"admission"`
	ReasonCodes  []SecurityReasonCode `json:"reason_codes"`
	ServerTime   time.Time            `json:"server_time"`
	ExpiresAt    time.Time            `json:"expires_at"`
}

func (r SecurityPreflightResult) Validate() error {
	if !IsValidAgreementID(r.PreflightID) || !IsValidSHA256Fingerprint(r.ReportDigest) || !securityInstant(r.ServerTime) || !securityInstant(r.ExpiresAt) || r.ReasonCodes == nil || len(r.ReasonCodes) > 10 || (r.Admission != "eligible" && r.Admission != "blocked") || (r.Admission == "eligible") != (len(r.ReasonCodes) == 0) {
		return ErrSecurityPreflightInvalid
	}
	seen := map[SecurityReasonCode]bool{}
	for _, reason := range r.ReasonCodes {
		if !reason.IsValid() || seen[reason] {
			return ErrSecurityPreflightInvalid
		}
		seen[reason] = true
	}
	return nil
}

func (r SecurityPreflightReport) Validate() error {
	raw, err := base64.RawURLEncoding.DecodeString(r.Challenge)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != r.Challenge || !IsValidSHA256Fingerprint(r.PolicyDigest) || !IsValidSHA256Fingerprint(r.PolicyContentDigest) || !IsValidSHA256Fingerprint(r.CapabilityMatrixDigest) || !IsValidAgreementID(r.SecuritySessionID) || !IsValidSHA256Fingerprint(r.SourceManifestDigest) || r.SelectedSourceCategories == nil || len(r.SelectedSourceCategories) > len(NativeSourceCategories()) || r.Sources == nil || len(r.Sources) > len(NativeSources()) || r.Coverage == nil || len(r.Coverage) > NativeCoverageClaimLimit || !slices.Contains([]string{"compliant", "degraded", "contained", "failed"}, r.Posture) || !securityInstant(r.ReportedAt) {
		return ErrSecurityPreflightInvalid
	}
	for _, status := range []string{r.Baseline.ConstrainedWindow, r.Baseline.SinglePhysicalDisplay, r.Baseline.ContentProtection} {
		if status != "verified" && status != "unavailable" {
			return ErrSecurityPreflightInvalid
		}
	}
	for i, category := range r.SelectedSourceCategories {
		if !slices.Contains(NativeSourceCategories(), category) || slices.Contains(r.SelectedSourceCategories[:i], category) {
			return ErrSecurityPreflightInvalid
		}
	}
	if err := ValidateNativeCoverageSnapshot(r.Sources, r.Coverage); err != nil {
		return err
	}
	document, err := encodeCanonicalExamDocument(r)
	if err != nil || len(document) > SecurityPreflightReportMaxBytes {
		return ErrSecurityPreflightInvalid
	}
	return nil
}
func securitySafeInt(value int64) bool { return value >= 0 && value <= 9007199254740991 }
func (r SecurityPreflightReport) Canonical() ([]byte, error) {
	if r.Validate() != nil {
		return nil, ErrSecurityPreflightInvalid
	}
	return encodeCanonicalExamDocument(r)
}

// EvaluateSecurityPreflight interprets minimized assertions against immutable
// verified release meaning. It does not authenticate a Session or grant a lease;
// the owning atomic Store operation must establish those current fences.
func EvaluateSecurityPreflight(challenge SecurityPreflightChallenge, resolved ResolvedNativePolicy, agreement *DesktopNativeAgreement, report SecurityPreflightReport, now time.Time) (SecurityPreflightResult, error) {
	if challenge.Validate() != nil || report.Validate() != nil || !securityInstant(now) || resolved.Policy.Validate(now) != nil || agreement == nil || agreement.MatrixDigest() != resolved.CapabilityMatrixDigest || report.Challenge != challenge.Challenge || report.PolicyDigest != resolved.Policy.Digest || report.PolicyContentDigest != resolved.PolicyContentDigest || report.CapabilityMatrixDigest != resolved.CapabilityMatrixDigest || report.SourceManifestDigest != agreement.SourceManifestDigest() || !slices.Equal(report.SelectedSourceCategories, resolved.Categories) || len(report.Sources) != len(resolved.Sources) || len(report.Coverage) != len(resolved.Requirements) {
		return SecurityPreflightResult{}, ErrSecurityPreflightInvalid
	}
	document, _ := report.Canonical()
	result := SecurityPreflightResult{PreflightID: challenge.PreflightID, ReportDigest: SHA256Fingerprint(document), Admission: "eligible", ReasonCodes: []SecurityReasonCode{}, ServerTime: now, ExpiresAt: challenge.ExpiresAt}
	block := func(reason SecurityReasonCode) {
		result.Admission = "blocked"
		if !slices.Contains(result.ReasonCodes, reason) {
			result.ReasonCodes = append(result.ReasonCodes, reason)
		}
	}
	if now.Before(challenge.IssuedAt) {
		return SecurityPreflightResult{}, ErrSecurityPreflightInvalid
	}
	if !now.Before(challenge.ExpiresAt) {
		block(SecurityReasonPreflightExpired)
	}
	for _, status := range []string{report.Baseline.ConstrainedWindow, report.Baseline.SinglePhysicalDisplay, report.Baseline.ContentProtection} {
		if status != "verified" {
			block(SecurityReasonBaselineUnavailable)
		}
	}

	reasons, err := EvaluateNativeCoverage(resolved, report.Sources, report.Coverage, report.Posture)
	if err != nil {
		return SecurityPreflightResult{}, err
	}
	for _, reason := range reasons {
		block(reason)
	}
	return result, nil
}

// ValidateNativeCoverageSnapshot validates the ordered source and coverage facts
// shared by admission, renewal and retained recovery snapshots.
func ValidateNativeCoverageSnapshot(sources []NativeSourceCoverage, claims []NativeCoverageClaim) error {
	if sources == nil || len(sources) > len(NativeSources()) || claims == nil || len(claims) > NativeCoverageClaimLimit {
		return ErrSecurityPreflightInvalid
	}
	previous := -1
	for _, source := range sources {
		order := slices.Index(NativeSources(), source.SourceID)
		if order <= previous || !IsValidAgreementID(source.SourceInstanceID) || !securitySafeInt(source.Sequence) || !IsValidSHA256Fingerprint(source.SourceSchemaDigest) || !IsValidAgreementID(source.AdapterVersion) || !slices.Contains([]string{"starting", "healthy", "degraded", "failed", "recovering", "stopped"}, source.Health) || !slices.Contains([]string{"denied", "granted", "not_determined", "restricted", "unavailable"}, source.Permission) || !securitySafeInt(source.GapCount) {
			return ErrSecurityPreflightInvalid
		}
		previous = order
	}
	previousKey := ""
	for _, coverage := range claims {
		if !IsValidAgreementID(coverage.CoverageKey) || coverage.CoverageKey <= previousKey || !IsValidSHA256Fingerprint(coverage.SourceSchemaDigest) || !slices.Contains([]NativeCapabilityClaim{NativeClaimEnforce, NativeClaimObserve, NativeClaimUnavailable}, coverage.Claim) || !slices.Contains([]string{"ready", "degraded", "unavailable"}, coverage.State) || coverage.Claim == NativeClaimUnavailable && coverage.State != "unavailable" {
			return ErrSecurityPreflightInvalid
		}
		previousKey = coverage.CoverageKey
	}
	return nil
}

// EvaluateNativeCoverage interprets current minimized coverage independently of
// challenge expiry. The caller owns policy/Session/lease and source continuity.
func EvaluateNativeCoverage(resolved ResolvedNativePolicy, sources []NativeSourceCoverage, claims []NativeCoverageClaim, posture string) ([]SecurityReasonCode, error) {
	if ValidateNativeCoverageSnapshot(sources, claims) != nil || len(sources) != len(resolved.Sources) || len(claims) != len(resolved.Requirements) || !slices.Contains([]string{"checking", "compliant", "degraded", "contained", "failed"}, posture) {
		return nil, ErrSecurityControlInvalid
	}
	reasons := []SecurityReasonCode{}
	block := func(reason SecurityReasonCode) {
		if !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	if posture == "checking" || posture == "contained" || posture == "failed" {
		block(SecurityReasonPostureBlocked)
	}
	for index, claim := range claims {
		required := resolved.Requirements[index]
		if claim.CoverageKey != required.CoverageKey || claim.SourceSchemaDigest != required.Entry.SourceSchemaDigest || claim.Claim != required.Entry.Claim {
			return nil, ErrSecurityPreflightInvalid
		}
		if required.RequiredClaim == NativeClaimEnforce && claim.State != "ready" {
			block(SecurityReasonUnsupportedCapability)
		}
	}
	for index, source := range sources {
		if source.SourceID != resolved.Sources[index] {
			return nil, ErrSecurityPreflightInvalid
		}
		requiredSource := false
		requiresPermission := false
		for _, requirement := range resolved.Requirements {
			if requirement.SourceID != source.SourceID || requirement.Entry.Claim == NativeClaimUnavailable {
				continue
			}
			if source.SourceSchemaDigest != requirement.Entry.SourceSchemaDigest || source.AdapterVersion != requirement.Entry.AdapterVersion {
				return nil, ErrSecurityPreflightInvalid
			}
			if requirement.RequiredClaim == NativeClaimEnforce {
				requiredSource = true
				if len(requirement.Entry.RequiredPermissions) > 0 {
					requiresPermission = true
				}
			}
		}
		if requiredSource && (!source.Complete || source.GapCount != 0 || source.Health != "healthy") {
			block(SecurityReasonSourceUnavailable)
		}
		if requiresPermission && source.Permission != "granted" {
			block(SecurityReasonPermissionRequired)
		}
	}
	return reasons, nil
}

func (v SecurityPreflightReport) MarshalJSON() ([]byte, error) {
	type wire SecurityPreflightReport
	return json.Marshal(struct {
		*wire
		ReportedAt securityJSONInstant `json:"reported_at"`
	}{wire: (*wire)(&v), ReportedAt: securityJSONInstant{Time: v.ReportedAt, spelling: v.reportedAtSpelling}})
}
func (v *SecurityPreflightReport) UnmarshalJSON(raw []byte) error {
	if v == nil {
		return ErrSecurityPreflightInvalid
	}
	type wire SecurityPreflightReport
	var decoded wire
	value := struct {
		*wire
		ReportedAt securityJSONInstant `json:"reported_at"`
	}{wire: &decoded}
	if decodeClosedDeliveryDeclaration(raw, &value, SecurityPreflightReportMaxBytes) != nil {
		return ErrSecurityPreflightInvalid
	}
	candidate := SecurityPreflightReport(decoded)
	candidate.ReportedAt = value.ReportedAt.Time
	candidate.reportedAtSpelling = value.ReportedAt.spelling
	if candidate.Validate() != nil {
		return ErrSecurityPreflightInvalid
	}
	*v = candidate
	return nil
}
