// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserInvitationTransactionStoreFake struct {
	created       *store.BrowserInvitationTransactionCreation
	createdAt     time.Time
	expiresAt     time.Time
	createResult  *model.BrowserAuthenticationTransaction
	createErr     error
	createNil     bool
	resolveResult *model.BrowserAuthenticationTransaction
	resolveErr    error
	resolveNil    bool
	resolveCalls  int
}

func (s *browserInvitationTransactionStoreFake) CreateInvitation(_ context.Context, input *store.BrowserInvitationTransactionCreation) (*model.BrowserAuthenticationTransaction, error) {
	copy := *input
	s.created = &copy
	if s.createNil {
		return nil, nil
	}
	if s.createResult != nil || s.createErr != nil {
		return s.createResult, s.createErr
	}
	createdAt := s.createdAt
	if createdAt.IsZero() {
		createdAt = model.NowUTC()
	}
	expiresAt := s.expiresAt
	if expiresAt.IsZero() {
		expiresAt = createdAt.Add(model.BrowserAuthenticationTransactionLifetime)
	}
	transaction := &model.BrowserAuthenticationTransaction{
		Purpose:       model.BrowserAuthenticationPurposeInvitationAcceptance,
		InstitutionID: input.InstitutionID, Issuer: input.Issuer, InvitationID: input.InvitationID,
		HandleHash: input.HandleHash, BrowserProofHash: input.BrowserProofHash,
		InvitationClaimHash: input.InvitationClaimHash, ClientType: model.SessionClientWeb, ExpiresAt: expiresAt,
	}
	transaction.PrepareCreate(input.ID, createdAt)
	return transaction, nil
}

func (s *browserInvitationTransactionStoreFake) ResolveInvitation(context.Context, string, string) (*model.BrowserAuthenticationTransaction, error) {
	s.resolveCalls++
	if s.resolveNil {
		return nil, nil
	}
	if s.resolveResult != nil || s.resolveErr != nil {
		return s.resolveResult, s.resolveErr
	}
	return nil, &store.ErrNotFound{Resource: "browser authentication transaction"}
}

type browserInvitationStoreFake struct {
	store.InvitationStore
	invitation *model.Invitation
}

func (s browserInvitationStoreFake) GetByClaimHash(context.Context, string) (*model.Invitation, error) {
	return s.invitation, nil
}

type browserInvitationInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
}

func (s browserInvitationInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, nil
}

type browserInvitationAttemptLimiterFake struct{ checks int }

func (a *browserInvitationAttemptLimiterFake) Check(context.Context, string, string) error {
	a.checks++
	return nil
}

func TestBrowserInvitationStartReplacesRawClaimWithTwoHashedProofs(t *testing.T) {
	t.Parallel()
	now := model.TimeUTC(time.Now())
	claim := model.NewCredentialToken()
	handle := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	invitation := &model.Invitation{
		ID: model.NewInvitationID(), Purpose: model.InvitationPurposeStudentClass,
		State: model.InvitationPending, ClaimHash: model.HashInvitationClaim(claim),
		ExpiresAt: now.Add(time.Minute),
	}
	attempts := &browserInvitationAttemptLimiterFake{}
	invitationService := &invitationService{
		store: browserInvitationStoreFake{invitation: invitation}, attempts: attempts,
	}
	transactions := &browserInvitationTransactionStoreFake{createdAt: now, expiresAt: invitation.ExpiresAt}
	proofs := []string{handle, proof}
	service, err := newBrowserInvitationService(
		transactions,
		browserInvitationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		invitationService,
		"https://proctor.example.edu",
		func() string {
			value := proofs[0]
			proofs = proofs[1:]
			return value
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), Invocation{}, StartBrowserInvitationCommand{Claim: claim, Source: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Handle != handle || started.BrowserProof != proof || started.Requirement != BrowserInvitationRequirementAccount ||
		started.ExpiresAt != invitation.ExpiresAt.UnixMilli() || attempts.checks != 1 {
		t.Fatalf("start = %#v checks=%d", started, attempts.checks)
	}
	created := transactions.created
	if created == nil || created.InvitationID != invitation.ID || created.InvitationPurpose != invitation.Purpose ||
		created.InvitationClaimHash != model.HashInvitationClaim(claim) ||
		created.HandleHash != model.HashToken(handle) || created.BrowserProofHash != model.HashToken(proof) {
		t.Fatalf("created transaction = %#v", created)
	}
	if created.HandleHash == handle || created.BrowserProofHash == proof || created.InvitationClaimHash == claim {
		t.Fatal("raw browser Invitation credential was persisted")
	}
}

func TestBrowserInvitationStoreNilResultsFailClosed(t *testing.T) {
	t.Parallel()
	now := model.TimeUTC(time.Now())
	claim := model.NewCredentialToken()
	invitation := &model.Invitation{
		ID: model.NewInvitationID(), Purpose: model.InvitationPurposeStudentClass,
		State: model.InvitationPending, ClaimHash: model.HashInvitationClaim(claim),
		ExpiresAt: now.Add(time.Minute),
	}
	invitationService := &invitationService{
		store: browserInvitationStoreFake{invitation: invitation}, attempts: &browserInvitationAttemptLimiterFake{},
	}
	transactions := &browserInvitationTransactionStoreFake{createNil: true}
	proofs := []string{model.NewCredentialToken(), model.NewCredentialToken()}
	service, err := newBrowserInvitationService(
		transactions,
		browserInvitationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		invitationService,
		"https://proctor.example.edu",
		func() string {
			value := proofs[0]
			proofs = proofs[1:]
			return value
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// An explicit nil result from an otherwise successful dependency must not
	// become a successful application response or a later nil dereference.
	_, err = service.Start(context.Background(), Invocation{}, StartBrowserInvitationCommand{Claim: claim, Source: "source"})
	if !Is(err, "invitation.unavailable") {
		t.Fatalf("Start error = %v, want invitation.unavailable", err)
	}

	transactions.createNil = false
	transactions.resolveNil = true
	_, _, err = service.resolve(context.Background(), model.NewCredentialToken(), model.NewCredentialToken())
	if !Is(err, "invitation.unavailable") {
		t.Fatalf("resolve error = %v, want invitation.unavailable", err)
	}
}

func TestBrowserInvitationSessionAcceptanceRequiresWebSession(t *testing.T) {
	t.Parallel()
	now := model.NowUTC()
	transactions := &browserInvitationTransactionStoreFake{}
	service, err := newBrowserInvitationService(
		transactions,
		browserInvitationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		&invitationService{
			store: browserInvitationStoreFake{}, attempts: &browserInvitationAttemptLimiterFake{},
		},
		"https://proctor.example.edu",
		model.NewCredentialToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: now,
	}
	_, err = service.AcceptSession(
		context.Background(),
		NewInvocation(principal, model.RequestMetadata{}),
		BrowserInvitationSessionAcceptanceCommand{
			Handle: model.NewCredentialToken(), BrowserProof: model.NewCredentialToken(), Source: "source",
		},
	)
	if !Is(err, "invitation.invalid") || transactions.resolveCalls != 0 {
		t.Fatalf("AcceptSession error=%v resolve_calls=%d, want invitation.invalid before persistence", err, transactions.resolveCalls)
	}
}
