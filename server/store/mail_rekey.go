// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type MailRekeyStart struct {
	PrimaryKeyID  string
	RetiringKeyID string
	Job           *model.Job
	AuditEventID  string
	AuditAt       int64
}

type MailRekeyOperation struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
	CreatedAt     time.Time
}

type MailPayloadKeyUsage struct {
	KeyID            string
	ActiveReferences int64
}

type MailKeyState struct {
	RequiredPrimaryKeyID string
	// PrimaryPromotionAllowed is true only after the operation which installed
	// RequiredPrimaryKeyID completed with a valid zero-reference retirement
	// proof. A node may then stage a different primary while retaining the
	// required key for reads; persistence still rejects its writes until the
	// next rekey command advances the durable fence.
	PrimaryPromotionAllowed bool
	Active                  []MailPayloadKeyUsage
}

// MailRekeyTargetKind identifies an encrypted mail-domain value without
// exposing its ciphertext through any operator projection.
type MailRekeyTargetKind string

const (
	MailRekeyTargetDelivery     MailRekeyTargetKind = "delivery"
	MailRekeyTargetFanoutBundle MailRekeyTargetKind = "fanout_bundle"
)

type MailRekeyTarget struct {
	Kind             MailRekeyTargetKind
	ID               string
	KeyID            string
	EncryptedPayload json.RawMessage
}

type MailRekeyTargetPageRequest struct {
	JobID        model.JobID
	PrimaryKeyID string
	AfterKind    MailRekeyTargetKind
	AfterID      string
	Limit        int
}

type MailRekeyTargetPage struct {
	Targets []MailRekeyTarget
	More    bool
}

type MailRekeyReplacement struct {
	JobID            model.JobID
	Kind             MailRekeyTargetKind
	ID               string
	ExpectedKeyID    string
	PrimaryKeyID     string
	EncryptedPayload json.RawMessage
}

type MailRekeyProofRequest struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
}

// MailRekeyProof is safe operational metadata: key identities and aggregate
// reference counts only. It never contains key material, owners, or payloads.
type MailRekeyProof struct {
	PrimaryKeyID         string
	RetiringKeyID        string
	NonPrimaryReferences int64
	RetiringReferences   int64
	RetirementSafe       bool
}
