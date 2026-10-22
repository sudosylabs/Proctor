// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/token_store.go.
// Proctor additionally verifies target binding, replacement, single-use
// concurrency, transactional audits, password mutation, and session reset.

package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	t.Run("EmailChangeCommitsAddressTokenAndBothFrozenDeliveries", func(t *testing.T) {
		testUserEmailChangeAtomic(t, ss)
	})
	t.Run("EmailChangeUsesOneDatabaseClockForTokenAndMail", func(t *testing.T) {
		testUserEmailChangeUsesDatabaseClock(t, ss)
	})
	t.Run("ConcurrentEmailChangeHasOneWinner", func(t *testing.T) {
		testConcurrentUserEmailChange(t, ss)
	})
	t.Run("PrivilegedEmailVerificationCommitsNotice", func(t *testing.T) {
		testPrivilegedEmailVerificationAtomic(t, ss)
	})
}

func testUserEmailChangeUsesDatabaseClock(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_ = saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	preparedAt := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	tokenLifetime, warningLifetime := 37*time.Minute, 19*time.Hour
	newEmail := model.NewID() + "@database-clock.example.edu"
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: preparedAt.Add(tokenLifetime)}
	token.PrepareCreate(model.NewUserTokenID(), preparedAt)
	warningOccurrence, warningDelivery, warningJob := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver,
		preparedAt, preparedAt.Add(warningLifetime))
	verificationOccurrence, verificationDelivery, verificationJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(token.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential,
		preparedAt, preparedAt.Add(tokenLifetime))
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())

	command := &store.UserEmailChange{
		UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: tokenLifetime, WarningLifetime: warningLifetime,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verificationOccurrence, VerificationDelivery: verificationDelivery, VerificationJob: verificationJob,
		AuditEventID: audit.ID, AuditAt: model.GetMillis(),
	}
	result, err := ss.UserToken().ChangeEmail(ctx, command)
	require.NoError(t, err)
	// The User's modification time never moves backwards when the database
	// clock trails the node clock that created the User.
	wantUserUpdatedAt := result.Token.CreatedAt
	if user.UpdatedAt.After(wantUserUpdatedAt) {
		wantUserUpdatedAt = user.UpdatedAt
	}
	require.False(t, result.Token.CreatedAt.Equal(preparedAt), "database-clock User/token = %#v / %#v", result.User, result.Token)
	require.Equal(t, tokenLifetime, result.Token.ExpiresAt.Sub(result.Token.CreatedAt), "database-clock User/token = %#v / %#v", result.User, result.Token)
	require.True(t, result.User.UpdatedAt.Equal(wantUserUpdatedAt), "database-clock User/token = %#v / %#v", result.User, result.Token)
	for _, expected := range []struct {
		deliveryID model.MailDeliveryID
		jobID      model.JobID
		lifetime   time.Duration
	}{
		{warningDelivery.ID, warningJob.ID, warningLifetime},
		{verificationDelivery.ID, verificationJob.ID, tokenLifetime},
	} {
		delivery, getErr := ss.Mail().GetDelivery(ctx, expected.deliveryID)
		require.NoError(t, getErr)
		job, getErr := ss.Job().Get(ctx, expected.jobID)
		require.NoError(t, getErr)
		require.True(t, delivery.CreatedAt.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.True(t, delivery.UpdatedAt.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.True(t, delivery.MessageDate.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.Equal(t, expected.lifetime, delivery.Deadline.Sub(delivery.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.True(t, job.CreatedAt.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.True(t, job.UpdatedAt.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.True(t, job.AvailableAt.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
		require.False(t, job.CompletedAt.Valid && !job.CompletedAt.Time.Equal(result.Token.CreatedAt), "database-clock delivery/job = %#v / %#v, token=%#v", delivery, job, result.Token)
	}
	for _, invalidLifetime := range []struct {
		name    string
		token   time.Duration
		warning time.Duration
	}{
		{name: "short token", token: time.Minute, warning: warningLifetime},
		{name: "long token", token: 31 * 24 * time.Hour, warning: warningLifetime},
		{name: "short warning", token: tokenLifetime, warning: time.Second},
		{name: "long warning", token: tokenLifetime, warning: 25 * time.Hour},
		{name: "sub-millisecond", token: tokenLifetime + time.Nanosecond, warning: warningLifetime},
	} {
		t.Run(invalidLifetime.name, func(t *testing.T) {
			invalid := *command
			invalid.TokenLifetime, invalid.WarningLifetime = invalidLifetime.token, invalidLifetime.warning
			_, invalidErr := ss.UserToken().ChangeEmail(ctx, &invalid)
			var typed *store.ErrInvalidInput
			require.ErrorAs(t, invalidErr, &typed)
		})
	}
}

func testConcurrentUserEmailChange(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_ = saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	build := func(suffix string) *store.UserEmailChange {
		at := model.TimeUTC(time.Now().Add(time.Second))
		email := model.NewID() + suffix + "@example.edu"
		token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification, TokenHash: model.HashToken(model.NewCredentialToken()), Target: email, ExpiresAt: at.Add(time.Hour)}
		token.PrepareCreate(model.NewUserTokenID(), at)
		warningOccurrence, warningDelivery, warningJob := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
		verifyOccurrence, verifyDelivery, verifyJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(token.ID.String()), model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential, at, token.ExpiresAt)
		audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
		return &store.UserEmailChange{UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: email, Token: token,
			TokenLifetime: time.Hour, WarningLifetime: 24 * time.Hour,
			WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
			VerificationOccurrence: verifyOccurrence, VerificationDelivery: verifyDelivery, VerificationJob: verifyJob,
			AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at)}
	}
	inputs := []*store.UserEmailChange{build("a"), build("b")}
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for _, input := range inputs {
		go func(candidate *store.UserEmailChange) {
			ready.Done()
			<-start
			_, err := ss.UserToken().ChangeEmail(ctx, candidate)
			errs <- err
		}(input)
	}
	ready.Wait()
	close(start)
	wins, conflicts := 0, 0
	for range inputs {
		err := <-errs
		if err == nil {
			wins++
		} else if store.IsConflict(err) {
			conflicts++
		} else {
			require.FailNow(t, fmt.Sprintf("concurrent ChangeEmail error = %v", err))
		}
	}
	require.Equal(t, 1, wins, "concurrent outcomes wins")
	require.Equal(t, 1, conflicts, "concurrent outcomes wins")
}

func testUserEmailChangeAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_ = saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	at := model.TimeUTC(time.Now().Add(time.Second))
	newEmail := model.NewID() + "@new.example.edu"
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification, TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: at.Add(time.Hour)}
	token.PrepareCreate(model.NewUserTokenID(), at)
	warningOccurrence, warningDelivery, warningJob := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
	verifyOccurrence, verifyDelivery, verifyJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(token.ID.String()), model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential, at, token.ExpiresAt)
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	result, err := ss.UserToken().ChangeEmail(ctx, &store.UserEmailChange{UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: time.Hour, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verifyOccurrence, VerificationDelivery: verifyDelivery, VerificationJob: verifyJob,
		AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at)})
	require.NoError(t, err)
	require.Equal(t, newEmail, result.User.Email)
	require.False(t, result.User.EmailVerified)
	require.Equal(t, user.Revision+1, result.User.Revision)
	require.Equal(t, newEmail, result.Token.Target)
	for _, expected := range []*model.MailDelivery{warningDelivery, verifyDelivery} {
		delivery, getErr := ss.Mail().GetDelivery(ctx, expected.ID)
		require.NoError(t, getErr)
		require.Equal(t, expected.TemplateKey, delivery.TemplateKey, "%s delivery = %#v", expected.TemplateKey, delivery)
		require.NotEmpty(t, delivery.EncryptedPayload, "%s delivery = %#v", expected.TemplateKey, delivery)
	}
	second, _ := saveLocalUser(t, ctx, ss)
	secondAt := at.Add(time.Second)
	conflictToken := &model.UserToken{UserID: second.ID, Purpose: model.UserTokenEmailVerification, TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: secondAt.Add(time.Hour)}
	conflictToken.PrepareCreate(model.NewUserTokenID(), secondAt)
	conflictWarningOccurrence, conflictWarningDelivery, conflictWarningJob := userTokenMailFixture(t, second.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver, secondAt, secondAt.Add(24*time.Hour))
	conflictVerifyOccurrence, conflictVerifyDelivery, conflictVerifyJob := userTokenMailFixture(t, second.ID, model.MailOccurrenceID(conflictToken.ID.String()), model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential, secondAt, conflictToken.ExpiresAt)
	conflictAudit := saveUserProfileAuditAttempt(t, ctx, ss, second.ID.String())
	_, err = ss.UserToken().ChangeEmail(ctx, &store.UserEmailChange{UserID: second.ID, ExpectedRevision: second.Revision, NewEmail: newEmail, Token: conflictToken,
		TokenLifetime: time.Hour, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: conflictWarningOccurrence, WarningDelivery: conflictWarningDelivery, WarningJob: conflictWarningJob,
		VerificationOccurrence: conflictVerifyOccurrence, VerificationDelivery: conflictVerifyDelivery, VerificationJob: conflictVerifyJob,
		AuditEventID: conflictAudit.ID, AuditAt: model.MillisFromTime(secondAt)})
	require.True(t, store.IsConflict(err), "duplicate email change error")
	unchanged, getErr := ss.User().Get(ctx, second.ID)
	require.NoError(t, getErr)
	require.Equal(t, second.Email, unchanged.Email)
	require.Equal(t, second.Revision, unchanged.Revision)
	_, getErr = ss.UserToken().GetByHash(ctx, conflictToken.TokenHash, conflictToken.Purpose)
	require.True(t, store.IsNotFound(getErr), "rolled-back token lookup")
}

func testPrivilegedEmailVerificationAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_ = saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	at := model.TimeUTC(time.Now().Add(time.Second))
	occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailVerifiedByAdmin, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	verified, err := ss.UserToken().VerifyEmailPrivileged(ctx, &store.PrivilegedEmailVerification{UserID: user.ID, ExpectedRevision: user.Revision,
		Occurrence: occurrence, Delivery: delivery, Job: job,
		AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at)})
	require.NoError(t, err)
	require.True(t, verified.EmailVerified)
	require.Equal(t, user.Revision+1, verified.Revision)
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityEmailVerifiedByAdmin}, Limit: 10})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	require.NotEmpty(t, deliveries[0].EncryptedPayload)
}

func testUserTokenIssueRechecksCurrentEligibleAccount(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)

	t.Run("changed verification target", func(t *testing.T) {
		user, _ := saveLocalUser(t, ctx, ss)
		token := newUserToken(user, model.UserTokenEmailVerification)
		user = changeUserEmailForTest(t, ctx, ss, user, model.NewID()+"@changed.example.edu")
		var err error
		_, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
		require.True(t, store.IsNotFound(err), "changed-target Issue() error")
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})

	t.Run("disabled user", func(t *testing.T) {
		user, _ := saveLocalUser(t, ctx, ss)
		audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
		at := model.GetMillis() + 1
		_, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
			ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
			AuditEventID: audit.ID, AuditAt: at,
		}))
		require.NoError(t, err)
		token := newUserToken(user, model.UserTokenEmailVerification)
		_, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
		require.True(t, store.IsNotFound(err), "disabled-user Issue() error")
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})

	t.Run("password reset without active local credential", func(t *testing.T) {
		user, err := createUser(t, ctx, ss, newUser())
		require.NoError(t, err)
		token := newUserToken(user, model.UserTokenPasswordReset)
		_, err = issueUserToken(t, ss, ctx, token,
			userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
		require.True(t, store.IsNotFound(err), "external-only Issue() error")
		assertUserTokenIssueAbsent(t, ctx, ss, token)
	})
}

func assertUserTokenIssueAbsent(t *testing.T, ctx context.Context, ss store.Store, token *model.UserToken) {
	t.Helper()
	_, err := ss.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	require.True(t, store.IsNotFound(err), "ineligible Issue() persisted token")
	key := model.MailTemplateIdentityVerifyEmail
	if token.Purpose == model.UserTokenPasswordReset {
		key = model.MailTemplateIdentityPasswordReset
	}
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 100})
	require.NoError(t, err)
	for _, delivery := range deliveries {
		require.NotEqual(t, token.ID.String(), delivery.OccurrenceID.String())
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
	require.NoError(t, err)
	second := newUserToken(user, model.UserTokenEmailVerification)
	second, err = issueUserToken(t, ss,
		ctx,
		second,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	require.NoError(t, err)
	gotFirst, err := ss.UserToken().GetByHash(ctx, first.TokenHash, first.Purpose)
	require.NoError(t, err)
	require.True(t, gotFirst.ArchivedAt.Valid)
	gotSecond, err := ss.UserToken().GetByHash(ctx, second.TokenHash, second.Purpose)
	require.NoError(t, err)
	require.False(t, gotSecond.ArchivedAt.Valid)
	require.Equal(t, user.Email, gotSecond.Target)
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	require.NoError(t, err)
	var firstDelivery, secondDelivery *model.MailDelivery
	for _, delivery := range deliveries {
		switch delivery.OccurrenceID.String() {
		case first.ID.String():
			firstDelivery = delivery
		case second.ID.String():
			secondDelivery = delivery
		}
	}
	require.NotNil(t, firstDelivery)
	require.Equal(t, model.MailDeliverySuppressed, firstDelivery.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, firstDelivery.PublicFailureCode)
	require.Empty(t, firstDelivery.EncryptedPayload)
	firstJob, err := ss.Job().Get(ctx, firstDelivery.JobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, firstJob.Status)
	require.NotNil(t, secondDelivery)
	require.Equal(t, model.MailDeliveryQueued, secondDelivery.State)
	require.NotEmpty(t, secondDelivery.EncryptedPayload)
	_, err = ss.UserToken().ConsumeEmailVerification(
		ctx,
		first.TokenHash,
		model.MillisFromTime(second.CreatedAt)+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.True(t, store.IsNotFound(err), "superseded token consumption error")
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
	require.NoError(t, err)
	behindNodeTime := model.MillisFromTime(token.CreatedAt.Add(-time.Minute))
	result, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		behindNodeTime,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.NoError(t, err)
	// The node clock is a minute behind; PostgreSQL time decides, and a
	// database clock trailing the token's creating node may only meet it.
	require.True(t, result.Token.ConsumedAt.Valid)
	require.False(t, result.Token.ConsumedAt.Time.Before(token.CreatedAt))
	require.False(t, result.User.UpdatedAt.Before(token.CreatedAt))
	require.True(t, result.Token.ConsumedAt.Time.After(model.TimeFromMillis(behindNodeTime)))
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.email_verification.complete", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.True(t, containsUserTokenAuditAt(audits, user.ID, result.Token.ConsumedAt.Time), "behind-node verification audit = %#v", audits)
}

func testUserTokenCallerClockCannotBackdatePasswordReset(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	behindNodeTime := model.MillisFromTime(token.CreatedAt.Add(-time.Minute))
	result, err := ss.UserToken().ConsumePasswordReset(ctx,
		passwordResetCompletion(t, user, token.TokenHash, "behind-node-password-hash", behindNodeTime,
			userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String())))
	require.NoError(t, err)
	require.True(t, result.Token.ConsumedAt.Valid)
	require.False(t, result.Token.ConsumedAt.Time.Before(token.CreatedAt))
	require.True(t, result.Token.ConsumedAt.Time.After(model.TimeFromMillis(behindNodeTime)))
	require.False(t, result.PasswordCredential.PasswordChangedAt.Before(token.CreatedAt))
	require.Len(t, result.RevokedSessions, 1)
	require.False(t, result.RevokedSessions[0].RevokedAt.Time.Before(session.CreatedAt))
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "authentication.password_reset.complete", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	transitionAt := result.Token.ConsumedAt.Time
	require.True(t, containsUserTokenAuditAt(audits, user.ID, transitionAt), "behind-node password reset audit = %#v", audits)
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 10,
	})
	require.NoError(t, err)
	var notice *model.MailDelivery
	for _, candidate := range notices {
		if candidate.TargetUserID == user.ID && candidate.CreatedAt.Equal(transitionAt) {
			notice = candidate
			break
		}
	}
	require.NotNil(t, notice)
	require.True(t, notice.MessageDate.Equal(transitionAt))
	noticeJob, err := ss.Job().Get(ctx, notice.JobID)
	require.NoError(t, err)
	require.True(t, noticeJob.CreatedAt.Equal(transitionAt))
	require.True(t, noticeJob.UpdatedAt.Equal(transitionAt))
	require.True(t, noticeJob.AvailableAt.Equal(transitionAt))
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
	require.NoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	result, err := ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.NoError(t, err)
	transitionAt := result.Token.ConsumedAt.Time
	require.True(t, result.User.EmailVerified)
	require.Equal(t, user.Revision+1, result.User.Revision)
	require.True(t, result.Token.ConsumedAt.Valid)
	require.True(t, result.User.UpdatedAt.Equal(transitionAt))
	delivery := findUserTokenDelivery(t, ctx, ss, token.ID, model.MailTemplateIdentityVerifyEmail)
	suppressed, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, transitionAt.Add(time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, suppressed.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, suppressed.PublicFailureCode)
	require.Empty(t, suppressed.EncryptedPayload)
	_, err = ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.True(t, store.IsNotFound(err), "second consumption error")

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
	require.NoError(t, err)
	changeUserEmailForTest(t, ctx, ss, changedUser, model.NewID()+"@changed.example.edu")
	_, err = ss.UserToken().ConsumeEmailVerification(
		ctx,
		changedToken.TokenHash,
		now+2,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.True(t, store.IsNotFound(err), "changed-target consumption error")
}

func changeUserEmailForTest(t *testing.T, ctx context.Context, ss store.Store, user *model.User, newEmail string) *model.User {
	t.Helper()
	at := model.TimeUTC(time.Now().Add(time.Second))
	tokenLifetime, warningLifetime := time.Hour, 24*time.Hour
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: at.Add(tokenLifetime)}
	token.PrepareCreate(model.NewUserTokenID(), at)
	warningOccurrence, warningDelivery, warningJob := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver,
		at, at.Add(warningLifetime))
	verificationOccurrence, verificationDelivery, verificationJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(token.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential,
		at, at.Add(tokenLifetime))
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	result, err := ss.UserToken().ChangeEmail(ctx, &store.UserEmailChange{
		UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: tokenLifetime, WarningLifetime: warningLifetime,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verificationOccurrence, VerificationDelivery: verificationDelivery, VerificationJob: verificationJob,
		AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at),
	})
	require.NoError(t, err)
	return result.User
}

func findUserTokenDelivery(t *testing.T, ctx context.Context, ss store.Store, tokenID model.UserTokenID, key model.MailTemplateKey) *model.MailDelivery {
	t.Helper()
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 20})
	require.NoError(t, err)
	for _, delivery := range deliveries {
		if delivery.OccurrenceID.String() == tokenID.String() {
			return delivery
		}
	}
	require.FailNow(t, fmt.Sprintf("delivery for token %s was not found", tokenID))
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
	require.NoError(t, err)
	now := max(model.MillisFromTime(token.CreatedAt), model.MillisFromTime(session.CreatedAt)) + 100
	result, err := ss.UserToken().ConsumePasswordReset(
		ctx,
		passwordResetCompletion(t, user, token.TokenHash, "new-encoded-password-hash", now,
			userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String())),
	)
	require.NoError(t, err)
	transitionAt := result.Token.ConsumedAt.Time
	require.Equal(t, credential.ID, result.PasswordCredential.ID)
	require.Equal(t, credential.Revision+1, result.PasswordCredential.Revision)
	require.Equal(t, "new-encoded-password-hash", result.PasswordCredential.PasswordHash)
	require.True(t, result.Token.ConsumedAt.Valid)
	require.True(t, result.PasswordCredential.PasswordChangedAt.Equal(transitionAt))
	require.Len(t, result.RevokedSessions, 1)
	require.Len(t, result.RevokedAccessHashes, 2)
	require.True(t, result.RevokedSessions[0].RevokedAt.Time.Equal(transitionAt))
	resetDelivery := findUserTokenDelivery(t, ctx, ss, token.ID, model.MailTemplateIdentityPasswordReset)
	suppressed, err := ss.Mail().StartDelivery(ctx, resetDelivery.ID, resetDelivery.Revision, transitionAt.Add(time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, suppressed.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, suppressed.PublicFailureCode)
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 20})
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Equal(t, user.ID, notices[0].TargetUserID)
	require.Equal(t, model.MailDeliveryQueued, notices[0].State)
	require.True(t, notices[0].CreatedAt.Equal(transitionAt))
	require.True(t, notices[0].MessageDate.Equal(transitionAt))
	resolved, resolvedSession, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(raw.access),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.True(t, resolved.RevokedAt.Time.Equal(transitionAt), "reset session remained active: %#v %#v", resolved, resolvedSession)
	require.True(t, resolvedSession.RevokedAt.Time.Equal(transitionAt), "reset session remained active: %#v %#v", resolved, resolvedSession)
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action:     "authentication.password_reset.complete",
		Limit:      10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, user.ID.String(), audits[0].Resource.ID)
	require.Equal(t, model.AuditStatusSuccess, audits[0].Status)
	require.True(t, audits[0].CreatedAt.Equal(transitionAt))
}

func testPasswordResetMailFailureRollsBackMutation(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, credential := saveLocalUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	now := max(model.MillisFromTime(token.CreatedAt), model.MillisFromTime(session.CreatedAt)) + 100
	completion := passwordResetCompletion(t, user, token.TokenHash, "must-not-commit", now,
		userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String()))
	// The immutable token occurrence already owns this ID. The collision occurs
	// only after the credential/session/token mutations have run in the named
	// transaction, proving a delivery persistence failure rolls them all back.
	completion.Occurrence.ID = model.MailOccurrenceID(token.ID.String())
	completion.Delivery.OccurrenceID = completion.Occurrence.ID
	_, err = ss.UserToken().ConsumePasswordReset(ctx, completion)
	require.Error(t, err, "password reset accepted a colliding notice occurrence")
	unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, credential.ID, unchanged.ID)
	require.Equal(t, credential.PasswordHash, unchanged.PasswordHash)
	retainedSession, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.False(t, retainedSession.RevokedAt.Valid)
	retainedToken, err := ss.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	require.NoError(t, err)
	require.False(t, retainedToken.ConsumedAt.Valid)
	notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 20})
	require.NoError(t, err)
	for _, notice := range notices {
		require.NotEqual(t, completion.Delivery.ID, notice.ID)
	}
}

func testPasswordResetCommitsSuppressedNoticeWhenMailDisabled(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	token := newUserToken(user, model.UserTokenPasswordReset)
	token, err := issueUserToken(t, ss, ctx, token,
		userTokenAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	completion := passwordResetCompletion(t, user, token.TokenHash, "disabled-mail-new-password", now,
		userTokenCompletionAudit("authentication.password_reset.complete", institution.ID.String()))
	completion.Delivery.State = model.MailDeliverySuppressed
	completion.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	completion.Delivery.EncryptedPayload = nil
	completion.Job, err = completion.Job.RequestCancellation(completion.Job.CreatedAt)
	require.NoError(t, err)
	result, err := ss.UserToken().ConsumePasswordReset(ctx, completion)
	require.NoError(t, err)
	require.Equal(t, "disabled-mail-new-password", result.PasswordCredential.PasswordHash)
	require.True(t, result.Token.ConsumedAt.Valid)
	notice, err := ss.Mail().GetDelivery(ctx, completion.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, notice.State)
	require.Equal(t, model.MailDeliveryDisabledCode, notice.PublicFailureCode)
	require.Empty(t, notice.EncryptedPayload)
	job, err := ss.Job().Get(ctx, completion.Job.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, job.Status)
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
	require.NoError(t, err)
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
			require.FailNow(t, fmt.Sprintf("concurrent consumption error = %v", consumeErr))
		}
	}
	require.Equal(t, 1, successes, "concurrent results success")
	require.Equal(t, 1, notFound, "concurrent results success")
}

func testUserTokenAuditFailureRollsBack(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	unissued := newUserToken(user, model.UserTokenEmailVerification)
	_, err := issueUserToken(t, ss,
		ctx,
		unissued,
		&model.AuditEvent{},
	)
	require.Error(t, err, "token issue accepted an invalid terminal audit")
	_, err = ss.UserToken().GetByHash(
		ctx, unissued.TokenHash, unissued.Purpose,
	)
	require.True(t, store.IsNotFound(err), "audit-failed issue persisted token")

	token := newUserToken(user, model.UserTokenEmailVerification)
	token, err = issueUserToken(t, ss,
		ctx,
		token,
		userTokenAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()),
	)
	require.NoError(t, err)
	now := model.MillisFromTime(token.CreatedAt) + 100
	_, err = ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now,
		&model.AuditEvent{},
	)
	require.Error(t, err, "token consumption accepted an invalid terminal audit")
	_, err = ss.UserToken().ConsumeEmailVerification(
		ctx,
		token.TokenHash,
		now+1,
		userTokenCompletionAudit("authentication.email_verification.complete", institution.ID.String()),
	)
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	return &store.PasswordResetCompletion{TokenHash: tokenHash, PasswordHash: passwordHash, At: at, RevocationReason: model.SessionRevocationPasswordReset, AuditEvent: audit, Occurrence: occurrence, Delivery: delivery, Job: job}
}

func userTokenMailFixture(t *testing.T, userID model.UserID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind, key model.MailTemplateKey, jobType model.JobType, at, deadline time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job) {
	t.Helper()
	deliveryID := model.NewMailDeliveryID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), jobType, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: userID, CreatedAt: at}
	delivery := &model.MailDelivery{
		ID: deliveryID, OccurrenceID: occurrenceID, JobID: job.ID, TargetUserID: userID,
		TemplateKey: key, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "u***@example.test",
		State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
		MessageID:        "<mail." + deliveryID.String() + "@example.test>",
		EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1,
	}
	err = occurrence.Validate()
	require.NoError(t, err)
	err = delivery.Validate()
	require.NoError(t, err)
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
