// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserInvitationTransactionStore interface {
	CreateInvitation(context.Context, *store.BrowserInvitationTransactionCreation) (*model.BrowserAuthenticationTransaction, error)
	ResolveInvitation(context.Context, string, string) (*model.BrowserAuthenticationTransaction, error)
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
		return nil, NewError("invitation.unavailable")
	}
	return a.browserInvitations.Start(ctx, invocation, command)
}

func (a *App) AcceptBrowserInvitation(ctx context.Context, invocation Invocation, command BrowserInvitationAcceptanceCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.browserInvitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.browserInvitations.AcceptLocal(ctx, invocation, command)
}

func (a *App) AcceptBrowserInvitationWithSession(ctx context.Context, invocation Invocation, command BrowserInvitationSessionAcceptanceCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.browserInvitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.browserInvitations.AcceptSession(ctx, invocation, command)
}

func (s *browserInvitationService) Start(ctx context.Context, _ Invocation, command StartBrowserInvitationCommand) (*BrowserInvitationStart, error) {
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, NewError("invitation.invalid")
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
		return nil, NewError("invitation.invalid")
	}
	var requirement BrowserInvitationRequirement
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass, model.InvitationPurposeTeacherAcademicUnit:
		requirement = BrowserInvitationRequirementAccount
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		requirement = BrowserInvitationRequirementSession
	default:
		return nil, NewError("invitation.invalid")
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil {
		return nil, NewError("invitation.unavailable").Wrap(err)
	}
	handle, proof := s.newProof(), s.newProof()
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) || handle == proof {
		return nil, NewError("invitation.unavailable")
	}
	creation := &store.BrowserInvitationTransactionCreation{
		ID: model.NewBrowserAuthenticationTransactionID(), InstitutionID: institution.ID, Issuer: s.issuer,
		InvitationID: invitation.ID, InvitationPurpose: invitation.Purpose, InvitationClaimHash: claimHash,
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
	saved, err := s.transactions.CreateInvitation(ctx, creation)
	if err != nil || saved == nil {
		return nil, browserInvitationStoreError(err)
	}
	if saved.Validate() != nil || saved.ID != creation.ID || saved.InstitutionID != creation.InstitutionID ||
		saved.Issuer != creation.Issuer || saved.InvitationID != creation.InvitationID ||
		saved.InvitationClaimHash != creation.InvitationClaimHash || saved.HandleHash != creation.HandleHash ||
		saved.BrowserProofHash != creation.BrowserProofHash || saved.Purpose != model.BrowserAuthenticationPurposeInvitationAcceptance ||
		saved.ClientType != model.SessionClientWeb || saved.ExpiresAt.After(invitation.ExpiresAt) ||
		(invitation.IntendedEndsAt.Valid && saved.ExpiresAt.After(invitation.IntendedEndsAt.Time)) {
		return nil, NewError("invitation.unavailable")
	}
	return &BrowserInvitationStart{
		Handle: handle, BrowserProof: proof, Purpose: invitation.Purpose,
		Requirement: requirement, ExpiresAt: saved.ExpiresAt.UnixMilli(),
	}, nil
}

func (s *browserInvitationService) AcceptLocal(ctx context.Context, invocation Invocation, command BrowserInvitationAcceptanceCommand) (*InvitationAcceptanceView, error) {
	transaction, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.invitations.attempts.Check(ctx, transaction.HandleHash, command.Source); err != nil {
		return nil, err
	}
	base := AcceptStudentClassInvitationCommand{
		Password: command.Password, Username: command.Username, DisplayName: command.DisplayName,
		FirstName: command.FirstName, LastName: command.LastName, Locale: command.Locale,
		Timezone: command.Timezone, Source: command.Source,
		browserTransaction: browserInvitationProof(transaction, command.Handle, command.BrowserProof),
	}
	var accepted *InvitationAcceptanceView
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		accepted, err = s.invitations.acceptStudentClassByClaimHash(ctx, invocation, base, transaction.InvitationClaimHash)
	case model.InvitationPurposeTeacherAcademicUnit:
		accepted, err = s.invitations.acceptTeacherAcademicUnitByClaimHash(ctx, invocation, AcceptTeacherAcademicUnitInvitationCommand(base), transaction.InvitationClaimHash)
	default:
		return nil, NewError("invitation.invalid")
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
		return nil, NewError("invitation.invalid")
	}
	transaction, invitation, err := s.resolve(ctx, command.Handle, command.BrowserProof)
	if err != nil {
		return nil, err
	}
	if err = s.invitations.attempts.Check(ctx, transaction.HandleHash, command.Source); err != nil {
		return nil, err
	}
	if invitation.Purpose != model.InvitationPurposeAcademicUnitRole && invitation.Purpose != model.InvitationPurposeInstitutionRole {
		return nil, NewError("invitation.invalid")
	}
	accepted, err := s.invitations.acceptScopedRoleByClaimHash(
		ctx, invocation, transaction.InvitationClaimHash, invitation.Purpose,
		browserInvitationProof(transaction, command.Handle, command.BrowserProof),
	)
	if err != nil {
		return nil, err
	}
	return accepted, nil
}

func (s *browserInvitationService) resolve(ctx context.Context, handle, proof string) (*model.BrowserAuthenticationTransaction, *model.Invitation, error) {
	if !model.IsValidCredentialToken(handle) || !model.IsValidCredentialToken(proof) {
		return nil, nil, NewError("invitation.invalid")
	}
	handleHash, proofHash := model.HashToken(handle), model.HashToken(proof)
	transaction, err := s.transactions.ResolveInvitation(ctx, handleHash, proofHash)
	if err != nil || transaction == nil {
		return nil, nil, browserInvitationStoreError(err)
	}
	if transaction.Validate() != nil || transaction.Purpose != model.BrowserAuthenticationPurposeInvitationAcceptance ||
		transaction.ClientType != model.SessionClientWeb || transaction.HandleHash != handleHash ||
		transaction.BrowserProofHash != proofHash || !model.IsValidTokenHash(transaction.InvitationClaimHash) {
		return nil, nil, NewError("invitation.unavailable")
	}
	invitation, err := s.invitations.store.GetByClaimHash(ctx, transaction.InvitationClaimHash)
	if err != nil || invitation == nil || invitation.ID != transaction.InvitationID {
		return nil, nil, browserInvitationStoreError(err)
	}
	return transaction, invitation, nil
}

func browserInvitationProof(transaction *model.BrowserAuthenticationTransaction, handle, proof string) *store.BrowserInvitationTransactionProof {
	return &store.BrowserInvitationTransactionProof{
		ID: transaction.ID, HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
	}
}

func browserInvitationStoreError(err error) error {
	if err == nil {
		return NewError("invitation.unavailable")
	}
	if store.IsNotFound(err) || store.IsConflict(err) {
		return NewError("invitation.invalid").Wrap(err)
	}
	return NewError("invitation.unavailable").Wrap(err)
}
