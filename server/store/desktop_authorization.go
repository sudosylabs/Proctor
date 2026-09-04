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

// DesktopAuthorizationCodeIssue is the complete, hash-only input for turning
// one browser-authentication proof into one short-lived Proctor code. The
// Store rechecks the current AccessPolicy, exact configured provider
// capability, active User, expiry, purpose, and every pinned proof atomically.
// CodeLifetime is applied to the Store's authoritative database timestamp.
type DesktopAuthorizationCodeIssue struct {
	BindingHash    string
	StateHash      string
	CodeHash       string
	ExpectedUserID model.UserID
	CodeLifetime   time.Duration
	Capabilities   AccessDeploymentCapabilities
	AuditEventID   string
	AuditAt        int64
}

type DesktopAuthorizationCancellation struct {
	BindingHash string
	StateHash   string
}

// DesktopAuthorizationExchangeProof resolves the exact still-live
// authorization represented by an exchange request without consuming it.
// The atomic Exchange operation repeats every check before committing.
type DesktopAuthorizationExchangeProof struct {
	CodeHash                string
	StateHash               string
	CodeChallenge           string
	Issuer                  string
	ExpectedKeyThumbprint   string
	DesktopRelease          string
	DesktopBuildID          string
	DesktopPlatform         model.DesktopPlatform
	DesktopArchitecture     model.DesktopArchitecture
	DesktopRealtimeProtocol int
}

type DesktopAuthorizationCodeIssued struct {
	CallbackURL   string
	CodeExpiresAt time.Time
}

// DesktopAuthorizationExchange atomically consumes one code and creates an
// ordinary Desktop Session and its initial access/refresh credentials. All
// lifetimes are applied to one authoritative database timestamp.
type DesktopAuthorizationExchange struct {
	CodeHash                           string
	StateHash                          string
	CodeChallenge                      string
	Issuer                             string
	ExpectedPublicJWK                  model.DesktopPublicJWK
	ExpectedKeyThumbprint              string
	DesktopRelease                     string
	DesktopBuildID                     string
	DesktopPlatform                    model.DesktopPlatform
	DesktopArchitecture                model.DesktopArchitecture
	DesktopRealtimeProtocol            int
	DesktopCompatibilityPolicyRevision int64
	AccessTokenHash                    string
	RefreshTokenHash                   string
	AccessLifetime                     time.Duration
	RefreshLifetime                    time.Duration
	IdleLifetime                       time.Duration
	AbsoluteLifetime                   time.Duration
	MaximumActive                      int
	Capabilities                       AccessDeploymentCapabilities
	AuditEventID                       string
	AuditAt                            int64
}

type DesktopAuthorizationExchangeResult struct {
	Session          *model.Session
	Registration     *model.DesktopRegistration
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	Denied           bool
}
