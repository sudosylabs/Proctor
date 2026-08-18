//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestEmailVerificationConsumptionUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "verification-consume-time", DisplayName: "Verification Consume Time"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "verification-consume-time", Email: "verification-consume-time@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	behindNodeAt := expireTokenAfterObservedNodeTime(t, ctx, persistence, token.ID)
	if _, err = persistence.UserToken().ConsumeEmailVerification(ctx, token.TokenHash, model.MillisFromTime(behindNodeAt),
		authenticationPolicyTestAudit("authentication.email_verification.complete", "", institution.ID.String())); !store.IsNotFound(err) {
		t.Fatalf("expired verification with behind-node time error = %v, want not found", err)
	}
	current, err := persistence.User().Get(ctx, user.ID.String())
	if err != nil || current.EmailVerified {
		t.Fatalf("expired verification changed User = %#v, %v", current, err)
	}
	retained, err := persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	if err != nil || retained.ConsumedAt.Valid {
		t.Fatalf("expired verification consumed token = %#v, %v", retained, err)
	}
	assertExpiredConsumptionAuditAbsent(t, ctx, persistence, "authentication.email_verification.complete")
}

func TestPasswordResetConsumptionUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "reset-consume-time", DisplayName: "Reset Consume Time"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "reset-consume-time", Email: "reset-consume-time@example.edu"})
	credential, err := persistence.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"})
	if err != nil {
		t.Fatal(err)
	}
	session, sessionCredentials := authenticationPolicyTestSession(user.ID, "password", "")
	session, _, err = persistence.Session().Save(ctx, session, sessionCredentials, 10)
	if err != nil {
		t.Fatal(err)
	}
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenPasswordReset,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.password_reset.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	behindNodeAt := expireTokenAfterObservedNodeTime(t, ctx, persistence, token.ID)
	completion := authenticationPolicyTestResetCompletion(t, user, token.TokenHash, "encoded-must-not-commit",
		model.MillisFromTime(behindNodeAt), authenticationPolicyTestCompletionAudit(institution.ID.String()))
	if _, err = persistence.UserToken().ConsumePasswordReset(ctx, completion); !store.IsNotFound(err) {
		t.Fatalf("expired reset with behind-node time error = %v, want not found", err)
	}
	unchangedCredential, err := persistence.PasswordCredential().GetByUser(ctx, user.ID.String())
	if err != nil || unchangedCredential.ID != credential.ID || unchangedCredential.PasswordHash != credential.PasswordHash {
		t.Fatalf("expired reset changed credential = %#v, %v", unchangedCredential, err)
	}
	unchangedSession, err := persistence.Session().Get(ctx, session.ID.String())
	if err != nil || unchangedSession.RevokedAt.Valid {
		t.Fatalf("expired reset revoked Session = %#v, %v", unchangedSession, err)
	}
	retained, err := persistence.UserToken().GetByHash(ctx, token.TokenHash, token.Purpose)
	if err != nil || retained.ConsumedAt.Valid {
		t.Fatalf("expired reset consumed token = %#v, %v", retained, err)
	}
	assertExpiredConsumptionAuditAbsent(t, ctx, persistence, "authentication.password_reset.complete")
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 10,
	})
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("expired reset deliveries = %#v, %v", deliveries, err)
	}
}

func assertExpiredConsumptionAuditAbsent(t *testing.T, ctx context.Context, persistence *SQLStore, action string) {
	t.Helper()
	audits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		Action: action, Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil || len(audits) != 0 {
		t.Fatalf("expired consumption audits = %#v, %v", audits, err)
	}
}

func expireTokenAfterObservedNodeTime(t *testing.T, ctx context.Context, persistence *SQLStore, tokenID model.UserTokenID) time.Time {
	t.Helper()
	var behindNodeAt time.Time
	if err := persistence.GetMaster().Get(ctx, &behindNodeAt, `SELECT GREATEST(clock_timestamp(), created_at + interval '10 milliseconds') FROM user_tokens WHERE id=?`, tokenID.String()); err != nil {
		t.Fatal(err)
	}
	expiresAt := model.TimeUTC(behindNodeAt).Add(time.Millisecond)
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE user_tokens SET expires_at=? WHERE id=?`, expiresAt, tokenID.String()); err != nil {
		t.Fatal(err)
	}
	for {
		var expired bool
		if err := persistence.GetMaster().Get(ctx, &expired, `SELECT clock_timestamp()>=?`, expiresAt); err != nil {
			t.Fatal(err)
		}
		if expired {
			return model.TimeUTC(behindNodeAt)
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-mail-fence", Email: "token-mail-fence@example.edu"})
	first := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	first, err = authenticationPolicyTestIssue(t, ctx, persistence, first,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("initial delivery = %#v, %v", deliveries, err)
	}
	oldDelivery := deliveries[0]

	const advisoryKey int64 = 8154700260819
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_token_reissue() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260819);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_token_reissue BEFORE INSERT ON user_tokens
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_token_reissue()`); err != nil {
		t.Fatal(err)
	}
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

	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	result := <-issued
	if result.err != nil || result.token == nil {
		t.Fatalf("reissue = %#v, %v", result.token, result.err)
	}
	if startErr := <-started; !store.IsConflict(startErr) {
		t.Fatalf("concurrent stale StartDelivery error = %v, want conflict", startErr)
	}
	oldDelivery, err = persistence.Mail().GetDelivery(ctx, oldDelivery.ID)
	if err != nil || oldDelivery.State != model.MailDeliverySuppressed || oldDelivery.PublicFailureCode != model.MailDeliveryObsoleteCode || len(oldDelivery.EncryptedPayload) != 0 {
		t.Fatalf("superseded delivery = %#v, %v", oldDelivery, err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-consume-fence", Email: "token-consume-fence@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("delivery = %#v, %v", deliveries, err)
	}
	delivery := deliveries[0]

	const advisoryKey int64 = 8154700260820
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_token_consumption() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260820);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_token_consumption BEFORE UPDATE ON user_tokens
		FOR EACH ROW WHEN (OLD.consumed_at IS NULL AND NEW.consumed_at IS NOT NULL)
		EXECUTE FUNCTION proctor_test_pause_token_consumption()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_token_consumption ON user_tokens; DROP FUNCTION IF EXISTS proctor_test_pause_token_consumption()`)
	}()

	consumed := make(chan error, 1)
	consumeAt := model.MillisFromTime(token.CreatedAt) + 1
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
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	if consumeErr := <-consumed; consumeErr != nil {
		t.Fatalf("consume = %v", consumeErr)
	}
	start := <-started
	if start.err != nil || start.delivery == nil || start.delivery.State != model.MailDeliverySuppressed ||
		start.delivery.PublicFailureCode != model.MailDeliveryObsoleteCode || len(start.delivery.EncryptedPayload) != 0 {
		t.Fatalf("StartDelivery after consumption = %#v, %v", start.delivery, start.err)
	}
}

func TestRecoveryDeliveryRelevanceUsesPostgreSQLTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-database-time", DisplayName: "Token Database Time"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-database-time", Email: "token-database-time@example.edu"})
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: user.Email, ExpiresAt: model.NowUTC().Add(time.Hour)}
	token, err = authenticationPolicyTestIssue(t, ctx, persistence, token,
		authenticationPolicyTestAudit("authentication.email_verification.request", user.ID.String(), institution.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE user_tokens SET expires_at=created_at + interval '1 microsecond' WHERE id=?`, token.ID.String()); err != nil {
		t.Fatal(err)
	}
	var expiredAtDatabase bool
	if err = persistence.GetMaster().Get(ctx, &expiredAtDatabase, `SELECT expires_at < clock_timestamp() FROM user_tokens WHERE id=?`, token.ID.String()); err != nil || !expiredAtDatabase {
		t.Fatalf("database expiry observation = %t, %v", expiredAtDatabase, err)
	}
	deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityVerifyEmail}, Limit: 10})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("delivery = %#v, %v", deliveries, err)
	}
	// The supplied node time deliberately predates expiry. PostgreSQL time is
	// authoritative and must still suppress the expired token.
	result, err := persistence.Mail().StartDelivery(ctx, deliveries[0].ID, deliveries[0].Revision, token.CreatedAt)
	if err != nil || result.State != model.MailDeliverySuppressed || result.PublicFailureCode != model.MailDeliveryObsoleteCode {
		t.Fatalf("StartDelivery with stale node clock = %#v, %v", result, err)
	}
}

// TestUserTokenIssueSerializesWithTargetChange proves Issue rechecks the exact
// persisted mailbox while holding a User-row lock. The profile update is
// paused after owning that row; Issue must wait and then reject its stale
// target without persisting any token or delivery intent.
func TestUserTokenIssueSerializesWithTargetChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "token-target-fence", DisplayName: "Token Target Fence"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "token-target-fence", Email: "token-target-fence@example.edu"})
	staleEmail := user.Email

	const advisoryKey int64 = 8154700260821
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_user_email_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260821);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_user_email_change BEFORE UPDATE ON users
		FOR EACH ROW WHEN (OLD.email IS DISTINCT FROM NEW.email)
		EXECUTE FUNCTION proctor_test_pause_user_email_change()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_user_email_change ON users; DROP FUNCTION IF EXISTS proctor_test_pause_user_email_change()`)
	}()

	updated := make(chan error, 1)
	changed := *user
	changed.Email = "token-target-changed@example.edu"
	go func() {
		_, updateErr := persistence.User().Update(ctx, &changed)
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
	_ = waitForBlockedMailQuery(t, ctx, persistence, updatePID, "FROM users")
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	if updateErr := <-updated; updateErr != nil {
		t.Fatalf("email update = %v", updateErr)
	}
	if issueErr := <-issued; !store.IsNotFound(issueErr) {
		t.Fatalf("Issue after target change = %v, want not found", issueErr)
	}
}
