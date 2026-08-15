// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestConnectExamAttemptFingerprintBindsSessionAndNeverStoresRawCredential(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	credential := model.NewCredentialToken()
	principal := examAttemptPrincipal()
	invocation := NewInvocation(principal, model.RequestMetadata{RequestID: "connect-one"})
	command := ConnectExamAttemptCommand{SittingID: model.NewExamSittingID(), ContinuityCredential: credential, IdempotencyKey: "retry-key"}
	if _, err := application.ConnectExamAttempt(context.Background(), invocation, command); err != nil {
		t.Fatal(err)
	}
	first := *fake.connects[0].Idempotency
	if first.Operation != store.ExamAttemptConnectOperation || fake.connects[0].ContinuityCredential != credential {
		t.Fatalf("connect command = %#v idempotency=%#v", fake.connects[0], first)
	}

	principal.SessionID = model.NewSessionID()
	if _, err := application.ConnectExamAttempt(context.Background(), NewInvocation(principal, model.RequestMetadata{RequestID: "connect-two"}), command); err != nil {
		t.Fatal(err)
	}
	second := fake.connects[1].Idempotency
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("Connect fingerprint did not bind the authenticated Session")
	}
}

func TestCandidateExamAttemptFacadeConcealsAccessDenials(t *testing.T) {
	t.Parallel()
	for _, childCode := range []string{"exam.attempt.not_found", "exam.attempt.continuity_invalid"} {
		fake := &examAttemptUseCasesFake{err: &examattempt.Fault{Code: childCode}}
		application := &App{examAttempts: fake}
		_, err := application.GetCandidateExamPresentation(context.Background(),
			NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), CandidateExamAttemptAccess{})
		if !Is(err, "resource.not_found") {
			t.Fatalf("child=%s error=%v", childCode, err)
		}
	}
}

func TestTrustedConnectionCloseFacadeConcealsOwnershipMismatch(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{err: &examattempt.Fault{Code: "exam.attempt.not_found"}}
	application := &App{examAttempts: fake}
	_, err := application.CloseExamAttemptConnection(context.Background(),
		NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), CloseExamAttemptConnectionCommand{})
	if !Is(err, "resource.not_found") {
		t.Fatalf("error=%v", err)
	}
}

func TestConnectExamAttemptRequiresAnIdempotencyKeyBeforeChildCall(t *testing.T) {
	t.Parallel()
	fake := &examAttemptUseCasesFake{}
	application := &App{examAttempts: fake}
	_, err := application.ConnectExamAttempt(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}),
		ConnectExamAttemptCommand{SittingID: model.NewExamSittingID(), ContinuityCredential: model.NewCredentialToken()})
	if !Is(err, "idempotency.key_required") || len(fake.connects) != 0 {
		t.Fatalf("error=%v calls=%d", err, len(fake.connects))
	}
}

func examAttemptPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
}

type examAttemptUseCasesFake struct {
	connects []examattempt.ConnectCommand
	err      error
}

func (fake *examAttemptUseCasesFake) Connect(_ context.Context, _ examattempt.Call, command examattempt.ConnectCommand) (examattempt.ConnectionResult, error) {
	fake.connects = append(fake.connects, command)
	return examattempt.ConnectionResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) CloseConnection(context.Context, examattempt.Call, examattempt.CloseConnectionCommand) (examattempt.ConnectionClosedResult, error) {
	return examattempt.ConnectionClosedResult{}, fake.err
}

func (fake *examAttemptUseCasesFake) GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error) {
	return examattempt.Presentation{}, fake.err
}

func (fake *examAttemptUseCasesFake) ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error) {
	return examattempt.WorkspacePage{}, fake.err
}

func (fake *examAttemptUseCasesFake) OpenResource(context.Context, examattempt.Call, examattempt.CandidateAccess, model.ExamResourceID) (*examattempt.OpenedContent, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) OpenWorkspaceFile(context.Context, examattempt.Call, examattempt.CandidateAccess, model.AttemptWorkspaceEntryID) (*examattempt.OpenedContent, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) GetManaged(context.Context, examattempt.Call, examattempt.GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error) {
	return nil, fake.err
}

func (fake *examAttemptUseCasesFake) ListManaged(context.Context, examattempt.Call, examattempt.ListManagedAttemptsQuery) (examattempt.ManagedAttemptPage, error) {
	return examattempt.ManagedAttemptPage{}, fake.err
}

var _ examAttemptUseCases = (*examAttemptUseCasesFake)(nil)
