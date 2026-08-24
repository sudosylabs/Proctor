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
	ID                           model.BrowserAuthenticationTransactionID
	InstitutionID                model.InstitutionID
	Issuer                       string
	HandleHash                   string
	BrowserProofHash             string
	StateHash                    string
	CallbackURL                  string
	CodeChallenge                string
	ExpectedAuthenticationMethod string
	ExpectedProviderID           string
	DeviceID                     string
	DeviceName                   string
	Lifetime                     time.Duration
}

type DesktopAuthorizationCreated struct {
	ID        model.BrowserAuthenticationTransactionID
	ExpiresAt time.Time
}

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
	CreateInvitation(context.Context, *BrowserInvitationTransactionCreation) (*BrowserInvitationCreated, error)
	ResolveInvitation(context.Context, string, string) (*BrowserInvitationResolution, error)
	IssueCode(context.Context, *DesktopAuthorizationCodeIssue) (*DesktopAuthorizationCodeIssued, error)
	Cancel(context.Context, *DesktopAuthorizationCancellation) error
	Exchange(context.Context, *DesktopAuthorizationExchange) (*DesktopAuthorizationExchangeResult, error)
	Maintain(context.Context, int) (*BrowserAuthenticationMaintenanceResult, error)
}
