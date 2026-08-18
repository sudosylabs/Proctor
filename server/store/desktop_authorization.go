// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
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

// DesktopAuthorizationMaintenanceResult describes one bounded, authoritative
// database-clock maintenance page. More reports that immediately eligible work
// remains for a subsequent page or scheduled occurrence.
type DesktopAuthorizationMaintenanceResult struct {
	Expired int
	Purged  int
	More    bool
}

// DesktopAuthorizationStore owns the durable native-public-client protocol.
// No raw handle, browser proof, state, code, verifier, or Session credential
// crosses this boundary.
type DesktopAuthorizationStore interface {
	Create(context.Context, *model.BrowserAuthenticationTransaction) (*model.BrowserAuthenticationTransaction, error)
	IssueCode(context.Context, *DesktopAuthorizationCodeIssue) (*model.BrowserAuthenticationTransaction, error)
	Cancel(context.Context, *DesktopAuthorizationCancellation) (*model.BrowserAuthenticationTransaction, error)
	Exchange(context.Context, *DesktopAuthorizationExchange) (*DesktopAuthorizationExchangeResult, error)
	Maintain(context.Context, int) (*DesktopAuthorizationMaintenanceResult, error)
}
