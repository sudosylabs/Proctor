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
	"encoding/json"
	"strings"
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
	t.Run("IssueRechecksCurrentEligibleAccount", func(t *testing.T) {
		testUserTokenIssueRechecksCurrentEligibleAccount(t, ss)
	})
	t.Run("EmailVerificationIsTargetBoundAndSingleUse", func(t *testing.T) {
		testEmailVerificationIsTargetBoundAndSingleUse(t, ss)
	})
	t.Run("PasswordResetRevokesSessionsAndAudits", func(t *testing.T) {
		testPasswordResetRevokesSessionsAndAudits(t, ss)
	})
	t.Run("PasswordResetMailFailureRollsBackMutation", func(t *testing.T) {
		testPasswordResetMailFailureRollsBackMutation(t, ss)
	})
	t.Run("PasswordResetCommitsSuppressedNoticeWhenMailDisabled", func(t *testing.T) {
		testPasswordResetCommitsSuppressedNoticeWhenMailDisabled(t, ss)
	})
	t.Run("ConcurrentConsumptionHasOneWinner", func(t *testing.T) {
		testUserTokenConcurrentConsumption(t, ss)
	})
	t.Run("CallerClockCannotBackdateEmailVerification", func(t *testing.T) {
		testUserTokenCallerClockCannotBackdateEmailVerification(t, ss)
	})
	t.Run("CallerClockCannotBackdatePasswordReset", func(t *testing.T) {
		testUserTokenCallerClockCannotBackdatePasswordReset(t, ss)
	})
	t.Run("AuditFailureRollsBackCredentialState", func(t *testing.T) {
		testUserTokenAuditFailureRollsBack(t, ss)
	})
}

func testUserTokenIssueRechecksCurrentEligibleAccount(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	t.Run("changed verification target", func(t *testing.T) {
		user, _ := saveLocalUser(t, ctx, ss)
		token := newUserToken(user, model.UserTokenEmailVerification)
		user.Email = model.NewId() + "@changed.example.edu"
		_, err := ss.User().Update(ctx, user)
		requireNoError(t, err)
		if _, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String())); !store.IsNotFound(err) {
			t.Fatalf("changed-target Issue() error = %v, want not found", err)
		}
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})

	t.Run("disabled user", func(t *testing.T) {
		user, _ := saveLocalUser(t, ctx, ss)
		audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
		at := model.GetMillis() + 1
		_, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
			ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
			ChangedAt: at, RevocationReason: "account disabled",
			AuditEventID: audit.ID.String(), AuditAt: at,
		})
		requireNoError(t, err)
		token := newUserToken(user, model.UserTokenEmailVerification)
		if _, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String())); !store.IsNotFound(err) {
			t.Fatalf("disabled-user Issue() error = %v, want not found", err)
		}
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})

	t.Run("password reset without active local credential", func(t *testing.T) {
		user, err := createUser(t, ctx, ss, newUser())
		requireNoError(t, err)
		token := newUserToken(user, model.UserTokenPasswordReset)
		if _, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String())); !store.IsNotFound(err) {
			t.Fatalf("external-only Issue() error = %v, want not found", err)
		}
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})
}

func assertUserTokenIssueAbsent(t *testing.T, ctx context.Context, ss store.Store, token *model.UserToken) {
	t.Helper()
	if _, err := ss.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose); !store.IsNotFound(err) {
		t.Fatalf("ineligible Issue() persisted token: %v", err)
	}
	key := model.MailTemplateIdentityVerifyEmail
	if token.Purpose == model.UserTokenPasswordReset {
		key = model.MailTemplateIdentityPasswordReset
	}
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 100})
	requireNoError(t, err)
	for _, delivery := range deliveries {
		if delivery.OccurrenceID.String() == token.ID.String() {
			t.Fatalf("ineligible Issue() persisted delivery = %#v", delivery)
		}
	}
}

func testUserTokenIssueReplacesPriorToken(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	first := newUserToken(user, model.UserTokenEmailVerification)
	first, err := issueUserToken(t, ss,
		ctx,
		first,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	second := newUserToken(user, model.UserTokenEmailVerification)
	second, err = issueUserToken(t, ss,
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
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	requireNoError(t, err)
	var firstDelivery, secondDelivery *model.MailDelivery
	for _, delivery := range deliveries {
		switch delivery.OccurrenceID.String() {
		case first.ID.String():
			firstDelivery = delivery
		case second.ID.String():
			secondDelivery = delivery
		}
	}
	if firstDelivery == nil || firstDelivery.State != model.MailDeliverySuppressed ||
		firstDelivery.PublicFailureCode != model.MailDeliveryObsoleteCode || len(firstDelivery.EncryptedPayload) != 0 {
		t.Fatalf("superseded delivery = %#v", firstDelivery)
	}
	firstJob, err := ss.Job().Get(ctx, firstDelivery.JobID)
	requireNoError(t, err)
	if firstJob.Status != model.JobStatusCanceled {
		t.Fatalf("superseded delivery job = %#v", firstJob)
	}
	if secondDelivery == nil || secondDelivery.State != model.MailDeliveryQueued || len(secondDelivery.EncryptedPayload) == 0 {
		t.Fatalf("replacement delivery = %#v", secondDelivery)
	}
	if _, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		first.TokenHash,
		model.MillisFromTime(second.CreatedAt)+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	); !store.IsNotFound(err) {
		t.Fatalf("superseded token consumption error = %v, want not found", err)
	}
}

func testUserTokenCallerClockCannotBackdateEmailVerification(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := issueUserToken(t, ss,
		ctx,
		token,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	behindNodeTime := model.MillisFromTime(token.CreatedAt.Add(-time.Minute))
	result, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		behindNodeTime,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	requireNoError(t, err)
	if !result.Token.ConsumedAt.Valid || !result.Token.ConsumedAt.Time.After(token.CreatedAt) ||
		!result.User.UpdatedAt.After(token.CreatedAt) {
		t.Fatalf("behind-node verification result = %#v", result)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.email_verification.complete", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if !containsUserTokenAuditAt(audits, user.ID, result.Token.ConsumedAt.Time) {
		t.Fatalf("behind-node verification audit = %#v", audits)
	}
}

func testUserTokenCallerClockCannotBackdatePasswordReset(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	requireNoError(t, err)
	behindNodeTime := model.MillisFromTime(token.CreatedAt.Add(-time.Minute))
	result, err := ss.UserToken().ConsumePasswordReset(ctx,
		passwordResetCompletion(t, user, token.TokenHash, "behind-node-password-hash", behindNodeTime,
			userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String())))
	requireNoError(t, err)
	if !result.Token.ConsumedAt.Valid || !result.Token.ConsumedAt.Time.After(token.CreatedAt) ||
		!result.PasswordCredential.PasswordChangedAt.After(token.CreatedAt) || len(result.RevokedSessions) != 1 ||
		!result.RevokedSessions[0].RevokedAt.Time.After(session.CreatedAt) {
		t.Fatalf("behind-node password reset result = %#v", result)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.password_reset.complete", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	transitionAt := result.Token.ConsumedAt.Time
	if !containsUserTokenAuditAt(audits, user.ID, transitionAt) {
		t.Fatalf("behind-node password reset audit = %#v", audits)
	}
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 10,
	})
	requireNoError(t, err)
	var notice *model.MailDelivery
	for _, candidate := range notices {
		if candidate.TargetUserID == user.ID && candidate.CreatedAt.Equal(transitionAt) {
			notice = candidate
			break
		}
	}
	if notice == nil || !notice.MessageDate.Equal(transitionAt) {
		t.Fatalf("behind-node password reset notice = %#v", notices)
	}
	noticeJob, err := ss.Job().Get(ctx, notice.JobID)
	requireNoError(t, err)
	if !noticeJob.CreatedAt.Equal(transitionAt) || !noticeJob.UpdatedAt.Equal(transitionAt) || !noticeJob.AvailableAt.Equal(transitionAt) {
		t.Fatalf("behind-node password reset notice Job = %#v", noticeJob)
	}
}

func containsUserTokenAuditAt(audits []*model.AuditEvent, userID model.UserID, at time.Time) bool {
	for _, audit := range audits {
		if audit.Resource.ID == userID.String() && audit.CreatedAt.Equal(at) {
			return true
		}
	}
	return false
}

func testEmailVerificationIsTargetBoundAndSingleUse(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := issueUserToken(t, ss,
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
	transitionAt := result.Token.ConsumedAt.Time
	if !result.User.EmailVerified || result.User.Revision != user.Revision+1 ||
		!result.Token.ConsumedAt.Valid || !result.User.UpdatedAt.Equal(transitionAt) {
		t.Fatalf("verification result = %#v", result)
	}
	delivery := findUserTokenDelivery(t, ctx, ss, token.ID, model.MailTemplateIdentityVerifyEmail)
	suppressed, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, transitionAt.Add(time.Millisecond))
	requireNoError(t, err)
	if suppressed.State != model.MailDeliverySuppressed || suppressed.PublicFailureCode != model.MailDeliveryObsoleteCode || len(suppressed.EncryptedPayload) != 0 {
		t.Fatalf("consumed-token delivery start = %#v", suppressed)
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
	changedToken, err = issueUserToken(t, ss,
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

func findUserTokenDelivery(t *testing.T, ctx context.Context, ss store.Store, tokenID model.UserTokenID, key model.MailTemplateKey) *model.MailDelivery {
	t.Helper()
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 20})
	requireNoError(t, err)
	for _, delivery := range deliveries {
		if delivery.OccurrenceID.String() == tokenID.String() {
			return delivery
		}
	}
	t.Fatalf("delivery for token %s was not found", tokenID)
	return nil
}

func testPasswordResetRevokesSessionsAndAudits(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, credential := saveLocalUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss,
		ctx,
		token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()),
	)
	requireNoError(t, err)
	now := max(model.MillisFromTime(token.CreatedAt), model.MillisFromTime(session.CreatedAt)) + 100
	result, err := ss.UserToken().ConsumePasswordReset(
		ctx,
		passwordResetCompletion(t, user, token.TokenHash, "new-encoded-password-hash", now,
			userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String())),
	)
	requireNoError(t, err)
	transitionAt := result.Token.ConsumedAt.Time
	if result.PasswordCredential.ID != credential.ID ||
		result.PasswordCredential.PasswordHash != "new-encoded-password-hash" ||
		!result.Token.ConsumedAt.Valid || !result.PasswordCredential.PasswordChangedAt.Equal(transitionAt) ||
		len(result.RevokedSessions) != 1 ||
		len(result.RevokedAccessHashes) != 2 || !result.RevokedSessions[0].RevokedAt.Time.Equal(transitionAt) {
		t.Fatalf("password reset result = %#v", result)
	}
	resetDelivery := findUserTokenDelivery(t, ctx, ss, token.ID, model.MailTemplateIdentityPasswordReset)
	suppressed, err := ss.Mail().StartDelivery(ctx, resetDelivery.ID, resetDelivery.Revision, transitionAt.Add(time.Millisecond))
	requireNoError(t, err)
	if suppressed.State != model.MailDeliverySuppressed || suppressed.PublicFailureCode != model.MailDeliveryObsoleteCode {
		t.Fatalf("consumed reset delivery = %#v", suppressed)
	}
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 20})
	requireNoError(t, err)
	if len(notices) != 1 || notices[0].TargetUserID != user.ID || notices[0].State != model.MailDeliveryQueued ||
		!notices[0].CreatedAt.Equal(transitionAt) || !notices[0].MessageDate.Equal(transitionAt) {
		t.Fatalf("password-changed deliveries = %#v", notices)
	}
	resolved, resolvedSession, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if !resolved.RevokedAt.Time.Equal(transitionAt) || !resolvedSession.RevokedAt.Time.Equal(transitionAt) {
		t.Fatalf("reset session remained active: %#v %#v", resolved, resolvedSession)
	}
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action:     "authentication.password_reset.complete",
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	requireNoError(t, err)
	if len(audits) != 1 ||
		audits[0].Resource.ID != user.ID.String() ||
		audits[0].Status != model.AuditStatusSuccess || !audits[0].CreatedAt.Equal(transitionAt) {
		t.Fatalf("password reset audit = %#v", audits)
	}
}

func testPasswordResetMailFailureRollsBackMutation(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, credential := saveLocalUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	requireNoError(t, err)
	now := max(model.MillisFromTime(token.CreatedAt), model.MillisFromTime(session.CreatedAt)) + 100
	completion := passwordResetCompletion(t, user, token.TokenHash, "must-not-commit", now,
		userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String()))
	// The immutable token occurrence already owns this ID. The collision occurs
	// only after the credential/session/token mutations have run in the named
	// transaction, proving a delivery persistence failure rolls them all back.
	completion.Occurrence.ID = model.MailOccurrenceID(token.ID.String())
	completion.Delivery.OccurrenceID = completion.Occurrence.ID
	if _, err = ss.UserToken().ConsumePasswordReset(ctx, completion); err == nil {
		t.Fatal("password reset accepted a colliding notice occurrence")
	}
	unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if unchanged.ID != credential.ID || unchanged.PasswordHash != credential.PasswordHash {
		t.Fatalf("failed completion changed password credential = %#v", unchanged)
	}
	retainedSession, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if retainedSession.RevokedAt.Valid {
		t.Fatalf("failed completion revoked session = %#v", retainedSession)
	}
	retainedToken, err := ss.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	requireNoError(t, err)
	if retainedToken.ConsumedAt.Valid {
		t.Fatalf("failed completion consumed token = %#v", retainedToken)
	}
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 20})
	requireNoError(t, err)
	for _, notice := range notices {
		if notice.ID == completion.Delivery.ID {
			t.Fatalf("failed completion persisted notice = %#v", notice)
		}
	}
}

func testPasswordResetCommitsSuppressedNoticeWhenMailDisabled(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	requireNoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	completion := passwordResetCompletion(t, user, token.TokenHash, "disabled-mail-new-password", now,
		userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String()))
	completion.Delivery.State = model.MailDeliverySuppressed
	completion.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	completion.Delivery.EncryptedPayload = nil
	completion.Job, err = completion.Job.RequestCancellation(completion.Job.CreatedAt)
	requireNoError(t, err)
	result, err := ss.UserToken().ConsumePasswordReset(ctx, completion)
	requireNoError(t, err)
	if result.PasswordCredential.PasswordHash != "disabled-mail-new-password" || !result.Token.ConsumedAt.Valid {
		t.Fatalf("disabled-mail reset result = %#v", result)
	}
	notice, err := ss.Mail().GetDelivery(ctx, completion.Delivery.ID)
	requireNoError(t, err)
	if notice.State != model.MailDeliverySuppressed || notice.PublicFailureCode != model.MailDeliveryDisabledCode || len(notice.EncryptedPayload) != 0 {
		t.Fatalf("disabled-mail notice = %#v", notice)
	}
	job, err := ss.Job().Get(ctx, completion.Job.ID)
	requireNoError(t, err)
	if job.Status != model.JobStatusCanceled {
		t.Fatalf("disabled-mail notice job = %#v", job)
	}
}

func testUserTokenConcurrentConsumption(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err := issueUserToken(t, ss,
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
	if _, err := issueUserToken(t, ss,
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
	token, err := issueUserToken(t, ss,
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
	result, err := ss.User().Create(ctx, testUserCreation(
		newUser(),
		&model.PasswordCredential{PasswordHash: "encoded-password-hash"},
	))
	requireNoError(t, err)
	return result.User, result.PasswordCredential
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

func issueUserToken(t *testing.T, ss store.Store, ctx context.Context, token *model.UserToken, audit *model.AuditEvent) (*model.UserToken, error) {
	t.Helper()
	if token.ID.IsZero() {
		token.PrepareCreate(model.NewUserTokenID(), time.Now())
	}
	key := model.MailTemplateIdentityVerifyEmail
	if token.Purpose == model.UserTokenPasswordReset {
		key = model.MailTemplateIdentityPasswordReset
	}
	occurrence, delivery, job := userTokenMailFixture(t, token.UserID, model.MailOccurrenceID(token.ID.String()), model.MailOccurrenceAccountToken, key, model.JobTypeMailDeliverCredential, token.CreatedAt, token.ExpiresAt)
	return ss.UserToken().Issue(ctx, &store.UserTokenMailIssue{Token: token, Occurrence: occurrence, Delivery: delivery, Job: job, AuditEvent: audit})
}

func passwordResetCompletion(t *testing.T, user *model.User, tokenHash, passwordHash string, at int64, audit *model.AuditEvent) *store.PasswordResetCompletion {
	t.Helper()
	when := model.TimeFromMillis(at)
	occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityPasswordChanged, model.JobTypeMailDeliver, when, when.Add(24*time.Hour))
	return &store.PasswordResetCompletion{TokenHash: tokenHash, PasswordHash: passwordHash, At: at, RevocationReason: "password reset", AuditEvent: audit, Occurrence: occurrence, Delivery: delivery, Job: job}
}

func userTokenMailFixture(t *testing.T, userID model.UserID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind, key model.MailTemplateKey, jobType model.JobType, at, deadline time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	deliveryID := model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	requireNoError(t, err)
	job, err := model.NewJob(model.NewJobID(), jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	requireNoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: userID, CreatedAt: at}
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: userID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "u***@example.test",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
		MessageID:        "<mail." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1,
	}
	if err = occurrence.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = delivery.Validate(); err != nil {
		t.Fatal(err)
	}
	return occurrence, delivery, job
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
			ID:   userID,
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
