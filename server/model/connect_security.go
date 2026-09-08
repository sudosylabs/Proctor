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
	"time"
)

type ConnectSecurity struct {
	Kind            string                 `json:"kind"`
	PreflightID     string                 `json:"preflight_id,omitempty"`
	ReportDigest    string                 `json:"report_digest,omitempty"`
	ParticipationID AttemptParticipationID `json:"participation_id,omitempty"`
	Generation      int64                  `json:"generation,omitempty"`
	PolicyDigest    string                 `json:"policy_digest,omitempty"`
}

func (s ConnectSecurity) Validate() error {
	if s.Kind == "preflight" && IsValidAgreementID(s.PreflightID) && IsValidSHA256Fingerprint(s.ReportDigest) && s.ParticipationID == "" && s.Generation == 0 && s.PolicyDigest == "" {
		return nil
	}
	if s.Kind == "resume" && s.PreflightID == "" && s.ReportDigest == "" && s.ParticipationID.IsValid() && s.Generation > 0 && securitySafeInt(s.Generation) && IsValidSHA256Fingerprint(s.PolicyDigest) {
		return nil
	}
	return ErrSecurityPreflightInvalid
}
func (s *ConnectSecurity) UnmarshalJSON(raw []byte) error {
	if s == nil || len(raw) > 1024 || validateExamDocumentJSON(raw) != nil {
		return ErrSecurityPreflightInvalid
	}
	type wire ConnectSecurity
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil {
		return ErrSecurityPreflightInvalid
	}
	candidate := ConnectSecurity(decoded)
	if candidate.Validate() != nil {
		return ErrSecurityPreflightInvalid
	}
	supplied, err := encodeCanonicalExamRaw(raw)
	if err != nil {
		return err
	}
	expected, err := encodeCanonicalExamDocument(decoded)
	if err != nil || !bytes.Equal(supplied, expected) {
		return ErrSecurityPreflightInvalid
	}
	*s = candidate
	return nil
}

type AdmittedSecurity struct {
	Policy                 EffectiveExamSecurityPolicy `json:"policy"`
	PolicyContentDigest    string                      `json:"policy_content_digest"`
	PreflightID            string                      `json:"preflight_id"`
	PreflightReportDigest  string                      `json:"preflight_report_digest"`
	PreflightPolicyDigest  string                      `json:"preflight_policy_digest"`
	ParticipationID        AttemptParticipationID      `json:"participation_id"`
	Generation             int64                       `json:"generation"`
	SecuritySessionID      string                      `json:"security_session_id"`
	DeliveryStreamID       string                      `json:"delivery_stream_id"`
	RenewalIntervalSeconds int64                       `json:"renewal_interval_seconds"`
}

func (s AdmittedSecurity) Validate() error {
	if s.Policy.Scope.Kind != "attempt" || s.Policy.Validate(s.Policy.ActiveFrom) != nil || !IsValidSHA256Fingerprint(s.PolicyContentDigest) || !IsValidAgreementID(s.PreflightID) || !IsValidSHA256Fingerprint(s.PreflightReportDigest) || !IsValidSHA256Fingerprint(s.PreflightPolicyDigest) || !s.ParticipationID.IsValid() || s.Generation <= 0 || !securitySafeInt(s.Generation) || s.Policy.Ordinal != s.Generation || !IsValidAgreementID(s.SecuritySessionID) || !IsValidAgreementID(s.DeliveryStreamID) || s.RenewalIntervalSeconds != int64(AttemptParticipationRenewalInterval/time.Second) {
		return ErrSecurityPolicyInvalid
	}
	return nil
}
