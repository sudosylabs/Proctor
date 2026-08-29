// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// DesktopAuthorizationCreation is the closed input for creating a pending
// desktop browser handoff. The Store owns the initial state and all timestamps.
type DesktopAuthorizationCreation struct {
	ID                      model.BrowserAuthenticationTransactionID
	InstitutionID           model.InstitutionID
	Issuer                  string
	HandleHash              string
	BrowserProofHash        string
	StateHash               string
	CallbackURL             string
	CodeChallenge           string
	DeviceID                string
	DeviceName              string
	ProposedPublicJWK       model.DesktopPublicJWK
	ProposedKeyThumbprint   string
	DesktopRelease          string
	DesktopBuildID          string
	DesktopPlatform         model.DesktopPlatform
	DesktopArchitecture     model.DesktopArchitecture
	DesktopRealtimeProtocol int
	Lifetime                time.Duration
}

type DesktopAuthorizationCreated struct {
	ID        model.BrowserAuthenticationTransactionID
	ExpiresAt time.Time
}

// DesktopAuthorizationBinding atomically consumes the URL handle and initial
// fragment proof and replaces them with one server-owned browser binding.
type DesktopAuthorizationBinding struct {
	HandleHash       string
	BrowserProofHash string
	StateHash        string
	BindingHash      string
}

type DesktopAuthorizationBound struct {
	ExpiresAt time.Time
}

// DesktopAuthorizationContext is the safe current state selected only by the
// opaque browser-binding cookie. Authentication proof details remain durable
// but are not projected to the browser.
type DesktopAuthorizationContext struct {
	ID         model.BrowserAuthenticationTransactionID
	State      model.BrowserAuthenticationState
	UserID     model.UserID
	DeviceName string
	ExpiresAt  time.Time
}

// DesktopAuthorizationAuthentication binds one proved identity to the exact
// browser transaction. The Store rechecks active User, authentication policy,
// external-identity provenance, and the active-Attempt Session lock together.
type DesktopAuthorizationAuthentication struct {
	BindingHash              string
	TransactionID            model.BrowserAuthenticationTransactionID
	UserID                   model.UserID
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       model.ExternalIdentityID
	AuthenticationStrength   model.AuthenticationStrength
	AuthenticatedAt          int64
	MFACompletedAt           int64
	Capabilities             AccessDeploymentCapabilities
}

type DesktopAuthorizationAuthenticationResult struct {
	Denied bool
}

type DesktopAuthorizationAccountReset struct{ BindingHash string }

// BrowserInvitationTransactionProof lets an Invitation acceptance aggregate
// consume the already-proved browser transaction in the same database commit.
// The accepting Store supplies the authoritative Invitation and User.
type BrowserInvitationTransactionProof struct {
	ID               model.BrowserAuthenticationTransactionID
	HandleHash       string
	BrowserProofHash string
}

// BrowserInvitationTransactionCreation is the closed input for atomically
// rechecking one Invitation and creating its browser handoff. The Store owns
// the authoritative creation time and deadline calculation.
type BrowserInvitationTransactionCreation struct {
	ID                  model.BrowserAuthenticationTransactionID
	InstitutionID       model.InstitutionID
	Issuer              string
	InvitationID        model.InvitationID
	InvitationPurpose   model.InvitationPurpose
	InvitationClaimHash string
	HandleHash          string
	BrowserProofHash    string
}

type BrowserInvitationCreated struct {
	ID        model.BrowserAuthenticationTransactionID
	ExpiresAt time.Time
}

type BrowserInvitationResolution struct {
	ID                  model.BrowserAuthenticationTransactionID
	InvitationID        model.InvitationID
	InvitationClaimHash string
}

// BrowserAuthenticationMaintenanceResult describes one bounded, authoritative
// database-clock maintenance page. More reports that immediately eligible work
// remains for a subsequent page or scheduled occurrence.
type BrowserAuthenticationMaintenanceResult struct {
	Expired int
	Purged  int
	More    bool
}

// BrowserAuthenticationStore owns the shared durable browser-authentication
// transaction aggregate used by desktop authorization and hosted Invitation
// acceptance. No raw browser credential crosses this boundary.
type BrowserAuthenticationStore interface {
	CreateDesktopAuthorization(context.Context, *DesktopAuthorizationCreation) (*DesktopAuthorizationCreated, error)
	BindDesktopAuthorization(context.Context, *DesktopAuthorizationBinding) (*DesktopAuthorizationBound, error)
	GetDesktopAuthorizationContext(context.Context, string) (*DesktopAuthorizationContext, error)
	AuthenticateDesktopAuthorization(context.Context, *DesktopAuthorizationAuthentication) (*DesktopAuthorizationAuthenticationResult, error)
	ResetDesktopAuthorizationAccount(context.Context, *DesktopAuthorizationAccountReset) error
	CreateInvitation(context.Context, *BrowserInvitationTransactionCreation) (*BrowserInvitationCreated, error)
	ResolveInvitation(context.Context, string, string) (*BrowserInvitationResolution, error)
	IssueCode(context.Context, *DesktopAuthorizationCodeIssue) (*DesktopAuthorizationCodeIssued, error)
	Cancel(context.Context, *DesktopAuthorizationCancellation) error
	ResolveDesktopAuthorizationExchange(context.Context, *DesktopAuthorizationExchangeProof) (model.BrowserAuthenticationTransactionID, error)
	Exchange(context.Context, *DesktopAuthorizationExchange) (*DesktopAuthorizationExchangeResult, error)
	Maintain(context.Context, int) (*BrowserAuthenticationMaintenanceResult, error)
}
