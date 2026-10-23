// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type MFAActivationResult struct {
	Credential        *model.MFACredential
	Session           *model.Session
	AccessTokenHashes []string
}

type MFADisableResult struct {
	AccessTokenHashes []string
}

// MFASecurityNotice is the complete durable ordinary-mail intent coupled to
// one successful MFA security transition. It deliberately carries no MFA
// secret, encrypted credential state, recovery code, or recovery-code hash.
type MFASecurityNotice struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
}

type MFAActivationMutation struct {
	Principal               model.Principal
	RecentAuthenticationTTL time.Duration
	CredentialID            model.MFACredentialID
	UserID                  model.UserID
	TimeStep                int64
	RecoveryCodes           []*model.MFARecoveryCode
	SessionID               model.SessionID
	At                      time.Time
	AuditEventID            model.AuditEventID
	AuditAt                 time.Time
	Notice                  MFASecurityNotice
}

type MFARecoveryCodesRegeneration struct {
	Principal               model.Principal
	RecentAuthenticationTTL time.Duration
	UserID                  model.UserID
	RecoveryCodes           []*model.MFARecoveryCode
	At                      time.Time
	AuditEventID            model.AuditEventID
	AuditAt                 time.Time
	Notice                  MFASecurityNotice
}

type MFADisablement struct {
	Principal               model.Principal
	RecentAuthenticationTTL time.Duration
	UserID                  model.UserID
	At                      time.Time
	AuditEventID            model.AuditEventID
	AuditAt                 time.Time
	Notice                  MFASecurityNotice
}

// MFASecretKeyUsage counts the live TOTP credentials whose secret is sealed
// with one key of the MFA key ring.
type MFASecretKeyUsage struct {
	KeyID       string
	Credentials int64
}

// MFAStore owns the encrypted TOTP credential, hashed recovery codes, replay
// prevention, and the session-strength changes coupled to MFA lifecycle.
// Activation and Session upgrade use UTC microsecond decision instants and
// reject a pending credential or Session exactly at its expiry.
type MFAStore interface {
	GetRecoveryState(context.Context, model.UserID) (*model.UserMFARecovery, error)
	SavePending(context.Context, *MFAPendingEnrollment) (*model.MFACredential, error)
	Challenge(context.Context, *MFAChallenge) (*SessionReauthenticationResult, error)
	Reset(context.Context, *MFAAssistedReset) (*MFAResetResult, error)
	GetByUser(context.Context, model.UserID) (*model.MFACredential, error)
	Activate(context.Context, *MFAActivationMutation) (*MFAActivationResult, error)
	ConsumeSecondFactor(context.Context, *MFASecondFactorConsumption) error
	UpgradeSession(context.Context, *MFASessionUpgrade) ([]string, error)
	ReplaceRecoveryCodes(context.Context, *MFARecoveryCodesRegeneration) error
	CountRecoveryCodes(context.Context, model.UserID) (int, error)
	// InspectKeyState reads the durable primary-key fence with, per sealing
	// key, how many unarchived credentials hold a secret sealed with it,
	// ordered by key ID. Startup uses it to prove the configured key ring can
	// open every secret and write under the fence.
	InspectKeyState(context.Context) (*MFAKeyState, error)
	// StartRekey refuses while another rekey is queued or running, advances
	// the primary-key fence, queues the Job, and completes its audit.
	StartRekey(context.Context, *MFARekeyStart) (*MFARekeyOperation, error)
	// ListRekeyTargets pages the live credentials sealed with another key
	// than the fenced primary; a stale fence is a conflict.
	ListRekeyTargets(context.Context, *MFARekeyTargetPageRequest) (*MFARekeyTargetPage, error)
	// ReplaceRekeyTarget reports whether it re-sealed the credential; a
	// credential archived or re-sealed meanwhile is skipped, and a stale
	// fence is a conflict.
	ReplaceRekeyTarget(context.Context, *MFARekeyReplacement) (bool, error)
	// ProveRekey counts the live credentials still sealed with another key
	// than the fenced primary, and with the retiring key.
	ProveRekey(context.Context, *MFARekeyProofRequest) (*MFARekeyProof, error)
	Disable(context.Context, *MFADisablement) (*MFADisableResult, error)
	// SeedPending is an unaudited fixture write; production code never calls it.
	SeedPending(context.Context, *model.MFACredential) (*model.MFACredential, error)
}

// MFASecondFactorConsumption is the input to MFAStore.ConsumeSecondFactor.
type MFASecondFactorConsumption struct {
	UserID           model.UserID
	TimeStep         int64
	RecoveryCodeHash string
	Now              time.Time
}

// MFASessionUpgrade is the input to MFAStore.UpgradeSession.
type MFASessionUpgrade struct {
	SessionID model.SessionID
	UserID    model.UserID
	Now       time.Time
}
