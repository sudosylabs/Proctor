// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// DesktopAuthorizationCodeIssue is the complete, hash-only input for turning
// one browser-authentication proof into one short-lived Proctor code. The
// Store rechecks the current AccessPolicy, exact configured provider
// capability, active User, expiry, purpose, and every pinned proof atomically.
// CodeLifetime is applied to the Store's authoritative database timestamp.
type DesktopAuthorizationCodeIssue struct {
	HandleHash               string
	BrowserProofHash         string
	StateHash                string
	UserID                   model.UserID
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       model.ExternalIdentityID
	AuthenticationStrength   model.AuthenticationStrength
	AuthenticatedAt          int64
	MFACompletedAt           int64
	CodeHash                 string
	CodeLifetime             time.Duration
	Capabilities             AccessDeploymentCapabilities
	AuditEventID             string
	AuditAt                  int64
}

type DesktopAuthorizationCancellation struct {
	HandleHash       string
	BrowserProofHash string
	StateHash        string
	CancelledAt      int64
}

// DesktopAuthorizationExchange atomically consumes one code and creates an
// ordinary Desktop Session and its initial access/refresh credentials. All
// lifetimes are applied to one authoritative database timestamp.
type DesktopAuthorizationExchange struct {
	CodeHash         string
	StateHash        string
	CodeChallenge    string
	Issuer           string
	AccessTokenHash  string
	RefreshTokenHash string
	AccessLifetime   time.Duration
	RefreshLifetime  time.Duration
	IdleLifetime     time.Duration
	AbsoluteLifetime time.Duration
	MaximumActive    int
	Capabilities     AccessDeploymentCapabilities
	AuditEventID     string
	AuditAt          int64
}

type DesktopAuthorizationExchangeResult struct {
	Transaction *model.BrowserAuthenticationTransaction
	Session     *model.Session
	Credentials []*model.SessionCredential
}
