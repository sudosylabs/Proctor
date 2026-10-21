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
	AuditAt                 int64
	Notice                  MFASecurityNotice
}

type MFARecoveryCodesRegeneration struct {
	Principal               model.Principal
	RecentAuthenticationTTL time.Duration
	UserID                  model.UserID
	RecoveryCodes           []*model.MFARecoveryCode
	At                      int64
	AuditEventID            model.AuditEventID
	AuditAt                 int64
	Notice                  MFASecurityNotice
}

type MFADisablement struct {
	Principal               model.Principal
	RecentAuthenticationTTL time.Duration
	UserID                  model.UserID
	At                      int64
	AuditEventID            model.AuditEventID
	AuditAt                 int64
	Notice                  MFASecurityNotice
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
	ConsumeSecondFactor(context.Context, model.UserID, int64, string, int64) error
	UpgradeSession(context.Context, model.SessionID, model.UserID, time.Time) ([]string, error)
	ReplaceRecoveryCodes(context.Context, *MFARecoveryCodesRegeneration) error
	CountRecoveryCodes(context.Context, model.UserID) (int, error)
	Disable(context.Context, *MFADisablement) (*MFADisableResult, error)
	// SeedPending is an unaudited fixture write; production code never calls it.
	SeedPending(context.Context, *model.MFACredential) (*model.MFACredential, error)
}
