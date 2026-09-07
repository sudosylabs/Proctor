// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

var ErrAuthenticationGenerationChanged = errors.New("authentication generation changed")
var ErrMFAReenrollmentRequired = errors.New("MFA reenrollment required")

// MFAPendingEnrollment supersedes pending setup only for the exact live
// Session and generation. Setup and its required audit commit together.
type MFAPendingEnrollment struct {
	Principal               model.Principal
	Credential              *model.MFACredential
	Lifetime                time.Duration
	RecentAuthenticationTTL time.Duration
	AuditEventID            model.AuditEventID
}

// MFAChallenge consumes one factor and upgrades only the exact Session in the
// same commit as required audit. It cannot clear an assisted-reset restriction.
type MFAChallenge struct {
	Principal        model.Principal
	CredentialID     model.MFACredentialID
	TimeStep         int64
	RecoveryCodeHash string
	VerifiedAt       time.Time
	AuditEventID     model.AuditEventID
}

// MFAAssistedReset is a protected, non-self administrative reset. The named
// operation rechecks current authority, live strong/recent credential and
// active target under the same fences as issuance, MFA and authority changes.
// Credential retirement, generation advance, mandatory reenrollment, every
// target Session/PAT/unfinished access-grant revocation and audit+notice are one
// commit. A repeated reset advances the generation again; it is not a replay.
type MFAAssistedReset struct {
	Principal               model.Principal
	UserID                  model.UserID
	IdentityVerified        bool
	Reason                  string
	VerificationReference   string
	RecentAuthenticationTTL time.Duration
	AuditEventID            model.AuditEventID
	Capabilities            AccessDeploymentCapabilities
	Notice                  MFASecurityNotice
	NoticeAt                time.Time
}

type MFAResetResult struct {
	Recovery          *model.UserMFARecovery
	Sessions          []*model.Session
	AccessTokenHashes []string
}
