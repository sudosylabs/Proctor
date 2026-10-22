// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserInvitationTransactionStore interface {
	CreateInvitation(context.Context, *store.BrowserInvitationTransactionCreation) (*store.BrowserInvitationCreated, error)
	ResolveInvitation(context.Context, string, string) (*store.BrowserInvitationResolution, error)
}

// browserInvitationServiceDependencies are the collaborators of browserInvitationService;
// newBrowserInvitationService validates them once and browserInvitationService keeps them in deps.
type browserInvitationServiceDependencies struct {
	Transactions browserInvitationTransactionStore
	Institutions store.InstitutionStore
	Invitations  *invitationService
	Issuer       string
	NewProof     func() string
}

type browserInvitationService struct {
	deps browserInvitationServiceDependencies
}

type StartBrowserInvitationCommand struct {
	Claim  string
	Source string
}

type BrowserInvitationRequirement string

const (
	BrowserInvitationRequirementAccount BrowserInvitationRequirement = "account"
	BrowserInvitationRequirementSession BrowserInvitationRequirement = "session"
)

type BrowserInvitationStart struct {
	Handle       string
	BrowserProof string
	Purpose      model.InvitationPurpose
	Requirement  BrowserInvitationRequirement
	ExpiresAt    int64
}

type BrowserInvitationAcceptanceCommand struct {
	Handle, BrowserProof, Password, Username, DisplayName, FirstName, LastName, Locale, Timezone, Source string
}

type BrowserInvitationSessionAcceptanceCommand struct {
	Handle, BrowserProof, Source string
}

func newBrowserInvitationService(deps browserInvitationServiceDependencies) (*browserInvitationService, error) {
	if deps.Transactions == nil || deps.Institutions == nil || deps.Invitations == nil ||
		model.ValidateBrowserAuthenticationIssuer(deps.Issuer, true) != nil || deps.NewProof == nil {
		return nil, errors.New("browser invitation dependencies are invalid")
	}
	return &browserInvitationService{deps: deps}, nil
}

func (s *browserInvitationService) StartBrowserInvitation(ctx context.Context, _ invocation.Invocation, command StartBrowserInvitationCommand) (*BrowserInvitationStart, error) {
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, apperror.New("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	if err := s.deps.Invitations.deps.Attempts.Check(ctx, claimHash, command.Source); err != nil {
		return nil, err
	}
	invitation, err := s.deps.Invitations.deps.Store.GetByClaimHash(ctx, claimHash)
	if err != nil || invitation == nil {
		return nil, invalidInvitationError(err)
	}
	if invitation.State != model.InvitationPending {
		return nil, apperror.New("invitation.invalid")
	}
	var requirement BrowserInvitationRequirement
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass, model.InvitationPurposeTeacherAcademicUnit:
		requirement = BrowserInvitationRequirementAccount
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		requirement = BrowserInvitationRequirementSession
	default:
		return nil, apperror.New("invitation.invalid")
	}
	institution, err := s.deps.Institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, apperror.New("invitation.unavailable").Wrap(err)
	}
	handle, proof := s.deps.NewProof(), s.deps.NewProof()
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) || handle == proof {
		return nil, apperror.New("invitation.unavailable")
	}
	creation := &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: institution.ID, Issuer: s.deps.Issuer,
		InvitationID: invitation.ID, InvitationPurpose: invitation.Purpose, InvitationClaimHash: claimHash,
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
	saved, err := s.deps.Transactions.CreateInvitation(ctx, creation)
	if err != nil || saved == nil {
		return nil, browserInvitationStoreErrors.Translate(err)
	}
	if saved.ID != creation.ID || saved.ExpiresAt.IsZero() || saved.ExpiresAt.After(invitation.ExpiresAt) ||
		(invitation.IntendedEndsAt.Valid && saved.ExpiresAt.After(invitation.IntendedEndsAt.Time)) {
		return nil, apperror.New("invitation.unavailable")
	}
	return &BrowserInvitationStart{
		Handle: handle, BrowserProof: proof, Purpose: invitation.Purpose,
		Requirement: requirement, ExpiresAt: saved.ExpiresAt.UnixMilli(),
	}, nil
}

func (s *browserInvitationService) AcceptBrowserInvitation(ctx context.Context, invocation invocation.Invocation, command BrowserInvitationAcceptanceCommand) (*InvitationAcceptanceView, error) {
	resolution, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.deps.Invitations.deps.Attempts.Check(ctx, model.HashToken(command.Handle), command.Source); err != nil {
		return nil, err
	}
	base := AcceptStudentClassInvitationCommand{
		Password: command.Password, Username: command.Username, DisplayName: command.DisplayName,
		FirstName: command.FirstName, LastName: command.LastName, Locale: command.Locale,
		Timezone: command.Timezone, Source: command.Source,
		browserTransaction: browserInvitationProof(resolution, command.Handle, command.BrowserProof),
	}
	var accepted *InvitationAcceptanceView
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		accepted, err = s.deps.Invitations.acceptStudentClassByClaimHash(ctx, invocation, base, resolution.InvitationClaimHash)
	case model.InvitationPurposeTeacherAcademicUnit:
		accepted, err = s.deps.Invitations.acceptTeacherAcademicUnitByClaimHash(ctx, invocation, AcceptTeacherAcademicUnitInvitationCommand(base), resolution.InvitationClaimHash)
	default:
		return nil, apperror.New("invitation.invalid")
	}
	if err != nil {
		return nil, err
	}
	return accepted, nil
}

func (s *browserInvitationService) AcceptBrowserInvitationWithSession(ctx context.Context, invocation invocation.Invocation, command BrowserInvitationSessionAcceptanceCommand) (*InvitationAcceptanceView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess ||
		principal.ClientType != model.SessionClientWeb {
		return nil, apperror.New("invitation.invalid")
	}
	resolution, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.deps.Invitations.deps.Attempts.Check(ctx, model.HashToken(command.Handle), command.Source); err != nil {
		return nil, err
	}
	if invitation.Purpose != model.InvitationPurposeAcademicUnitRole && invitation.Purpose != model.InvitationPurposeInstitutionRole {
		return nil, apperror.New("invitation.invalid")
	}
	accepted, err := s.deps.Invitations.acceptScopedRoleByClaimHash(
		ctx, invocation, resolution.InvitationClaimHash, invitation.Purpose,
		browserInvitationProof(resolution, command.Handle, command.BrowserProof),
	)
	if err != nil {
		return nil, err
	}
	return accepted, nil
}

func (s *browserInvitationService) resolve(ctx context.Context, handle, proof string) (*store.BrowserInvitationResolution, *model.Invitation, error) {
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) {
		return nil, nil, apperror.New("invitation.invalid")
	}
	handleHash, proofHash := model.HashToken(handle), model.HashToken(proof)
	resolution, err := s.deps.Transactions.ResolveInvitation(ctx, handleHash, proofHash)
	if err != nil || resolution == nil {
		return nil, nil, browserInvitationStoreErrors.Translate(err)
	}
	if !resolution.ID.IsValid() || !resolution.InvitationID.IsValid() || !model.IsValidTokenHash(resolution.InvitationClaimHash) {
		return nil, nil, apperror.New("invitation.unavailable")
	}
	invitation, err := s.deps.Invitations.deps.Store.GetByClaimHash(ctx, resolution.InvitationClaimHash)
	if err != nil || invitation == nil || invitation.ID != resolution.InvitationID {
		return nil, nil, browserInvitationStoreErrors.Translate(err)
	}
	return resolution, invitation, nil
}

func browserInvitationProof(resolution *store.BrowserInvitationResolution, handle, proof string) *store.BrowserInvitationTransactionProof {
	return &store.BrowserInvitationTransactionProof{
		ID: resolution.ID, HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
}

var browserInvitationStoreErrors = apperror.StoreTable{
	NotFound:    "invitation.invalid",
	Conflict:    "invitation.invalid",
	Unavailable: "invitation.unavailable",
}
