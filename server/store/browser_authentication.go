// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

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
	CreateDesktopAuthorization(context.Context, *model.BrowserAuthenticationTransaction) (*model.BrowserAuthenticationTransaction, error)
	CreateInvitation(context.Context, *BrowserInvitationTransactionCreation) (*model.BrowserAuthenticationTransaction, error)
	ResolveInvitation(context.Context, string, string) (*model.BrowserAuthenticationTransaction, error)
	IssueCode(context.Context, *DesktopAuthorizationCodeIssue) (*model.BrowserAuthenticationTransaction, error)
	Cancel(context.Context, *DesktopAuthorizationCancellation) (*model.BrowserAuthenticationTransaction, error)
	Exchange(context.Context, *DesktopAuthorizationExchange) (*DesktopAuthorizationExchangeResult, error)
	Maintain(context.Context, int) (*BrowserAuthenticationMaintenanceResult, error)
}
