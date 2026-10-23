// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// MFARekeyStart fences the MFA key ring on PrimaryKeyID and queues the rekey
// Job that re-seals every live TOTP secret under it, completing the attempt
// audit in the same transaction.
type MFARekeyStart struct {
	PrimaryKeyID  string
	RetiringKeyID string
	Job           *model.Job
	AuditEventID  model.AuditEventID
	AuditAt       time.Time
}

// MFARekeyOperation is the queued rekey a successful MFARekeyStart returns.
type MFARekeyOperation struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
	CreatedAt     time.Time
}

// MFAKeyState is the durable primary-key fence and the live credentials per
// sealing key. RequiredPrimaryKeyID is empty until the first rekey; after it,
// every new TOTP secret must be sealed with that key.
type MFAKeyState struct {
	RequiredPrimaryKeyID string
	ActiveRekeyJobID     model.JobID
	Active               []MFASecretKeyUsage
}

// MFARekeyTarget is one live credential whose secret is sealed with a key
// other than the fenced primary. EncryptedSecret is the stored envelope; it
// never leaves the rekey Job.
type MFARekeyTarget struct {
	CredentialID    model.MFACredentialID
	UserID          model.UserID
	KeyID           string
	EncryptedSecret string
}

// MFARekeyTargetPageRequest continues the credential scan of one fenced
// rekey Job after AfterCredentialID, in credential identity order.
type MFARekeyTargetPageRequest struct {
	JobID             model.JobID
	PrimaryKeyID      string
	AfterCredentialID model.MFACredentialID
	Limit             int
}

// MFARekeyTargetPage is one page of rekey targets; More reports that another
// page follows the last target.
type MFARekeyTargetPage struct {
	Targets []MFARekeyTarget
	More    bool
}

// MFARekeyReplacement swaps one credential's secret envelope for the same
// secret sealed with PrimaryKeyID, only while the credential is still live and
// sealed with ExpectedKeyID and the Job still holds the fence.
type MFARekeyReplacement struct {
	JobID           model.JobID
	CredentialID    model.MFACredentialID
	ExpectedKeyID   string
	PrimaryKeyID    string
	EncryptedSecret string
}

// MFARekeyProofRequest asks how many live credentials one fenced rekey Job
// still has to move.
type MFARekeyProofRequest struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
}

// MFARekeyProof is safe operational metadata: key identities and aggregate
// credential counts only. It never contains key material, owners, or secrets.
type MFARekeyProof struct {
	PrimaryKeyID         string
	RetiringKeyID        string
	NonPrimaryReferences int64
	RetiringReferences   int64
	RetirementSafe       bool
}
