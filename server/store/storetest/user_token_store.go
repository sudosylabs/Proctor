// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/storetest/token_store.go.
// Proctor additionally verifies target binding, replacement, single-use
// concurrency, transactional audits, password mutation, and session reset.

package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestUserTokenStore(t *testing.T, ss store.Store) {
	t.Run("IssueReplacesPriorToken", func(t *testing.T) {
		testUserTokenIssueReplacesPriorToken(t, ss)
	})
	t.Run("EmailVerificationIsTargetBoundAndSingleUse", func(t *testing.T) {
		testEmailVerificationIsTargetBoundAndSingleUse(t, ss)
	})
	t.Run("PasswordResetRevokesSessionsAndAudits", func(t *testing.T) {
		testPasswordResetRevokesSessionsAndAudits(t, ss)
	})
	t.Run("ConcurrentConsumptionHasOneWinner", func(t *testing.T) {
		testUserTokenConcurrentConsumption(t, ss)
	})
	t.Run("AuditFailureRollsBackCredentialState", func(t *testing.T) {
		testUserTokenAuditFailureRollsBack(t, ss)
	})
}

func testUserTokenIssueReplacesPriorToken(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	first := newUserToken(user, model.UserTokenEmailVerification)
	first, err := ss.UserToken().Issue(
		ctx,
		first,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	second := newUserToken(user, model.UserTokenEmailVerification)
	second, err = ss.UserToken().Issue(
		ctx,
		second,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	gotFirst, err := ss.UserToken().GetByHash(ctx, first.TokenHash, first.Purpose)
	requireNoError(t, err)
	if !gotFirst.ArchivedAt.Valid {
		t.Fatalf("prior token remained active: %#v", gotFirst)
	}
	gotSecond, err := ss.UserToken().GetByHash(ctx, second.TokenHash, second.Purpose)
	requireNoError(t, err)
	if gotSecond.ArchivedAt.Valid || gotSecond.Target != user.Email {
		t.Fatalf("replacement token = %#v", gotSecond)
	}
}

func testEmailVerificationIsTargetBoundAndSingleUse(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := ss.UserToken().Issue(
		ctx,
		token,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	result, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	requireNoError(t, err)
	if !result.User.EmailVerified || result.User.Revision != user.Revision+1 || result.Token.ConsumedAt.Millis() != now {
		t.Fatalf("verification result = %#v", result)
	}
	if _, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	); !store.IsNotFound(err) {
		t.Fatalf("second consumption error = %v, want not found", err)
	}

	changedUser, _ := saveLocalUser(t, ctx, ss)
	changedToken := newUserToken(changedUser, model.UserTokenEmailVerification)
	changedToken, err = ss.UserToken().Issue(
		ctx,
		changedToken,
		userTokenAudit(
			"authentication.email_verification.request",
			changedUser.ID.String(),
			institution.ID.String(),
		),
	)
	requireNoError(t, err)
	changedUser.Email = model.NewId() + "@changed.example.edu"
	_, err = ss.User().Update(ctx, changedUser)
	requireNoError(t, err)
	if _, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		changedToken.TokenHash,
		now+2,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	); !store.IsNotFound(err) {
		t.Fatalf("changed-target consumption error = %v, want not found", err)
	}
}

func testPasswordResetRevokesSessionsAndAudits(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, credential := saveLocalUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := ss.UserToken().Issue(
		ctx,
		token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	now := max(model.MillisFromTime(token.CreatedAt), session.CreateAt) + 100
	result, err := ss.UserToken().ConsumePasswordReset(
		ctx,
		token.TokenHash,
		"new-encoded-password-hash",
		now,
		"password reset",
		userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String()),
	)
	requireNoError(t, err)
	if result.PasswordCredential.ID != credential.ID ||
		result.PasswordCredential.PasswordHash != "new-encoded-password-hash" ||
		model.MillisFromTime(result.PasswordCredential.PasswordChangedAt) != now ||
		len(result.RevokedSessions) != 1 ||
		len(result.RevokedAccessHashes) != 2 {
		t.Fatalf("password reset result = %#v", result)
	}
	resolved, resolvedSession, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if resolved.RevokedAt != now || resolvedSession.RevokedAt != now {
		t.Fatalf("reset session remained active: %#v %#v", resolved, resolvedSession)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.password_reset.complete",
		Limit:  10,
	})
	requireNoError(t, err)
	if len(audits) != 1 ||
		audits[0].Resource.Id != user.ID.String() ||
		audits[0].Status != model.AuditStatusSuccess {
		t.Fatalf("password reset audit = %#v", audits)
	}
}

func testUserTokenConcurrentConsumption(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := ss.UserToken().Issue(
		ctx,
		token,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	start := make(chan struct{})
	errorsByAttempt := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, consumeErr := ss.UserToken().ConsumeEmailVerification(
				ctx,
				token.TokenHash,
				now,
				userTokenCompletionAudit(
					"authentication.email_verification.complete",
					institution.ID.String(),
				),
			)
			errorsByAttempt <- consumeErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByAttempt)
	successes := 0
	notFound := 0
	for consumeErr := range errorsByAttempt {
		switch {
		case consumeErr == nil:
			successes++
		case store.IsNotFound(consumeErr):
			notFound++
		default:
			t.Fatalf("concurrent consumption error = %v", consumeErr)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("concurrent results success=%d not_found=%d", successes, notFound)
	}
}

func testUserTokenAuditFailureRollsBack(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	unissued := newUserToken(user, model.UserTokenEmailVerification)
	if _, err := ss.UserToken().Issue(
		ctx,
		unissued,
		&model.AuditEvent{},
	); err == nil {
		t.Fatal("token issue accepted an invalid terminal audit")
	}
	if _, err := ss.UserToken().GetByHash(
		ctx, unissued.TokenHash, unissued.Purpose,
	); !store.IsNotFound(err) {
		t.Fatalf("audit-failed issue persisted token: %v", err)
	}

	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := ss.UserToken().Issue(
		ctx,
		token,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	if _, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now,
		&model.AuditEvent{},
	); err == nil {
		t.Fatal("token consumption accepted an invalid terminal audit")
	}
	if _, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	); err != nil {
		t.Fatalf("audit-failed consumption changed token state: %v", err)
	}
}

func saveLocalUser(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
) (*model.User, *model.PasswordCredential) {
	t.Helper()
	user, credential, err := ss.User().SaveWithPassword(
		ctx,
		newUser(),
		&model.PasswordCredential{PasswordHash: "encoded-password-hash"},
	)
	requireNoError(t, err)
	return user, credential
}

func newUserToken(
	user *model.User,
	purpose model.UserTokenPurpose,
) *model.UserToken {
	return &model.UserToken{
		UserID: user.ID, Purpose: purpose,
		TokenHash: model.HashToken(model.NewCredentialToken()),
		Target:    user.Email,
		ExpiresAt: model.TimeFromMillis(model.GetMillis() + int64(time.Hour/time.Millisecond)),
	}
}

func userTokenAudit(
	action string,
	userID string,
	institutionID string,
) *model.AuditEvent {
	return &model.AuditEvent{
		Action: action,
		Resource: model.Resource{
			Type: model.ResourceUser,
			Id:   userID,
		},
		ScopeType:  model.RoleScopeInstitution,
		ScopeID:    institutionID,
		Status:     model.AuditStatusSuccess,
		NodeID:     "storetest",
		AuthMethod: "test",
	}
}

func userTokenCompletionAudit(
	action string,
	institutionID string,
) *model.AuditEvent {
	return &model.AuditEvent{
		Action:     action,
		Resource:   model.Resource{Type: model.ResourceUser},
		ScopeType:  model.RoleScopeInstitution,
		ScopeID:    institutionID,
		Status:     model.AuditStatusSuccess,
		NodeID:     "storetest",
		AuthMethod: "test_token",
	}
}
