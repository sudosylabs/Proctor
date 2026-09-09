// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"time"
)

// NativeConditionEvidenceMaxBytes includes a full bounded batch record and its server provenance.
const NativeConditionEvidenceMaxBytes = 256*1024 + 2048

// NativeConditionEvidence preserves a selected occurrence transition without
// asserting a violation or requiring a Flag. Raw platform identity is excluded.
type NativeConditionEvidence struct {
	ID                   IntegrityEvidenceID    `json:"id"`
	AttemptID            ExamAttemptID          `json:"attempt_id"`
	ParticipationID      AttemptParticipationID `json:"participation_id"`
	Generation           int64                  `json:"generation"`
	StreamID             string                 `json:"stream_id"`
	SecuritySessionID    string                 `json:"security_session_id"`
	PolicyRevisionID     ExamRevisionID         `json:"policy_revision_id"`
	PolicyDigest         string                 `json:"policy_digest"`
	ApplicationReleaseID string                 `json:"application_release_id"`
	MatrixID             string                 `json:"matrix_id"`
	BatchSequence        int64                  `json:"batch_sequence"`
	RecordIndex          int                    `json:"record_index"`
	Occurrence           NativeOccurrence       `json:"occurrence"`
	UnresolvedOpener     bool                   `json:"unresolved_opener"`
	ReceivedAt           time.Time              `json:"received_at"`
	InterpretedAt        time.Time              `json:"interpreted_at"`
}

func (v NativeConditionEvidence) Validate() error {
	if !v.ID.IsValid() || !v.AttemptID.IsValid() || !v.ParticipationID.IsValid() || v.Generation < 1 || !securitySafeInt(v.Generation) || !IsValidAgreementID(v.StreamID) || !IsValidAgreementID(v.SecuritySessionID) || !v.PolicyRevisionID.IsValid() || !IsValidSHA256Fingerprint(v.PolicyDigest) || !IsValidAgreementID(v.ApplicationReleaseID) || !IsValidAgreementID(v.MatrixID) || v.BatchSequence < 1 || v.BatchSequence > NativeParticipationPositionLimit || v.RecordIndex < 0 || v.RecordIndex > 63 || v.Occurrence.Validate() != nil || !securityInstant(v.ReceivedAt) || !securityInstant(v.InterpretedAt) {
		return ErrNativeDeliveryInvalid
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > NativeConditionEvidenceMaxBytes {
		return ErrNativeDeliveryInvalid
	}
	return nil
}
func (v NativeConditionEvidence) Clone() NativeConditionEvidence {
	v.Occurrence.SourceRanges = append([]NativeSourceRange(nil), v.Occurrence.SourceRanges...)
	return v
}

// A fixed digest commits every immutable transition without retaining a second
// unbounded inventory of identities in each finalization.
type NativeConditionInventory struct {
	Records int64  `json:"records"`
	Digest  string `json:"digest"`
}

func NewNativeConditionInventory() NativeConditionInventory {
	return NativeConditionInventory{Digest: SHA256Fingerprint([]byte("proctor.native-conditions.v1"))}
}
func (v NativeConditionInventory) Validate() error {
	if v.Records < 0 || v.Records > DeliveryAttemptRecordLimit || !IsValidSHA256Fingerprint(v.Digest) || (v.Records == 0 && v.Digest != NewNativeConditionInventory().Digest) {
		return ErrNativeDeliveryInvalid
	}
	return nil
}
func (v NativeConditionInventory) Append(record []byte) (NativeConditionInventory, error) {
	if v.Validate() != nil || v.Records == DeliveryAttemptRecordLimit || len(record) == 0 || len(record) > NativeConditionEvidenceMaxBytes {
		return v, ErrNativeDeliveryInvalid
	}
	raw := append([]byte("proctor.native-conditions.append.v1\x00"), []byte(v.Digest)...)
	raw = binary.BigEndian.AppendUint64(raw, uint64(v.Records+1))
	raw = append(raw, record...)
	return NativeConditionInventory{Records: v.Records + 1, Digest: SHA256Fingerprint(raw)}, nil
}
func IntegrityReviewNativeDigest(base string, inventory NativeConditionInventory) (string, error) {
	if !validLowerSHA256(base) || inventory.Validate() != nil {
		return "", ErrNativeDeliveryInvalid
	}
	if inventory.Records == 0 {
		return base, nil
	}
	raw := append([]byte("proctor.review.native-conditions.v1\x00"), []byte(base)...)
	raw = append(raw, []byte(inventory.Digest)...)
	raw = binary.BigEndian.AppendUint64(raw, uint64(inventory.Records))
	return strings.TrimPrefix(SHA256Fingerprint(raw), "sha256:"), nil
}
