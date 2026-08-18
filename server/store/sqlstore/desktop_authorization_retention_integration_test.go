//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestDesktopAuthorizationMaintenanceIsBoundedAndMultiNodeSafe(t *testing.T) {
	ctx := context.Background()
	first := openTestStore(t)
	resetTestStore(t, first)
	second := openTestStore(t)
	institution, err := first.Institution().Save(ctx, &model.Institution{Name: "desktop-maintenance", DisplayName: "Desktop Maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	const total = 24
	for index := 0; index < total; index++ {
		transaction, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC().Add(-10*time.Minute), institution.ID)
		if _, err = first.DesktopAuthorization().Create(ctx, transaction); err != nil {
			t.Fatal(err)
		}
		if _, err = first.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
			SET created_at=?,updated_at=?,expires_at=? WHERE id=?`, transaction.CreatedAt, transaction.CreatedAt,
			transaction.ExpiresAt, transaction.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		result *store.DesktopAuthorizationMaintenanceResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for _, candidate := range []store.DesktopAuthorizationStore{first.DesktopAuthorization(), second.DesktopAuthorization()} {
		wait.Add(1)
		go func(maintenance store.DesktopAuthorizationStore) {
			defer wait.Done()
			result, maintainErr := maintenance.Maintain(ctx, 10)
			outcomes <- outcome{result: result, err: maintainErr}
		}(candidate)
	}
	wait.Wait()
	close(outcomes)
	expired := 0
	for result := range outcomes {
		if result.err != nil || result.result == nil {
			t.Fatalf("concurrent Maintain() = %#v, %v", result.result, result.err)
		}
		expired += result.result.Expired
	}
	if expired != 20 {
		t.Fatalf("concurrent expired rows = %d, want 20", expired)
	}
	last, err := first.DesktopAuthorization().Maintain(ctx, 10)
	if err != nil || last.Expired != 4 || last.More {
		t.Fatalf("final Maintain() = %#v, %v", last, err)
	}
	var activeProofs int
	if err = first.GetMaster().Get(ctx, &activeProofs, `SELECT COUNT(*) FROM browser_authentication_transactions
		WHERE state <> 'expired' OR handle_hash IS NOT NULL OR browser_proof_hash IS NOT NULL OR state_hash IS NOT NULL
		   OR callback_url IS NOT NULL OR code_challenge IS NOT NULL OR code_hash IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if activeProofs != 0 {
		t.Fatalf("maintenance left %d rows live or proof-bearing", activeProofs)
	}
}

func TestDesktopAuthorizationExchangeSerializesWithConcurrentUserDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-disable-race", DisplayName: "Desktop Disable Race"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-disable-race", Email: "desktop-disable-race@example.edu"})
	_, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	exchangeAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "exchange")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID)

	const pauseKey int64 = 8154700260819
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()
	if _, err = persistence.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_desktop_session() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260819);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_desktop_session BEFORE INSERT ON sessions
		FOR EACH ROW EXECUTE FUNCTION proctor_test_pause_desktop_session()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_desktop_session ON sessions; DROP FUNCTION IF EXISTS proctor_test_pause_desktop_session()`)
	}()

	type exchangeOutcome struct {
		result *store.DesktopAuthorizationExchangeResult
		err    error
	}
	exchangeResult := make(chan exchangeOutcome, 1)
	go func() {
		result, exchangeErr := persistence.DesktopAuthorization().Exchange(ctx,
			desktopAuthorizationExchangeForSQLTest(code, state, verifier, exchangeAudit))
		exchangeResult <- exchangeOutcome{result: result, err: exchangeErr}
	}()
	exchangePID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "INSERT INTO sessions")

	type disableOutcome struct {
		result *store.UserDisabledStateResult
		err    error
	}
	disableResult := make(chan disableOutcome, 1)
	go func() {
		result, disableErr := persistence.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.GetMillis(),
			RevocationReason: "concurrent disable", AuditEventID: disableAudit.ID.String(), AuditAt: model.GetMillis(),
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		}))
		disableResult <- disableOutcome{result: result, err: disableErr}
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, exchangePID, "pg_advisory_xact_lock")
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	exchanged := <-exchangeResult
	if exchanged.err != nil || exchanged.result == nil || exchanged.result.Session == nil {
		t.Fatalf("Exchange() = %#v, %v", exchanged.result, exchanged.err)
	}
	disabled := <-disableResult
	if disabled.err != nil || disabled.result == nil || disabled.result.User == nil || !disabled.result.User.DisabledAt.Valid {
		t.Fatalf("SetDisabledWithAudit() = %#v, %v", disabled.result, disabled.err)
	}
	storedSession, err := persistence.Session().Get(ctx, exchanged.result.Session.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !storedSession.RevokedAt.Valid || storedSession.RevocationReason != "concurrent disable" {
		t.Fatalf("session escaped concurrent disable: %#v", storedSession)
	}
}

func TestDesktopAuthorizationIssueCodeSerializesWithConcurrentUserDisableAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primary := openTestStore(t)
	resetTestStore(t, primary)
	secondary := openTestStore(t)
	institution, err := primary.Institution().Save(ctx, &model.Institution{Name: "desktop-issue-disable-race", DisplayName: "Desktop Issue Disable Race"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, primary, &model.User{Username: "desktop-issue-disable-race", Email: "desktop-issue-disable-race@example.edu"})
	transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = primary.DesktopAuthorization().Create(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	issueAudit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, primary, institution.ID, user.ID, "issue-disable-race")
	disableAudit := saveUserStateAuditForSQLTest(t, ctx, primary, institution.ID, user.ID)

	const pauseKey int64 = 8154700260822
	controller, err := primary.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()
	if _, err = primary.GetMaster().Exec(ctx, `
		CREATE OR REPLACE FUNCTION proctor_test_pause_desktop_code_issue() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(8154700260822);
			RETURN NEW;
		END $$;
		CREATE TRIGGER proctor_test_pause_desktop_code_issue BEFORE UPDATE ON browser_authentication_transactions
		FOR EACH ROW WHEN (NEW.state = 'code_issued') EXECUTE FUNCTION proctor_test_pause_desktop_code_issue()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = primary.GetMaster().Exec(context.Background(), `DROP TRIGGER IF EXISTS proctor_test_pause_desktop_code_issue ON browser_authentication_transactions; DROP FUNCTION IF EXISTS proctor_test_pause_desktop_code_issue()`)
	}()

	type issueOutcome struct {
		result *model.BrowserAuthenticationTransaction
		err    error
	}
	issueResult := make(chan issueOutcome, 1)
	go func() {
		result, issueErr := primary.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
			HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
			UserID: user.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt: transaction.CreatedAt.UnixMilli(), CodeHash: model.HashToken(model.NewCredentialToken()),
			CodeLifetime: 45 * time.Second,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
		})
		issueResult <- issueOutcome{result: result, err: issueErr}
	}()
	issuePID := waitForBlockedMailQuery(t, ctx, primary, controllerPID, "UPDATE browser_authentication_transactions")

	type disableOutcome struct {
		result *store.UserDisabledStateResult
		err    error
	}
	disableResult := make(chan disableOutcome, 1)
	go func() {
		result, disableErr := secondary.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true, ChangedAt: model.GetMillis(),
			RevocationReason: "concurrent disable", AuditEventID: disableAudit.ID.String(), AuditAt: model.GetMillis(),
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		}))
		disableResult <- disableOutcome{result: result, err: disableErr}
	}()
	_ = waitForBlockedMailQuery(t, ctx, primary, issuePID, "pg_advisory_xact_lock")
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked = false
	issued := <-issueResult
	if issued.err != nil || issued.result == nil || issued.result.State != model.BrowserAuthenticationStateCodeIssued {
		t.Fatalf("IssueCode() = %#v, %v", issued.result, issued.err)
	}
	disabled := <-disableResult
	if disabled.err != nil || disabled.result == nil || disabled.result.User == nil || !disabled.result.User.DisabledAt.Valid {
		t.Fatalf("SetDisabledWithAudit() = %#v, %v", disabled.result, disabled.err)
	}
	after, err := primary.Audit().Get(ctx, issueAudit.ID.String())
	if err != nil || after.Status != model.AuditStatusSuccess {
		t.Fatalf("IssueCode audit = %#v, %v", after, err)
	}
}

func TestDesktopAuthorizationRetentionTerminalizesEveryLiveStateAndPurgesSafeMetadata(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-retention", DisplayName: "Desktop Retention"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-retention", Email: "desktop-retention@example.edu"})

	issued, code, state, verifier := issueDesktopAuthorizationForSQLTest(t, ctx, persistence, institution.ID, user.ID)
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET created_at=aged.now-interval '3 minutes', updated_at=aged.now-interval '2 minutes',
		    expires_at=aged.now+interval '2 minutes', authenticated_at=aged.now-interval '2 minutes',
		    code_expires_at=aged.now-interval '1 minute'
		FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, issued.ID.String()); err != nil {
		t.Fatal(err)
	}
	maintained, err := persistence.DesktopAuthorization().Maintain(ctx, 500)
	if err != nil || maintained.Expired != 1 {
		t.Fatalf("Maintain(expired code) = %#v, %v", maintained, err)
	}
	var expired desktopAuthorizationRow
	if err = persistence.GetMaster().Get(ctx, &expired, `SELECT `+desktopAuthorizationColumns+` FROM browser_authentication_transactions WHERE id=?`, issued.ID.String()); err != nil {
		t.Fatal(err)
	}
	expiredModel, err := expired.model()
	if err != nil {
		t.Fatal(err)
	}
	if expiredModel.State != model.BrowserAuthenticationStateExpired || expiredModel.StateHash != "" ||
		expiredModel.CallbackURL != "" || expiredModel.CodeChallenge != "" || expiredModel.CodeHash != "" ||
		!expiredModel.UserID.IsValid() || !expiredModel.ExpiredAt.Valid {
		t.Fatalf("expired issued-code transaction = %#v", expiredModel)
	}
	if _, err = persistence.DesktopAuthorization().Exchange(ctx, desktopAuthorizationExchangeForSQLTest(code, state, verifier,
		saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "expired-exchange"))); !store.IsNotFound(err) {
		t.Fatalf("Exchange(expired) error = %v", err)
	}

	cancelled, handle, proof, cancelState, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.DesktopAuthorization().Create(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.DesktopAuthorization().Cancel(ctx, &store.DesktopAuthorizationCancellation{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(cancelState), CancelledAt: model.GetMillis(),
	}); err != nil {
		t.Fatal(err)
	}
	exchanged, _, _, _, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
	if _, err = persistence.DesktopAuthorization().Create(ctx, exchanged); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions
		SET state='exchanged', handle_hash=NULL, browser_proof_hash=NULL, state_hash=NULL, callback_url=NULL,
		    code_challenge=NULL, user_id=?, authentication_method='password', authentication_strength='single_factor',
		    authenticated_at=created_at, exchanged_at=updated_at WHERE id=?`, user.ID.String(), exchanged.ID.String()); err != nil {
		t.Fatal(err)
	}

	for _, terminal := range []struct{ id string }{{issued.ID.String()}, {cancelled.ID.String()}, {exchanged.ID.String()}} {
		if _, err = persistence.GetMaster().Exec(ctx, `UPDATE browser_authentication_transactions SET
			created_at=aged.now-interval '24 hours 6 minutes', expires_at=aged.now-interval '24 hours 1 minute',
			cancelled_at=CASE WHEN state='cancelled' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			exchanged_at=CASE WHEN state='exchanged' THEN aged.now-interval '24 hours 2 minutes' ELSE NULL END,
			expired_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE NULL END,
			updated_at=CASE WHEN state='expired' THEN aged.now-interval '24 hours 1 minute' ELSE aged.now-interval '24 hours 2 minutes' END,
			authenticated_at=CASE WHEN user_id IS NOT NULL THEN aged.now-interval '24 hours 5 minutes' ELSE NULL END
			FROM (SELECT clock_timestamp() AS now) AS aged WHERE id=?`, terminal.id); err != nil {
			t.Fatal(err)
		}
	}
	maintained, err = persistence.DesktopAuthorization().Maintain(ctx, 500)
	if err != nil || maintained.Purged != 3 {
		t.Fatalf("Maintain(terminal retention) = %#v, %v", maintained, err)
	}
	var retained int
	if err = persistence.GetMaster().Get(ctx, &retained, `SELECT COUNT(*) FROM browser_authentication_transactions WHERE id IN (?,?,?)`,
		issued.ID.String(), cancelled.ID.String(), exchanged.ID.String()); err != nil {
		t.Fatal(err)
	}
	if retained != 0 {
		t.Fatalf("terminal rows past retention = %d", retained)
	}
}

func issueDesktopAuthorizationForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) (*model.BrowserAuthenticationTransaction, string, string, string) {
	t.Helper()
	transaction, handle, proof, state, verifier := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institutionID)
	if _, err := persistence.DesktopAuthorization().Create(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	code := model.NewCredentialToken()
	audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institutionID, userID, "issue")
	issued, err := persistence.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		UserID: userID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: transaction.CreatedAt.UnixMilli(), CodeHash: model.HashToken(code), CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued, code, state, verifier
}

func desktopAuthorizationTransactionForSQLTest(at time.Time, institutionID model.InstitutionID) (*model.BrowserAuthenticationTransaction, string, string, string, string) {
	handle, proof, state := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	verifier := model.NewCredentialToken()
	transaction := &model.BrowserAuthenticationTransaction{Purpose: model.BrowserAuthenticationPurposeDesktopAuthorization,
		InstitutionID: institutionID, Issuer: "https://proctor.example.edu", HandleHash: model.HashToken(handle),
		BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state), CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(),
		CodeChallenge: model.PKCES256Challenge(verifier), ExpectedAuthenticationMethod: "password", ClientType: model.SessionClientDesktop,
		ExpiresAt: at.Add(model.BrowserAuthenticationTransactionLifetime)}
	transaction.PrepareCreate(model.NewBrowserAuthenticationTransactionID(), at)
	return transaction, handle, proof, state, verifier
}

func desktopAuthorizationExchangeForSQLTest(code, state, verifier string, audit *model.AuditEvent) *store.DesktopAuthorizationExchange {
	return &store.DesktopAuthorizationExchange{CodeHash: model.HashToken(code), StateHash: model.HashToken(state),
		CodeChallenge: model.PKCES256Challenge(verifier), Issuer: "https://proctor.example.edu",
		AccessTokenHash: model.HashToken(model.NewCredentialToken()), RefreshTokenHash: model.HashToken(model.NewCredentialToken()),
		AccessLifetime: 5 * time.Minute, RefreshLifetime: time.Hour, IdleLifetime: 30 * time.Minute, AbsoluteLifetime: time.Hour,
		MaximumActive: 10, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}

func saveDesktopAuthorizationAuditForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: "authentication.desktop_authorization",
		Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "desktop-retention", Parameters: []byte(`{"operation":"` + operation + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func saveUserStateAuditForSQLTest(t *testing.T, ctx context.Context, persistence *SQLStore, institutionID model.InstitutionID, userID model.UserID) *model.AuditEvent {
	t.Helper()
	event, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: string(model.ActionUserManage),
		Resource: model.Resource{Type: model.ResourceUser, ID: userID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "desktop-disable-race"})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
