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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserInvitationTransactionStore interface {
	CreateInvitation(context.Context, *store.BrowserInvitationTransactionCreation) (*store.BrowserInvitationCreated, error)
	ResolveInvitation(context.Context, string, string) (*store.BrowserInvitationResolution, error)
}

type browserInvitationService struct {
	transactions browserInvitationTransactionStore
	institutions store.InstitutionStore
	invitations  *invitationService
	issuer       string
	newProof     func() string
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

func newBrowserInvitationService(
	transactions browserInvitationTransactionStore,
	institutions store.InstitutionStore,
	invitations *invitationService,
	issuer string,
	newProof func() string,
) (*browserInvitationService, error) {
	if transactions == nil || institutions == nil || invitations == nil ||
		model.ValidateBrowserAuthenticationIssuer(issuer, true) != nil || newProof == nil {
		return nil, errors.New("browser invitation dependencies are invalid")
	}
	return &browserInvitationService{
		transactions: transactions, institutions: institutions, invitations: invitations,
		issuer: issuer, newProof: newProof,
	}, nil
}

func (a *App) StartBrowserInvitation(ctx context.Context, invocation Invocation, command StartBrowserInvitationCommand) (*BrowserInvitationStart, error) {
	if a == nil || a.browserInvitations == nil {
		return nil, apperror.New("invitation.unavailable")
	}
	return a.browserInvitations.Start(ctx, invocation, command)
}

func (a *App) AcceptBrowserInvitation(ctx context.Context, invocation Invocation, command BrowserInvitationAcceptanceCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.browserInvitations == nil {
		return nil, apperror.New("invitation.unavailable")
	}
	return a.browserInvitations.AcceptLocal(ctx, invocation, command)
}

func (a *App) AcceptBrowserInvitationWithSession(ctx context.Context, invocation Invocation, command BrowserInvitationSessionAcceptanceCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.browserInvitations == nil {
		return nil, apperror.New("invitation.unavailable")
	}
	return a.browserInvitations.AcceptSession(ctx, invocation, command)
}

func (s *browserInvitationService) Start(ctx context.Context, _ Invocation, command StartBrowserInvitationCommand) (*BrowserInvitationStart, error) {
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, apperror.New("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	if err := s.invitations.attempts.Check(ctx, claimHash, command.Source); err != nil {
		return nil, err
	}
	invitation, err := s.invitations.store.GetByClaimHash(ctx, claimHash)
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
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, apperror.New("invitation.unavailable").Wrap(err)
	}
	handle, proof := s.newProof(), s.newProof()
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) || handle == proof {
		return nil, apperror.New("invitation.unavailable")
	}
	creation := &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: institution.ID, Issuer: s.issuer,
		InvitationID: invitation.ID, InvitationPurpose: invitation.Purpose, InvitationClaimHash: claimHash,
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
	saved, err := s.transactions.CreateInvitation(ctx, creation)
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

func (s *browserInvitationService) AcceptLocal(ctx context.Context, invocation Invocation, command BrowserInvitationAcceptanceCommand) (*InvitationAcceptanceView, error) {
	resolution, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.invitations.attempts.Check(ctx, model.HashToken(command.Handle), command.Source); err != nil {
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
		accepted, err = s.invitations.acceptStudentClassByClaimHash(ctx, invocation, base, resolution.InvitationClaimHash)
	case model.InvitationPurposeTeacherAcademicUnit:
		accepted, err = s.invitations.acceptTeacherAcademicUnitByClaimHash(ctx, invocation, AcceptTeacherAcademicUnitInvitationCommand(base), resolution.InvitationClaimHash)
	default:
		return nil, apperror.New("invitation.invalid")
	}
	if err != nil {
		return nil, err
	}
	return accepted, nil
}

func (s *browserInvitationService) AcceptSession(ctx context.Context, invocation Invocation, command BrowserInvitationSessionAcceptanceCommand) (*InvitationAcceptanceView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess ||
		principal.ClientType != model.SessionClientWeb {
		return nil, apperror.New("invitation.invalid")
	}
	resolution, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.invitations.attempts.Check(ctx, model.HashToken(command.Handle), command.Source); err != nil {
		return nil, err
	}
	if invitation.Purpose != model.InvitationPurposeAcademicUnitRole && invitation.Purpose != model.InvitationPurposeInstitutionRole {
		return nil, apperror.New("invitation.invalid")
	}
	accepted, err := s.invitations.acceptScopedRoleByClaimHash(
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
	resolution, err := s.transactions.ResolveInvitation(ctx, handleHash, proofHash)
	if err != nil || resolution == nil {
		return nil, nil, browserInvitationStoreErrors.Translate(err)
	}
	if !resolution.ID.IsValid() || !resolution.InvitationID.IsValid() || !model.IsValidTokenHash(resolution.InvitationClaimHash) {
		return nil, nil, apperror.New("invitation.unavailable")
	}
	invitation, err := s.invitations.store.GetByClaimHash(ctx, resolution.InvitationClaimHash)
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
