// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type DesktopAuthorizationSQLProbe struct {
	Backdate func(t *testing.T, id model.BrowserAuthenticationTransactionID, createdAt, expiresAt time.Time)
}

func TestDesktopAuthorizationStore(t *testing.T, ss store.Store, probe DesktopAuthorizationSQLProbe, concurrentPeers ...store.DesktopAuthorizationStore) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	now := model.NowUTC()
	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		nodeNow := model.NowUTC().Add(skew)
		skewed, _, _, _, _ := newDesktopAuthorizationTransaction(nodeNow, institution.ID)
		before := model.NowUTC()
		rebased, createErr := ss.DesktopAuthorization().Create(ctx, skewed)
		after := model.NowUTC()
		requireNoError(t, createErr)
		if rebased.CreatedAt.Before(before.Add(-time.Second)) || rebased.CreatedAt.After(after.Add(time.Second)) ||
			rebased.ExpiresAt.Sub(rebased.CreatedAt) != model.BrowserAuthenticationTransactionLifetime {
			t.Fatalf("Create(node skew %s) timestamps = %s..%s, call window = %s..%s", skew,
				rebased.CreatedAt, rebased.ExpiresAt, before, after)
		}
	}
	transaction, handle, proof, state, verifier := newDesktopAuthorizationTransaction(now, institution.ID)
	saved, err := ss.DesktopAuthorization().Create(ctx, transaction)
	requireNoError(t, err)
	if saved.ID != transaction.ID || saved.HandleHash != model.HashToken(handle) {
		t.Fatalf("Create() = %#v", saved)
	}

	expiredPending, expiredHandle, expiredProof, expiredState, _ := newDesktopAuthorizationTransaction(now.Add(-10*time.Minute), institution.ID)
	expired, err := ss.DesktopAuthorization().Create(ctx, expiredPending)
	requireNoError(t, err)
	probe.Backdate(t, expired.ID, expiredPending.CreatedAt, expiredPending.ExpiresAt)
	if expired.State != model.BrowserAuthenticationStatePending {
		t.Fatalf("Create(expired pending) performed opportunistic maintenance: %#v", expired)
	}
	secondExpired, _, _, _, _ := newDesktopAuthorizationTransaction(now.Add(-9*time.Minute), institution.ID)
	if _, err = ss.DesktopAuthorization().Create(ctx, secondExpired); err != nil {
		t.Fatal(err)
	}
	probe.Backdate(t, secondExpired.ID, secondExpired.CreatedAt, secondExpired.ExpiresAt)
	maintained, err := ss.DesktopAuthorization().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 0 || !maintained.More {
		t.Fatalf("Maintain(first bounded page) = %#v", maintained)
	}
	maintained, err = ss.DesktopAuthorization().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 0 || maintained.More {
		t.Fatalf("Maintain(second bounded page) = %#v", maintained)
	}
	reusedProofs, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedProofs.HandleHash = model.HashToken(expiredHandle)
	reusedProofs.BrowserProofHash = model.HashToken(expiredProof)
	reusedProofs.StateHash = model.HashToken(expiredState)
	if _, err = ss.DesktopAuthorization().Create(ctx, reusedProofs); err != nil {
		t.Fatalf("Create(reused terminal proofs) error = %v", err)
	}

	purgeCandidate, _, _, _, _ := newDesktopAuthorizationTransaction(now.Add(-25*time.Hour), institution.ID)
	purgedID := purgeCandidate.ID
	if _, err = ss.DesktopAuthorization().Create(ctx, purgeCandidate); err != nil {
		t.Fatalf("Create(past retention) error = %v", err)
	}
	probe.Backdate(t, purgeCandidate.ID, purgeCandidate.CreatedAt, purgeCandidate.ExpiresAt)
	maintained, err = ss.DesktopAuthorization().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 1 {
		t.Fatalf("Maintain(past retention) = %#v", maintained)
	}
	reusedID, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedID.ID = purgedID
	if _, err = ss.DesktopAuthorization().Create(ctx, reusedID); err != nil {
		t.Fatalf("Create(reused purged ID) error = %v", err)
	}
	for _, invalidLimit := range []int{0, 1001} {
		if _, err = ss.DesktopAuthorization().Maintain(ctx, invalidLimit); err == nil {
			t.Fatalf("Maintain(%d) accepted invalid limit", invalidLimit)
		}
	}

	issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "issue")
	code := model.NewCredentialToken()
	issueBefore := model.NowUTC()
	issued, err := ss.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		UserID: user.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now.UnixMilli(), CodeHash: model.HashToken(code), CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
	})
	issueAfter := model.NowUTC()
	requireNoError(t, err)
	if issued.State != model.BrowserAuthenticationStateCodeIssued || issued.HandleHash != "" ||
		issued.UpdatedAt.Before(issueBefore.Add(-time.Second)) || issued.UpdatedAt.After(issueAfter.Add(time.Second)) ||
		issued.CodeExpiresAt.Time.Sub(issued.UpdatedAt) != 45*time.Second {
		t.Fatalf("IssueCode() = %#v", issued)
	}
	if _, err = ss.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		UserID: user.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now.UnixMilli(), CodeHash: model.HashToken(model.NewCredentialToken()), CodeLifetime: 30 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsNotFound(err) {
		t.Fatalf("IssueCode(replay) error = %v", err)
	}

	exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "exchange")
	exchange := desktopAuthorizationExchange(now, code, state, verifier, exchangeAudit)
	for name, mutate := range map[string]func(*store.DesktopAuthorizationExchange){
		"state": func(candidate *store.DesktopAuthorizationExchange) {
			candidate.StateHash = model.HashToken(model.NewCredentialToken())
		},
		"PKCE": func(candidate *store.DesktopAuthorizationExchange) {
			candidate.CodeChallenge = model.PKCES256Challenge(model.NewCredentialToken())
		},
		"issuer": func(candidate *store.DesktopAuthorizationExchange) { candidate.Issuer = "https://other.example.edu" },
	} {
		candidate := *exchange
		mutate(&candidate)
		if _, mismatchErr := ss.DesktopAuthorization().Exchange(ctx, &candidate); !store.IsNotFound(mismatchErr) {
			t.Fatalf("Exchange(%s mix-up) error = %v", name, mismatchErr)
		}
	}
	peers := []store.DesktopAuthorizationStore{ss.DesktopAuthorization(), ss.DesktopAuthorization()}
	if len(concurrentPeers) > 0 && concurrentPeers[0] != nil {
		peers[1] = concurrentPeers[0]
	}
	results := make(chan *store.DesktopAuthorizationExchangeResult, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	exchangeBefore := model.NowUTC()
	for _, peer := range peers {
		wait.Add(1)
		go func(candidate store.DesktopAuthorizationStore) {
			defer wait.Done()
			value, exchangeErr := candidate.Exchange(ctx, exchange)
			results <- value
			errs <- exchangeErr
		}(peer)
	}
	wait.Wait()
	exchangeAfter := model.NowUTC()
	close(results)
	close(errs)
	successes, rejected := 0, 0
	for err = range errs {
		if err == nil {
			successes++
		} else if store.IsNotFound(err) {
			rejected++
		} else {
			t.Fatalf("Exchange() error = %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("concurrent exchange success=%d rejected=%d", successes, rejected)
	}
	for result := range results {
		if result == nil {
			continue
		}
		if result.Session == nil || result.Session.ClientType != model.SessionClientDesktop || len(result.Credentials) != 2 ||
			result.Session.CreatedAt.Before(exchangeBefore.Add(-time.Second)) || result.Session.CreatedAt.After(exchangeAfter.Add(time.Second)) ||
			result.Session.IdleExpiresAt.Sub(result.Session.CreatedAt) != 30*time.Minute ||
			result.Session.ExpiresAt.Sub(result.Session.CreatedAt) != time.Hour {
			t.Fatalf("Exchange() = %#v", result)
		}
		for _, credential := range result.Credentials {
			want := time.Hour
			if credential.Kind == model.SessionCredentialAccess {
				want = 5 * time.Minute
			}
			if credential.ExpiresAt.Sub(credential.CreatedAt) != want {
				t.Fatalf("Exchange() credential = %#v, lifetime want %s", credential, want)
			}
		}
	}
	sessions, err := ss.Session().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(sessions) != 1 {
		t.Fatalf("sessions after exchange = %d", len(sessions))
	}

	disabledUser := saveUser(t, ctx, ss)
	disabledTransaction, disabledHandle, disabledProof, disabledState, disabledVerifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.DesktopAuthorization().Create(ctx, disabledTransaction)
	requireNoError(t, err)
	disabledCode := model.NewCredentialToken()
	disabledIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user")
	_, err = ss.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(disabledHandle), BrowserProofHash: model.HashToken(disabledProof), StateHash: model.HashToken(disabledState),
		UserID: disabledUser.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: disabledTransaction.CreatedAt.UnixMilli(), CodeHash: model.HashToken(disabledCode), CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: disabledIssueAudit.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	disableAudit := saveUserProfileAuditAttempt(t, ctx, ss, disabledUser.ID.String())
	disabled, err := ss.User().SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
		ID: disabledUser.ID.String(), ExpectedRevision: disabledUser.Revision, Disabled: true,
		ChangedAt: model.GetMillis(), RevocationReason: "test disabled user", AuditEventID: disableAudit.ID.String(),
		AuditAt: model.GetMillis(), Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	requireNoError(t, err)
	if disabled == nil || disabled.User == nil || !disabled.User.DisabledAt.Valid {
		t.Fatalf("disabled user = %#v", disabled)
	}
	rejectedIssue, rejectedHandle, rejectedProof, rejectedState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.DesktopAuthorization().Create(ctx, rejectedIssue)
	requireNoError(t, err)
	rejectedIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user-after-disable")
	_, err = ss.DesktopAuthorization().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(rejectedHandle), BrowserProofHash: model.HashToken(rejectedProof), StateHash: model.HashToken(rejectedState),
		UserID: disabledUser.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: rejectedIssue.CreatedAt.UnixMilli(), CodeHash: model.HashToken(model.NewCredentialToken()),
		CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: rejectedIssueAudit.ID.String(), AuditAt: model.GetMillis(),
	})
	if !store.IsNotFound(err) {
		t.Fatalf("IssueCode(disabled user) error = %v, want not found", err)
	}
	rejectedIssueAuditAfter, getErr := ss.Audit().Get(ctx, rejectedIssueAudit.ID.String())
	requireNoError(t, getErr)
	if rejectedIssueAuditAfter.Status != model.AuditStatusAttempt {
		t.Fatalf("disabled-user IssueCode completed audit: %#v", rejectedIssueAuditAfter)
	}
	disabledExchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "exchange-disabled-user")
	_, err = ss.DesktopAuthorization().Exchange(ctx, desktopAuthorizationExchange(model.NowUTC(), disabledCode, disabledState, disabledVerifier, disabledExchangeAudit))
	if !store.IsNotFound(err) {
		t.Fatalf("Exchange(disabled user) error = %v, want not found", err)
	}
	disabledSessions, listErr := ss.Session().ListByUser(ctx, disabledUser.ID.String())
	requireNoError(t, listErr)
	if len(disabledSessions) != 0 {
		t.Fatalf("disabled user sessions = %#v", disabledSessions)
	}
	disabledExchangeAuditAfter, getErr := ss.Audit().Get(ctx, disabledExchangeAudit.ID.String())
	requireNoError(t, getErr)
	if disabledExchangeAuditAfter.Status != model.AuditStatusAttempt {
		t.Fatalf("disabled exchange completed audit: %#v", disabledExchangeAuditAfter)
	}

	cancelled, cancelHandle, cancelProof, cancelState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.DesktopAuthorization().Create(ctx, cancelled)
	requireNoError(t, err)
	cancelled, err = ss.DesktopAuthorization().Cancel(ctx, &store.DesktopAuthorizationCancellation{HandleHash: model.HashToken(cancelHandle), BrowserProofHash: model.HashToken(cancelProof), StateHash: model.HashToken(cancelState), CancelledAt: model.GetMillis()})
	requireNoError(t, err)
	if cancelled.State != model.BrowserAuthenticationStateCancelled || cancelled.StateHash != "" {
		t.Fatalf("Cancel() = %#v", cancelled)
	}
	_, err = ss.DesktopAuthorization().Cancel(ctx, &store.DesktopAuthorizationCancellation{HandleHash: model.HashToken(cancelHandle), BrowserProofHash: model.HashToken(cancelProof), StateHash: model.HashToken(cancelState), CancelledAt: model.GetMillis()})
	if !store.IsNotFound(err) {
		t.Fatalf("Cancel(replay) error = %v", err)
	}
}

func newDesktopAuthorizationTransaction(at time.Time, institutionID model.InstitutionID) (*model.BrowserAuthenticationTransaction, string, string, string, string) {
	handle, proof, state := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	t := &model.BrowserAuthenticationTransaction{Purpose: model.BrowserAuthenticationPurposeDesktopAuthorization,
		InstitutionID: institutionID, Issuer: "https://proctor.example.edu", HandleHash: model.HashToken(handle),
		BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), CodeChallenge: model.PKCES256Challenge(verifier),
		ExpectedAuthenticationMethod: "password", ClientType: model.SessionClientDesktop,
		DeviceID: "desktop-1", DeviceName: "Test Desktop", ExpiresAt: at.Add(5 * time.Minute)}
	t.PrepareCreate(model.NewBrowserAuthenticationTransactionID(), at)
	return t, handle, proof, state, verifier
}

func desktopAuthorizationExchange(at time.Time, code, state, verifier string, audit *model.AuditEvent) *store.DesktopAuthorizationExchange {
	return &store.DesktopAuthorizationExchange{CodeHash: model.HashToken(code), StateHash: model.HashToken(state),
		CodeChallenge: model.PKCES256Challenge(verifier), Issuer: "https://proctor.example.edu",
		AccessTokenHash: model.HashToken(model.NewCredentialToken()), RefreshTokenHash: model.HashToken(model.NewCredentialToken()),
		AccessLifetime: 5 * time.Minute, RefreshLifetime: time.Hour, IdleLifetime: 30 * time.Minute, AbsoluteLifetime: time.Hour,
		MaximumActive: 10,
		Capabilities:  store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}

func saveDesktopAuthorizationAudit(t *testing.T, ctx context.Context, ss store.Store, institutionID model.InstitutionID, userID model.UserID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: userID, Action: "authentication.desktop_authorization",
		Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "storetest", Parameters: []byte(`{"operation":"` + operation + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
