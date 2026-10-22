//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestUserEmailChangePreservesLifecycleAcrossClockSkew(t *testing.T) {
	for _, test := range []struct {
		name          string
		futureCreated bool
		futureUpdated bool
		priorToken    bool
	}{
		{name: "user_created_ahead", futureCreated: true, futureUpdated: true},
		{name: "user_updated_ahead", futureUpdated: true},
		{name: "prior_token_ahead", priorToken: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			persistence := openTestStore(t)
			resetTestStore(t, persistence)
			institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "email-clock", DisplayName: "Email Clock"})
			require.NoError(t, err)
			databaseNow := func() time.Time {
				t.Helper()
				var at time.Time
				require.NoError(t, persistence.GetMaster().Get(ctx, &at, `SELECT clock_timestamp()`))
				return model.TimeUTC(at)
			}
			now := databaseNow()
			future := now.Add(time.Hour)
			createdAt := now.Add(-time.Hour)
			if test.futureCreated {
				createdAt = future
			}
			user := &model.User{Username: "email-clock", Email: "email-clock@example.edu"}
			user.PrepareCreate(model.NewUserID(), createdAt)
			if test.futureUpdated {
				user.UpdatedAt = future.Add(time.Hour)
			}
			pictureCommand, err := model.EncodeDefaultProfilePictureCommand(model.DefaultProfilePictureCommand{UserID: user.ID})
			require.NoError(t, err)
			pictureJob, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault,
				pictureCommand, user.ID.String(), createdAt, createdAt, 8)
			require.NoError(t, err)
			settings, err := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), createdAt)
			require.NoError(t, err)
			created, err := persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: pictureJob})
			require.NoError(t, err)
			user = created.User
			var prior *model.UserToken
			var priorDelivery *model.MailDelivery
			var priorJob *model.Job
			if test.priorToken {
				prior = &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
					TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: future.Add(24 * time.Hour)}
				prior.PrepareCreate(model.NewUserTokenID(), future)
				prior.UpdatedAt = future.Add(time.Hour)
				var occurrence *model.MailOccurrence
				occurrence, priorDelivery, priorJob = authenticationPolicyTestMail(t, user.ID, model.MailOccurrenceID(prior.ID.String()),
					model.MailOccurrenceAccountToken, model.MailTemplateIdentityVerifyEmail, model.JobTypeMailDeliverCredential,
					future, prior.ExpiresAt)
				prior, err = persistence.UserToken().Issue(ctx, &store.UserTokenMailIssue{Token: prior,
					Occurrence: occurrence, Delivery: priorDelivery, Job: priorJob,
					AuditEvent: authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String())})
				require.NoError(t, err)
			}

			preparedAt := future.Add(24 * time.Hour)
			tokenLifetime, warningLifetime := 37*time.Minute, 19*time.Hour
			token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
				TokenHash: model.HashToken(model.NewCredentialToken()), Target: "changed@example.edu", ExpiresAt: preparedAt.Add(tokenLifetime)}
			token.PrepareCreate(model.NewUserTokenID(), preparedAt)
			warningOccurrence, warningDelivery, warningJob := authenticationPolicyTestMail(t, user.ID, model.NewMailOccurrenceID(),
				model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver,
				preparedAt, preparedAt.Add(warningLifetime))
			verifyOccurrence, verifyDelivery, verifyJob := authenticationPolicyTestMail(t, user.ID, model.MailOccurrenceID(token.ID.String()),
				model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential,
				preparedAt, preparedAt.Add(tokenLifetime))
			audit := authenticationPolicyTestAudit("user.email.change", user.ID.String(), institution.ID.String())
			audit.Status = model.AuditStatusAttempt
			audit, err = persistence.Audit().Save(ctx, audit)
			require.NoError(t, err)
			before := databaseNow()
			changed, err := persistence.UserToken().ChangeEmail(ctx, &store.UserEmailChange{
				UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: token.Target, Token: token,
				TokenLifetime: tokenLifetime, WarningLifetime: warningLifetime,
				WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
				VerificationOccurrence: verifyOccurrence, VerificationDelivery: verifyDelivery, VerificationJob: verifyJob,
				AuditEventID: audit.ID, AuditAt: before,
			})
			require.NoError(t, err)
			after := databaseNow()
			persistedUser, err := persistence.User().Get(ctx, user.ID)
			require.NoError(t, err)
			err = persistedUser.Validate()
			require.NoError(t, err)
			require.True(t, persistedUser.CreatedAt.Equal(createdAt), "email change rewrote or moved the User lifecycle backward")
			require.False(t, persistedUser.UpdatedAt.Before(user.UpdatedAt), "email change rewrote or moved the User lifecycle backward")
			require.False(t, test.futureUpdated && !persistedUser.UpdatedAt.Equal(user.UpdatedAt), "email change rewrote or moved the User lifecycle backward")
			require.Equal(t, token.Target, persistedUser.Email, "email change did not persist the returned User transition")
			require.False(t, persistedUser.EmailVerified, "email change did not persist the returned User transition")
			require.Equal(t, user.Revision+1, persistedUser.Revision, "email change did not persist the returned User transition")
			require.True(t, persistedUser.UpdatedAt.Equal(changed.User.UpdatedAt), "email change did not persist the returned User transition")
			persistedToken, err := persistence.UserToken().Get(ctx, token.ID)
			require.NoError(t, err)
			err = persistedToken.Validate()
			require.NoError(t, err)
			at := persistedToken.CreatedAt
			require.False(t, at.Before(before), "replacement token lifetime did not use the current PostgreSQL instant")
			require.False(t, at.After(after), "replacement token lifetime did not use the current PostgreSQL instant")
			require.True(t, persistedToken.UpdatedAt.Equal(at), "replacement token lifetime did not use the current PostgreSQL instant")
			require.Equal(t, tokenLifetime, persistedToken.ExpiresAt.Sub(at), "replacement token lifetime did not use the current PostgreSQL instant")
			require.True(t, changed.Token.CreatedAt.Equal(at), "replacement token lifetime did not use the current PostgreSQL instant")
			for _, expected := range []struct {
				delivery *model.MailDelivery
				job      *model.Job
				lifetime time.Duration
			}{{warningDelivery, warningJob, warningLifetime}, {verifyDelivery, verifyJob, tokenLifetime}} {
				t.Run(fmt.Sprintf("lifetime=%v", expected.lifetime), func(t *testing.T) {
					delivery, err := persistence.Mail().GetDelivery(ctx, expected.delivery.ID)
					require.NoError(t, err)
					job, err := persistence.Job().Get(ctx, expected.job.ID)
					require.NoError(t, err)
					err = delivery.Validate()
					require.NoError(t, err)
					err = job.Validate()
					require.NoError(t, err)
					require.True(t, delivery.CreatedAt.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.True(t, delivery.UpdatedAt.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.True(t, delivery.MessageDate.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.Equal(t, expected.lifetime, delivery.Deadline.Sub(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.True(t, job.CreatedAt.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.True(t, job.UpdatedAt.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
					require.True(t, job.AvailableAt.Equal(at), "replacement mail or Job inherited a future lifecycle timestamp")
				})
			}
			if prior != nil {
				archived, err := persistence.UserToken().Get(ctx, prior.ID)
				require.NoError(t, err)
				err = archived.Validate()
				require.NoError(t, err)
				require.True(t, archived.CreatedAt.Equal(prior.CreatedAt), "prior token history or immediate invalidation was not preserved")
				require.True(t, archived.ExpiresAt.Equal(prior.ExpiresAt), "prior token history or immediate invalidation was not preserved")
				require.True(t, archived.UpdatedAt.Equal(prior.UpdatedAt), "prior token history or immediate invalidation was not preserved")
				require.True(t, archived.ArchivedAt.Valid, "prior token history or immediate invalidation was not preserved")
				require.True(t, archived.ArchivedAt.Time.Equal(prior.UpdatedAt), "prior token history or immediate invalidation was not preserved")
				require.False(t, archived.IsActiveAt(after), "prior token history or immediate invalidation was not preserved")
				delivery, err := persistence.Mail().GetDelivery(ctx, priorDelivery.ID)
				require.NoError(t, err)
				job, err := persistence.Job().Get(ctx, priorJob.ID)
				require.NoError(t, err)
				err = delivery.Validate()
				require.NoError(t, err)
				err = job.Validate()
				require.NoError(t, err)
				require.Equal(t, model.MailDeliverySuppressed, delivery.State, "superseded frozen mail was not safely suppressed")
				require.Empty(t, delivery.EncryptedPayload, "superseded frozen mail was not safely suppressed")
				require.True(t, delivery.CreatedAt.Equal(priorDelivery.CreatedAt), "superseded frozen mail was not safely suppressed")
				require.True(t, delivery.Deadline.Equal(priorDelivery.Deadline), "superseded frozen mail was not safely suppressed")
				require.Equal(t, model.JobStatusCanceled, job.Status, "superseded frozen mail was not safely suppressed")
				require.False(t, job.UpdatedAt.Before(priorJob.UpdatedAt), "superseded frozen mail was not safely suppressed")
			}
		})
	}
}

func TestPasswordResetSerializesWithPasswordRemoval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence, institution, user, password := passwordProofFixture(t, ctx)
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE access_policies
		SET provider_admissions=jsonb_build_object('campus-cas', ?::text) WHERE singleton=1`, string(model.ProviderAdmissionLinkedOnly))
	require.NoError(t, err)
	_, err = persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: user.ID, Provider: "campus-cas", Subject: "password-reset-removal",
		LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	})
	require.NoError(t, err)
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: user.ID, Action: string(model.ActionExternalIdentityManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "password-reset-removal-order",
	})
	require.NoError(t, err)
	completion := passwordProofReset(t, ctx, persistence, institution, user)
	controllerPID, release := pausePasswordProofWrite(t, ctx, persistence, true)
	type resetOutcome struct {
		result *store.PasswordResetResult
		err    error
	}
	resetDone := make(chan resetOutcome, 1)
	go func() {
		result, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
		resetDone <- resetOutcome{result: result, err: err}
	}()
	resetPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "UPDATE password_credentials")
	removal := &store.PasswordCredentialRemoval{
		UserID: user.ID, ChangedAt: model.NowUTC(), RevocationReason: model.SessionRevocationPasswordRemoved,
		AuditEventID: audit.ID, AuditAt: model.NowUTC(),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
	}
	type removalOutcome struct {
		result *store.AuthenticationMethodMutationResult
		err    error
	}
	removed := make(chan removalOutcome, 1)
	go func() {
		result, err := persistence.PasswordCredential().Remove(ctx, removal)
		removed <- removalOutcome{result: result, err: err}
	}()
	// The old reset order held the User row before acquiring this Session
	// fence: removal would own the fence and block on the User instead.
	waitForBlockedMailQuery(t, ctx, persistence, resetPID, "pg_advisory_xact_lock")
	release()
	reset, remove := <-resetDone, <-removed
	require.NoError(t, reset.err, "reset before removal")
	require.NotNil(t, reset.result, "reset before removal")
	require.True(t, reset.result.Token.ConsumedAt.Valid, "reset before removal")
	require.NoError(t, remove.err, "removal after reset")
	require.NotNil(t, remove.result, "removal after reset")
	require.NotNil(t, remove.result.PasswordCredential, "removal after reset")
	credential := remove.result.PasswordCredential
	require.True(t, credential.ArchivedAt.Valid, "removal did not archive the password produced by the preceding reset")
	require.Equal(t, password.Revision+1, credential.Revision, "removal did not archive the password produced by the preceding reset")
	require.Equal(t, completion.PasswordHash, credential.PasswordHash, "removal did not archive the password produced by the preceding reset")
	_, err = persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.True(t, store.IsNotFound(err), "password remained active after removal")
	terminal, err := persistence.Audit().Get(ctx, audit.ID)
	require.NoError(t, err, "removal audit = %#v", terminal)
	require.Equal(t, model.AuditStatusSuccess, terminal.Status, "removal audit = %#v", terminal)
}

func TestPasswordResetSerializesWithResetTokenReissue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence, institution, user, password := passwordProofFixture(t, ctx)
	completion := passwordProofReset(t, ctx, persistence, institution, user)
	next := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	next.PrepareCreate(model.NewUserTokenID(), model.NowUTC())
	occurrence, delivery, job := authenticationPolicyTestMail(t, next.UserID, model.MailOccurrenceID(next.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityPasswordReset, model.JobTypeMailDeliverCredential,
		next.CreatedAt, next.ExpiresAt)
	issue := &store.UserTokenMailIssue{Token: next, Occurrence: occurrence, Delivery: delivery, Job: job,
		AuditEvent: authenticationPolicyTestAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String())}
	controllerPID, release := pausePasswordProofWrite(t, ctx, persistence, true)
	type resetOutcome struct {
		result *store.PasswordResetResult
		err    error
	}
	resetDone := make(chan resetOutcome, 1)
	go func() {
		result, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
		resetDone <- resetOutcome{result: result, err: err}
	}()
	resetPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "UPDATE password_credentials")
	type issueOutcome struct {
		token *model.UserToken
		err   error
	}
	issued := make(chan issueOutcome, 1)
	go func() {
		token, err := persistence.UserToken().Issue(ctx, issue)
		issued <- issueOutcome{token: token, err: err}
	}()
	// Reissue must wait on the token-purpose fence already held by reset,
	// before taking the User or credential rows.
	waitForBlockedMailQuery(t, ctx, persistence, resetPID, "pg_advisory_xact_lock")
	release()
	reset, reissued := <-resetDone, <-issued
	require.NoError(t, reset.err, "reset before token reissue")
	require.NotNil(t, reset.result, "reset before token reissue")
	require.True(t, reset.result.Token.ConsumedAt.Valid, "reset before token reissue")
	require.NoError(t, reissued.err, "token reissue after reset")
	require.NotNil(t, reissued.token, "token reissue after reset")
	require.Equal(t, next.ID, reissued.token.ID, "token reissue after reset")
	current, err := persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, password.Revision+1, current.Revision)
	require.Equal(t, completion.PasswordHash, current.PasswordHash)
	retained, err := persistence.UserToken().Get(ctx, reset.result.Token.ID)
	require.NoError(t, err)
	require.True(t, retained.ConsumedAt.Valid)
	active, err := persistence.UserToken().GetByHash(ctx, next.TokenHash, model.UserTokenPasswordReset)
	require.NoError(t, err)
	require.False(t, active.ArchivedAt.Valid)
	require.False(t, active.ConsumedAt.Valid)
}

func TestEmailVerificationConsumptionUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "verification-consume-time", DisplayName: "Verification Consume Time"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "verification-consume-time", Email: "verification-consume-time@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	behindNodeAt := expireTokenAfterObservedNodeTime(t, ctx, persistence, token.ID)
	_, err = persistence.UserToken().ConsumeEmailVerification(ctx, token.TokenHash, behindNodeAt,
		authenticationPolicyTestAudit("authentication.email_verification.complete", "", institution.ID.String()))
	require.True(t, store.IsNotFound(err), "expired verification with behind-node time error")
	current, err := persistence.User().Get(ctx, user.ID)
	require.NoError(t, err, "expired verification changed User = %#v", current)
	require.False(t, current.EmailVerified, "expired verification changed User = %#v", current)
	retained, err := persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	require.NoError(t, err, "expired verification consumed token = %#v", retained)
	require.False(t, retained.ConsumedAt.Valid, "expired verification consumed token = %#v", retained)
	assertExpiredConsumptionAuditAbsent(t, ctx, persistence, "authentication.email_verification.complete")
}

func TestPasswordResetConsumptionUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "reset-consume-time", DisplayName: "Reset Consume Time"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "reset-consume-time", Email: "reset-consume-time@example.edu"})
	credential, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"})
	require.NoError(t, err)
	session, sessionCredentials := authenticationPolicyTestSession(user.ID, "password", "", "")
	session, _, err = storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, session, sessionCredentials, 10))
	require.NoError(t, err)
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	behindNodeAt := expireTokenAfterObservedNodeTime(t, ctx, persistence, token.ID)
	completion := authenticationPolicyTestResetCompletion(t, user, token.TokenHash, "encoded-must-not-commit",
		behindNodeAt, authenticationPolicyTestCompletionAudit(institution.ID.String()))
	_, err = persistence.UserToken().ConsumePasswordReset(ctx, completion)
	require.True(t, store.IsNotFound(err), "expired reset with behind-node time error")
	unchangedCredential, err := persistence.PasswordCredential().GetByUser(ctx, user.ID)
	require.NoError(t, err, "expired reset changed credential = %#v", unchangedCredential)
	require.Equal(t, credential.ID, unchangedCredential.ID, "expired reset changed credential = %#v", unchangedCredential)
	require.Equal(t, credential.PasswordHash, unchangedCredential.PasswordHash, "expired reset changed credential = %#v", unchangedCredential)
	unchangedSession, err := persistence.Session().Get(ctx, session.ID)
	require.NoError(t, err, "expired reset revoked Session = %#v", unchangedSession)
	require.False(t, unchangedSession.RevokedAt.Valid, "expired reset revoked Session = %#v", unchangedSession)
	retained, err := persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	require.NoError(t, err, "expired reset consumed token = %#v", retained)
	require.False(t, retained.ConsumedAt.Valid, "expired reset consumed token = %#v", retained)
	assertExpiredConsumptionAuditAbsent(t, ctx, persistence, "authentication.password_reset.complete")
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 10,
	})
	require.NoError(t, err, "expired reset deliveries")
	require.Empty(t, deliveries, "expired reset deliveries")
}

// TestUserTokenConsumptionPreservesLifecycleWhenNodeClockLeads moves every
// node-clock instant a consumption touches a minute after PostgreSQL time, as
// creating nodes whose clocks lead the database do. PostgreSQL still decides
// validity, but the recorded transition never precedes those lifecycles and
// stays one instant across the token, User, credential, Sessions, and audit.
func TestUserTokenConsumptionPreservesLifecycleWhenNodeClockLeads(t *testing.T) {
	lead := func(t *testing.T, ctx context.Context, persistence *SQLStore, query string, id string) {
		t.Helper()
		_, err := persistence.GetMaster().Exec(ctx, query, id)
		require.NoError(t, err)
	}
	setup := func(t *testing.T, ctx context.Context, persistence *SQLStore, purpose model.UserTokenPurpose, name string) (*model.Institution, *model.User, *model.UserToken) {
		t.Helper()
		institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: name, DisplayName: name})
		require.NoError(t, err)
		user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: name, Email: name + "@example.edu"})
		if purpose == model.UserTokenPasswordReset {
			credential, saveErr := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"})
			require.NoError(t, saveErr)
			lead(t, ctx, persistence, `UPDATE password_credentials SET created_at=created_at+INTERVAL '1 minute',
				updated_at=updated_at+INTERVAL '1 minute', password_changed_at=password_changed_at+INTERVAL '1 minute' WHERE id=?`, credential.ID.String())
		}
		token := &model.UserToken{UserID: user.ID, Purpose: purpose,
			TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
		action := "authentication.email_verification.request"
		if purpose == model.UserTokenPasswordReset {
			action = "authentication.password_reset.request"
		}
		token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
			authenticationPolicyTestAudit(action, user.ID.String(), institution.ID.String()))
		require.NoError(t, err)
		lead(t, ctx, persistence, `UPDATE users SET updated_at=updated_at+INTERVAL '1 minute' WHERE id=?`, user.ID.String())
		lead(t, ctx, persistence, `UPDATE user_tokens SET created_at=created_at+INTERVAL '1 minute',
			updated_at=updated_at+INTERVAL '1 minute', expires_at=expires_at+INTERVAL '1 minute' WHERE id=?`, token.ID.String())
		token, err = persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
		require.NoError(t, err)
		return institution, user, token
	}
	consumedAudit := func(t *testing.T, ctx context.Context, persistence *SQLStore, action string, at time.Time) {
		t.Helper()
		audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
			Action: action, Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		require.NoError(t, err, "consumption audits")
		require.Len(t, audits, 1, "consumption audits")
		require.True(t, audits[0].CreatedAt.Equal(at), "consumption audits")
	}

	t.Run("email_verification", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		persistence := openTestStore(t)
		resetTestStore(t, persistence)
		institution, user, token := setup(t, ctx, persistence, model.UserTokenEmailVerification, "verification-node-leads")
		result, err := persistence.UserToken().ConsumeEmailVerification(ctx, token.TokenHash, model.NowUTC(),
			authenticationPolicyTestAudit("authentication.email_verification.complete", "", institution.ID.String()))
		require.NoError(t, err)
		at := result.Token.ConsumedAt.Time
		require.False(t, at.Before(token.UpdatedAt), "verification result = %#v / %#v", result.Token, result.User)
		require.True(t, result.User.UpdatedAt.Equal(at), "verification result = %#v / %#v", result.Token, result.User)
		require.True(t, result.Token.UpdatedAt.Equal(at), "verification result = %#v / %#v", result.Token, result.User)
		persisted, getErr := persistence.User().Get(ctx, user.ID)
		require.NoError(t, getErr, "persisted User = %#v", persisted)
		require.True(t, persisted.UpdatedAt.Equal(at), "persisted User = %#v", persisted)
		persistedToken, getErr := persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
		require.NoError(t, getErr, "persisted token")
		require.True(t, persistedToken.ConsumedAt.Time.Equal(at), "persisted token = %#v", persistedToken)
		consumedAudit(t, ctx, persistence, "authentication.email_verification.complete", at)
	})

	t.Run("password_reset", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		persistence := openTestStore(t)
		resetTestStore(t, persistence)
		institution, user, token := setup(t, ctx, persistence, model.UserTokenPasswordReset, "reset-node-leads")
		session, sessionCredentials := authenticationPolicyTestSession(user.ID, "password", "", "")
		session, _, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, session, sessionCredentials, 10))
		require.NoError(t, err)
		leadSessionDatabaseClock(t, ctx, persistence, session.ID)
		completion := authenticationPolicyTestResetCompletion(t, user, token.TokenHash, "encoded-replacement-password",
			model.NowUTC(), authenticationPolicyTestCompletionAudit(institution.ID.String()))
		result, err := persistence.UserToken().ConsumePasswordReset(ctx, completion)
		require.NoError(t, err)
		at := result.Token.ConsumedAt.Time
		require.False(t, at.Before(token.UpdatedAt), "reset result = %#v / %#v", result.Token, result.PasswordCredential)
		require.True(t, result.PasswordCredential.PasswordChangedAt.Equal(at), "reset result = %#v / %#v", result.Token, result.PasswordCredential)
		require.Len(t, result.RevokedSessions, 1, "reset result = %#v / %#v", result.Token, result.PasswordCredential)
		require.True(t, result.RevokedSessions[0].RevokedAt.Time.Equal(at), "reset result = %#v / %#v", result.Token, result.PasswordCredential)
		persisted, getErr := persistence.Session().Get(ctx, session.ID)
		require.NoError(t, getErr, "persisted Session = %#v", persisted)
		require.True(t, persisted.RevokedAt.Time.Equal(at), "persisted Session = %#v", persisted)
		persistedCredential, getErr := persistence.PasswordCredential().GetByUser(ctx, user.ID)
		require.NoError(t, getErr, "persisted credential")
		require.True(t, persistedCredential.PasswordChangedAt.Equal(at), "persisted credential = %#v", persistedCredential)
		require.True(t, persistedCredential.UpdatedAt.Equal(at), "persisted credential = %#v", persistedCredential)
		consumedAudit(t, ctx, persistence, "authentication.password_reset.complete", at)
	})
}

func assertExpiredConsumptionAuditAbsent(t *testing.T, ctx context.Context, persistence *SQLStore, action string) {
	t.Helper()
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: action, Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "expired consumption audits")
	require.Empty(t, audits, "expired consumption audits")
}

func expireTokenAfterObservedNodeTime(t *testing.T, ctx context.Context, persistence *SQLStore, tokenID model.UserTokenID) time.Time {
	t.Helper()
	var behindNodeAt time.Time
	require.NoError(t, persistence.GetMaster().Get(ctx, &behindNodeAt, `SELECT GREATEST(clock_timestamp(), created_at + interval '10 milliseconds') FROM user_tokens WHERE id=?`, tokenID.String()))
	expiresAt := model.TimeUTC(behindNodeAt).Add(time.Millisecond)
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE user_tokens SET expires_at=? WHERE id=?`, expiresAt, tokenID.String())
	require.NoError(t, err)
	for {
		var expired bool
		require.NoError(t, persistence.GetMaster().Get(ctx, &expired, `SELECT clock_timestamp()>=?`, expiresAt))
		if expired {
			return model.TimeUTC(behindNodeAt)
		}
		require.NoError(t, ctx.Err())
	}
}

// TestUserTokenReissueFencesConcurrentDeliveryStart proves the purpose lock
// ordering from PostgreSQL's blocking graph. Reissue is paused only after it
// owns the token-purpose fence; StartDelivery must wait behind that exact
// transaction and therefore cannot start the superseded snapshot afterward.
func TestUserTokenReissueFencesConcurrentDeliveryStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-mail-fence", DisplayName: "Token Mail Fence"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-mail-fence", Email: "token-mail-fence@example.edu"})
	first := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	first, err = authenticationPolicyTestIssue(t, ctx, persistence, first,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	require.NoError(t, err, "initial delivery")
	require.Len(t, deliveries, 1, "initial delivery")
	oldDelivery := deliveries[0]

	const advisoryKey int64 = 8154700260819
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	var controllerPID int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	_, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_token_reissue() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260819);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_token_reissue BEFORE INSERT ON user_tokens
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_token_reissue()`)
	require.NoError(t, err)
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_token_reissue ON user_tokens; DROP FUNCTION IF EXISTS proctor_test_pause_token_reissue()`)
	}()

	type issueResult struct {
		token *model.UserToken
		err   error
	}
	issued := make(chan issueResult, 1)
	second := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	second.PrepareCreate(model.NewUserTokenID(), model.NowUTC())
	occurrence, delivery, job := authenticationPolicyTestMail(t, second.UserID, model.MailOccurrenceID(second.ID.String()), model.MailOccurrenceAccountToken,
		model.MailTemplateIdentityVerifyEmail, model.JobTypeMailDeliverCredential, second.CreatedAt, second.ExpiresAt)
	issue := &store.UserTokenMailIssue{Token: second, Occurrence: occurrence, Delivery: delivery, Job: job,
		AuditEvent: authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String())}
	go func() {
		token, issueErr := persistence.UserToken().Issue(ctx, issue)
		issued <- issueResult{token: token, err: issueErr}
	}()
	reissuePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "INSERT INTO user_tokens")
	started := make(chan error, 1)
	go func() {
		_, startErr := persistence.Mail().StartDelivery(ctx, oldDelivery.ID, oldDelivery.Revision, model.NowUTC())
		started <- startErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, reissuePID, "pg_advisory_xact_lock")

	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey)
	require.NoError(t, err)
	locked = false
	result := <-issued
	require.NoError(t, result.err, "reissue")
	require.NotNil(t, result.token, "reissue")
	require.True(t, store.IsConflict((<-started)), "concurrent stale StartDelivery error")
	oldDelivery, err = persistence.Mail().GetDelivery(ctx, oldDelivery.ID)
	require.NoError(t, err, "superseded delivery = %#v", oldDelivery)
	require.Equal(t, model.MailDeliverySuppressed, oldDelivery.State, "superseded delivery = %#v", oldDelivery)
	require.Equal(t, model.MailDeliveryObsoleteCode, oldDelivery.PublicFailureCode, "superseded delivery = %#v", oldDelivery)
	require.Empty(t, oldDelivery.EncryptedPayload, "superseded delivery = %#v", oldDelivery)
}

// TestUserTokenConsumptionFencesConcurrentDeliveryStart proves that token
// consumption and the worker's relevance check share the same authoritative
// purpose fence. Consumption is paused after it owns that fence; StartDelivery
// must block and then suppress the now-consumed credential instead of sending.
func TestUserTokenConsumptionFencesConcurrentDeliveryStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-consume-fence", DisplayName: "Token Consume Fence"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-consume-fence", Email: "token-consume-fence@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	require.NoError(t, err, "delivery")
	require.Len(t, deliveries, 1, "delivery")
	delivery := deliveries[0]

	const advisoryKey int64 = 8154700260820
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	var controllerPID int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	_, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_token_consumption() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260820);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_token_consumption BEFORE UPDATE ON user_tokens
		FOR EACH ROW WHEN (OLD.consumed_at IS NULL AND NEW.consumed_at IS NOT NULL)
		EXECUTE FUNCTION proctor_test_pause_token_consumption()`)
	require.NoError(t, err)
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_token_consumption ON user_tokens; DROP FUNCTION IF EXISTS proctor_test_pause_token_consumption()`)
	}()

	consumed := make(chan error, 1)
	consumeAt := model.TimeUTC(token.CreatedAt).Add(1 * time.Millisecond)
	go func() {
		_, consumeErr := persistence.UserToken().ConsumeEmailVerification(ctx, token.TokenHash, consumeAt,
			authenticationPolicyTestAudit("authentication.email_verification.complete", "", institution.ID.String()))
		consumed <- consumeErr
	}()
	consumePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "UPDATE user_tokens")
	started := make(chan struct {
		delivery *model.MailDelivery
		err      error
	}, 1)
	go func() {
		result, startErr := persistence.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
		started <- struct {
			delivery *model.MailDelivery
			err      error
		}{result, startErr}
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, consumePID, "pg_advisory_xact_lock")
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey)
	require.NoError(t, err)
	locked = false
	require.NoError(t, (<-consumed))
	start := <-started
	require.NoError(t, start.err, "StartDelivery after consumption")
	require.NotNil(t, start.delivery, "StartDelivery after consumption")
	require.Equal(t, model.MailDeliverySuppressed, start.delivery.State, "StartDelivery after consumption")
	require.Equal(t, model.MailDeliveryObsoleteCode, start.delivery.PublicFailureCode, "StartDelivery after consumption")
	require.Empty(t, start.delivery.EncryptedPayload, "StartDelivery after consumption")
}

func TestRecoveryDeliveryRelevanceUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-database-time", DisplayName: "Token Database Time"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-database-time", Email: "token-database-time@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `UPDATE user_tokens SET
		created_at=clock_timestamp()-interval '2 seconds',
		updated_at=clock_timestamp()-interval '2 seconds',
		expires_at=clock_timestamp()-interval '1 second'
		WHERE id=?`, token.ID.String())
	require.NoError(t, err)
	var expiredAtDatabase bool
	err = persistence.GetMaster().Get(ctx, &expiredAtDatabase, `SELECT expires_at < clock_timestamp() FROM user_tokens WHERE id=?`, token.ID.String())
	require.NoError(t, err, "database expiry observation")
	require.True(t, expiredAtDatabase, "database expiry observation")
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	require.NoError(t, err, "delivery")
	require.Len(t, deliveries, 1, "delivery")
	// The supplied node time deliberately predates expiry. PostgreSQL time is
	// authoritative and must still suppress the expired token.
	result, err := persistence.Mail().StartDelivery(ctx, deliveries[0].ID, deliveries[0].Revision, token.CreatedAt)
	require.NoError(t, err, "StartDelivery with stale node clock = %#v", result)
	require.Equal(t, model.MailDeliverySuppressed, result.State, "StartDelivery with stale node clock = %#v", result)
	require.Equal(t, model.MailDeliveryObsoleteCode, result.PublicFailureCode, "StartDelivery with stale node clock = %#v", result)
}

// TestUserTokenIssueSerializesWithTargetChange proves Issue rechecks the exact
// persisted mailbox behind the same token-purpose fence as the named email
// transition. The transition is paused after owning the fence; Issue must wait
// and then reject its stale target without persisting token or delivery intent.
func TestUserTokenIssueSerializesWithTargetChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-target-fence", DisplayName: "Token Target Fence"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-target-fence", Email: "token-target-fence@example.edu"})
	staleEmail := user.Email

	const advisoryKey int64 = 8154700260821
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	var controllerPID int
	err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID)
	require.NoError(t, err)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	_, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_user_email_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260821);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_user_email_change BEFORE UPDATE ON users
		FOR EACH ROW WHEN (OLD.email IS DISTINCT FROM NEW.email)
		EXECUTE FUNCTION proctor_test_pause_user_email_change()`)
	require.NoError(t, err)
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_user_email_change ON users; DROP FUNCTION IF EXISTS proctor_test_pause_user_email_change()`)
	}()

	updated := make(chan error, 1)
	changedEmail := "token-target-changed@example.edu"
	changeAt := model.NowUTC()
	changeToken := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: changedEmail, ExpiresAt: changeAt.Add(time.Hour)}
	changeToken.PrepareCreate(model.NewUserTokenID(), changeAt)
	warningOccurrence, warningDelivery, warningJob := authenticationPolicyTestMail(t, user.ID, model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver,
		changeAt, changeAt.Add(24*time.Hour))
	verificationOccurrence, verificationDelivery, verificationJob := authenticationPolicyTestMail(t, user.ID, model.MailOccurrenceID(changeToken.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential,
		changeAt, changeAt.Add(time.Hour))
	changeAudit := authenticationPolicyTestAudit("user.email.change", user.ID.String(), institution.ID.String())
	changeAudit.Status = model.AuditStatusAttempt
	changeAudit, err = persistence.Audit().Save(ctx, changeAudit)
	require.NoError(t, err)
	change := &store.UserEmailChange{UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: changedEmail, Token: changeToken,
		TokenLifetime: time.Hour, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verificationOccurrence, VerificationDelivery: verificationDelivery, VerificationJob: verificationJob,
		AuditEventID: changeAudit.ID, AuditAt: model.NowUTC()}
	go func() {
		_, updateErr := persistence.UserToken().ChangeEmail(ctx, change)
		updated <- updateErr
	}()
	updatePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "UPDATE users")
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: staleEmail, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token.PrepareCreate(model.NewUserTokenID(), model.NowUTC())
	occurrence, delivery, job := authenticationPolicyTestMail(t, token.UserID, model.MailOccurrenceID(token.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityVerifyEmail, model.JobTypeMailDeliverCredential,
		token.CreatedAt, token.ExpiresAt)
	input := &store.UserTokenMailIssue{Token: token, Occurrence: occurrence, Delivery: delivery, Job: job,
		AuditEvent: authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String())}
	issued := make(chan error, 1)
	go func() {
		_, issueErr := persistence.UserToken().Issue(ctx, input)
		issued <- issueErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, updatePID, "pg_advisory_xact_lock")
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey)
	require.NoError(t, err)
	locked = false
	require.NoError(t, (<-updated))
	require.True(t, store.IsNotFound((<-issued)), "Issue after target change")
}

// TestUserDisableAndEmailChangeShareUserEligibilityLockOrder proves account
// state and mailbox transitions both acquire the User before the installation
// mail-eligibility singleton. Holding the User while both commands queue makes
// an inverse singleton -> User path form a deterministic PostgreSQL deadlock.
func TestUserDisableAndEmailChangeShareUserEligibilityLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "disable-email-order", DisplayName: "Disable Email Order",
	})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "disable-email-order", Email: "disable-email-order@example.edu",
	})

	changeAt := model.NowUTC()
	newEmail := "disable-email-order-changed@example.edu"
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: changeAt.Add(time.Hour)}
	token.PrepareCreate(model.NewUserTokenID(), changeAt)
	warningOccurrence, warningDelivery, warningJob := authenticationPolicyTestMail(t, user.ID, model.NewMailOccurrenceID(),
		model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliver,
		changeAt, changeAt.Add(24*time.Hour))
	verificationOccurrence, verificationDelivery, verificationJob := authenticationPolicyTestMail(t, user.ID,
		model.MailOccurrenceID(token.ID.String()), model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew,
		model.JobTypeMailDeliverCredential, changeAt, changeAt.Add(time.Hour))
	changeAudit := authenticationPolicyTestAudit("user.email.change", user.ID.String(), institution.ID.String())
	changeAudit.Status = model.AuditStatusAttempt
	changeAudit, err = persistence.Audit().Save(ctx, changeAudit)
	require.NoError(t, err)
	change := &store.UserEmailChange{UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: newEmail, Token: token,
		TokenLifetime: time.Hour, WarningLifetime: 24 * time.Hour,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verificationOccurrence, VerificationDelivery: verificationDelivery, VerificationJob: verificationJob,
		AuditEventID: changeAudit.ID, AuditAt: model.NowUTC()}
	disableAt := model.NowUTC()
	disableAudit := authenticationPolicyTestAudit("user.disable", user.ID.String(), institution.ID.String())
	disableAudit.Status = model.AuditStatusAttempt
	disableAudit, err = persistence.Audit().Save(ctx, disableAudit)
	require.NoError(t, err)
	disable := storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true, ChangedAt: disableAt,
		RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID, AuditAt: disableAt,
	})

	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	controllerBackendPID := controllerPID(t, ctx, controller)
	_, err = controller.ExecContext(ctx, `BEGIN`)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	_, err = controller.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user.ID.String())
	require.NoError(t, err)

	changeResult := make(chan error, 1)
	go func() {
		_, changeErr := persistence.UserToken().ChangeEmail(ctx, change)
		changeResult <- changeErr
	}()
	changePID := waitForBlockedMailQuery(t, ctx, persistence, controllerBackendPID, "FROM users WHERE id")
	disableResult := make(chan error, 1)
	go func() {
		_, disableErr := persistence.User().SetDisabled(ctx, disable)
		disableResult <- disableErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, changePID, "SELECT id FROM users")
	_, err = controller.ExecContext(ctx, `ROLLBACK`)
	require.NoError(t, err)
	locked = false

	changeErr, disableErr := <-changeResult, <-disableResult
	require.NoError(t, changeErr)
	require.True(t, store.IsConflict(disableErr), "SetDisabled after email change")
}
