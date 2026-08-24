// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type BrowserAuthenticationSQLProbe struct {
	Backdate func(t *testing.T, id model.BrowserAuthenticationTransactionID, createdAt, expiresAt time.Time)
}

func TestBrowserAuthenticationStore(t *testing.T, ss store.Store, probe BrowserAuthenticationSQLProbe, concurrentPeers ...store.BrowserAuthenticationStore) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	now := model.NowUTC()
	t.Run("RejectsInvalidDesktopCreations", func(t *testing.T) {
		valid, _, _, _, _ := newDesktopAuthorizationTransaction(now, institution.ID)
		tests := []struct {
			name   string
			input  *store.DesktopAuthorizationCreation
			mutate func(*store.DesktopAuthorizationCreation)
		}{
			{name: "nil", input: nil},
			{name: "ID", mutate: func(value *store.DesktopAuthorizationCreation) { value.ID = "" }},
			{name: "institution", mutate: func(value *store.DesktopAuthorizationCreation) { value.InstitutionID = "" }},
			{name: "issuer", mutate: func(value *store.DesktopAuthorizationCreation) { value.Issuer = "http://proctor.example.edu" }},
			{name: "handle hash", mutate: func(value *store.DesktopAuthorizationCreation) { value.HandleHash = "raw-secret" }},
			{name: "browser proof hash", mutate: func(value *store.DesktopAuthorizationCreation) { value.BrowserProofHash = "raw-secret" }},
			{name: "equal handle and proof hashes", mutate: func(value *store.DesktopAuthorizationCreation) { value.BrowserProofHash = value.HandleHash }},
			{name: "state hash", mutate: func(value *store.DesktopAuthorizationCreation) { value.StateHash = "raw-secret" }},
			{name: "callback", mutate: func(value *store.DesktopAuthorizationCreation) {
				value.CallbackURL = "https://attacker.example/callback"
			}},
			{name: "PKCE challenge", mutate: func(value *store.DesktopAuthorizationCreation) { value.CodeChallenge = "short" }},
			{name: "authentication path", mutate: func(value *store.DesktopAuthorizationCreation) { value.ExpectedAuthenticationMethod = "unknown" }},
			{name: "device ID", mutate: func(value *store.DesktopAuthorizationCreation) {
				value.DeviceID = strings.Repeat("x", model.SessionDeviceIdMaxLength+1)
			}},
			{name: "device name", mutate: func(value *store.DesktopAuthorizationCreation) {
				value.DeviceName = strings.Repeat("x", model.SessionDeviceNameMaxRunes+1)
			}},
			{name: "zero lifetime", mutate: func(value *store.DesktopAuthorizationCreation) { value.Lifetime = 0 }},
			{name: "negative lifetime", mutate: func(value *store.DesktopAuthorizationCreation) { value.Lifetime = -time.Second }},
			{name: "excessive lifetime", mutate: func(value *store.DesktopAuthorizationCreation) {
				value.Lifetime = model.BrowserAuthenticationTransactionLifetime + time.Nanosecond
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				input := test.input
				if test.mutate != nil {
					candidate := *valid
					test.mutate(&candidate)
					input = &candidate
				}
				if _, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, input); !isInvalidInput(createErr) {
					t.Fatalf("CreateDesktopAuthorization() error = %v, want invalid input", createErr)
				}
			})
		}
	})
	shortLifetime := 90 * time.Second
	short, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	short.Lifetime = shortLifetime
	shortBefore := model.NowUTC()
	shortSaved, shortErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, short)
	shortAfter := model.NowUTC()
	requireNoError(t, shortErr)
	if shortSaved.ExpiresAt.Before(shortBefore.Add(shortLifetime-time.Second)) ||
		shortSaved.ExpiresAt.After(shortAfter.Add(shortLifetime+time.Second)) {
		t.Fatalf("Create(short lifetime) expiry = %s, call window = %s..%s", shortSaved.ExpiresAt, shortBefore, shortAfter)
	}
	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		nodeNow := model.NowUTC().Add(skew)
		skewed, _, _, _, _ := newDesktopAuthorizationTransaction(nodeNow, institution.ID)
		before := model.NowUTC()
		rebased, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, skewed)
		after := model.NowUTC()
		requireNoError(t, createErr)
		if rebased.ExpiresAt.Before(before.Add(model.BrowserAuthenticationTransactionLifetime-time.Second)) ||
			rebased.ExpiresAt.After(after.Add(model.BrowserAuthenticationTransactionLifetime+time.Second)) {
			t.Fatalf("Create(node skew %s) expiry = %s, call window = %s..%s", skew,
				rebased.ExpiresAt, before, after)
		}
	}
	transaction, handle, proof, state, verifier := newDesktopAuthorizationTransaction(now, institution.ID)
	saved, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	requireNoError(t, err)
	if saved.ID != transaction.ID || saved.ExpiresAt.IsZero() {
		t.Fatalf("Create() = %#v", saved)
	}

	expiredCreatedAt := now.Add(-10 * time.Minute)
	expiredPending, expiredHandle, expiredProof, expiredState, _ := newDesktopAuthorizationTransaction(expiredCreatedAt, institution.ID)
	expired, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, expiredPending)
	requireNoError(t, err)
	probe.Backdate(t, expired.ID, expiredCreatedAt, expiredCreatedAt.Add(expiredPending.Lifetime))
	secondCreatedAt := now.Add(-9 * time.Minute)
	secondExpired, _, _, _, _ := newDesktopAuthorizationTransaction(secondCreatedAt, institution.ID)
	if _, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, secondExpired); err != nil {
		t.Fatal(err)
	}
	probe.Backdate(t, secondExpired.ID, secondCreatedAt, secondCreatedAt.Add(secondExpired.Lifetime))
	maintained, err := ss.BrowserAuthentication().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 0 || !maintained.More {
		t.Fatalf("Maintain(first bounded page) = %#v", maintained)
	}
	maintained, err = ss.BrowserAuthentication().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 0 || maintained.More {
		t.Fatalf("Maintain(second bounded page) = %#v", maintained)
	}
	reusedProofs, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedProofs.HandleHash = model.HashToken(expiredHandle)
	reusedProofs.BrowserProofHash = model.HashToken(expiredProof)
	reusedProofs.StateHash = model.HashToken(expiredState)
	if _, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, reusedProofs); err != nil {
		t.Fatalf("Create(reused terminal proofs) error = %v", err)
	}

	purgeCreatedAt := now.Add(-25 * time.Hour)
	purgeCandidate, _, _, _, _ := newDesktopAuthorizationTransaction(purgeCreatedAt, institution.ID)
	purgedID := purgeCandidate.ID
	if _, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, purgeCandidate); err != nil {
		t.Fatalf("Create(past retention) error = %v", err)
	}
	probe.Backdate(t, purgeCandidate.ID, purgeCreatedAt, purgeCreatedAt.Add(purgeCandidate.Lifetime))
	maintained, err = ss.BrowserAuthentication().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Expired != 1 || maintained.Purged != 1 {
		t.Fatalf("Maintain(past retention) = %#v", maintained)
	}
	reusedID, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedID.ID = purgedID
	if _, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, reusedID); err != nil {
		t.Fatalf("Create(reused purged ID) error = %v", err)
	}
	for _, invalidLimit := range []int{0, 1001} {
		if _, err = ss.BrowserAuthentication().Maintain(ctx, invalidLimit); err == nil {
			t.Fatalf("Maintain(%d) accepted invalid limit", invalidLimit)
		}
	}

	code := model.NewCredentialToken()
	issueInput := &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		UserID: user.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now.UnixMilli(), CodeHash: model.HashToken(code), CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: model.NewAuditEventID().String(), AuditAt: model.GetMillis(),
	}
	if _, rollbackErr := ss.BrowserAuthentication().IssueCode(ctx, issueInput); rollbackErr == nil {
		t.Fatal("IssueCode() succeeded without its durable audit attempt")
	}
	issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "issue")
	issueInput.AuditEventID, issueInput.AuditAt = issueAudit.ID.String(), model.GetMillis()
	issueBefore := model.NowUTC()
	issued, err := ss.BrowserAuthentication().IssueCode(ctx, issueInput)
	issueAfter := model.NowUTC()
	requireNoError(t, err)
	if model.ValidateDesktopAuthorizationCallback(issued.CallbackURL) != nil ||
		issued.CodeExpiresAt.Before(issueBefore.Add(45*time.Second-time.Second)) ||
		issued.CodeExpiresAt.After(issueAfter.Add(45*time.Second+time.Second)) {
		t.Fatalf("IssueCode() = %#v", issued)
	}
	if _, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		UserID: user.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now.UnixMilli(), CodeHash: model.HashToken(model.NewCredentialToken()), CodeLifetime: 30 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsNotFound(err) {
		t.Fatalf("IssueCode(replay) error = %v", err)
	}

	exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "exchange")
	exchange := desktopAuthorizationExchange(now, code, state, verifier, exchangeAudit)
	rollbackExchange := *exchange
	rollbackExchange.AuditEventID = model.NewAuditEventID().String()
	if _, rollbackErr := ss.BrowserAuthentication().Exchange(ctx, &rollbackExchange); rollbackErr == nil {
		t.Fatal("Exchange() succeeded without its durable audit attempt")
	}
	rollbackSessions, listErr := ss.Session().ListByUser(ctx, user.ID.String())
	requireNoError(t, listErr)
	if len(rollbackSessions) != 0 {
		t.Fatalf("Exchange() audit rollback retained sessions: %#v", rollbackSessions)
	}
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
		if _, mismatchErr := ss.BrowserAuthentication().Exchange(ctx, &candidate); !store.IsNotFound(mismatchErr) {
			t.Fatalf("Exchange(%s mix-up) error = %v", name, mismatchErr)
		}
	}
	peers := []store.BrowserAuthenticationStore{ss.BrowserAuthentication(), ss.BrowserAuthentication()}
	if len(concurrentPeers) > 0 && concurrentPeers[0] != nil {
		peers[1] = concurrentPeers[0]
	}
	results := make(chan *store.DesktopAuthorizationExchangeResult, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	exchangeBefore := model.NowUTC()
	for _, peer := range peers {
		wait.Add(1)
		go func(candidate store.BrowserAuthenticationStore) {
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
		if result.Session == nil || result.Session.ClientType != model.SessionClientDesktop ||
			result.Session.CreatedAt.Before(exchangeBefore.Add(-time.Second)) || result.Session.CreatedAt.After(exchangeAfter.Add(time.Second)) ||
			result.Session.IdleExpiresAt.Sub(result.Session.CreatedAt) != 30*time.Minute ||
			result.Session.ExpiresAt.Sub(result.Session.CreatedAt) != time.Hour ||
			result.AccessExpiresAt.Sub(result.Session.CreatedAt) != 5*time.Minute ||
			result.RefreshExpiresAt.Sub(result.Session.CreatedAt) != time.Hour {
			t.Fatalf("Exchange() = %#v", result)
		}
	}
	sessions, err := ss.Session().ListByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if len(sessions) != 1 {
		t.Fatalf("sessions after exchange = %d", len(sessions))
	}

	disabledUser := saveUser(t, ctx, ss)
	disabledTransaction, disabledHandle, disabledProof, disabledState, disabledVerifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, disabledTransaction)
	requireNoError(t, err)
	disabledCode := model.NewCredentialToken()
	disabledIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user")
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(disabledHandle), BrowserProofHash: model.HashToken(disabledProof), StateHash: model.HashToken(disabledState),
		UserID: disabledUser.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.GetMillis(), CodeHash: model.HashToken(disabledCode), CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: disabledIssueAudit.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	disableAudit := saveUserProfileAuditAttempt(t, ctx, ss, disabledUser.ID.String())
	disabled, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledUser.ID.String(), ExpectedRevision: disabledUser.Revision, Disabled: true,
		ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID.String(),
		AuditAt: model.GetMillis(), Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	}))
	requireNoError(t, err)
	if disabled == nil || disabled.User == nil || !disabled.User.DisabledAt.Valid {
		t.Fatalf("disabled user = %#v", disabled)
	}
	rejectedIssue, rejectedHandle, rejectedProof, rejectedState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, rejectedIssue)
	requireNoError(t, err)
	rejectedIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user-after-disable")
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		HandleHash: model.HashToken(rejectedHandle), BrowserProofHash: model.HashToken(rejectedProof), StateHash: model.HashToken(rejectedState),
		UserID: disabledUser.ID, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.GetMillis(), CodeHash: model.HashToken(model.NewCredentialToken()),
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
	_, err = ss.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchange(model.NowUTC(), disabledCode, disabledState, disabledVerifier, disabledExchangeAudit))
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
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, cancelled)
	requireNoError(t, err)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{HandleHash: model.HashToken(cancelHandle), BrowserProofHash: model.HashToken(cancelProof), StateHash: model.HashToken(cancelState)})
	requireNoError(t, err)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{HandleHash: model.HashToken(cancelHandle), BrowserProofHash: model.HashToken(cancelProof), StateHash: model.HashToken(cancelState)})
	if !store.IsNotFound(err) {
		t.Fatalf("Cancel(replay) error = %v", err)
	}
}

func isInvalidInput(err error) bool {
	var target *store.ErrInvalidInput
	return errors.As(err, &target)
}

func newDesktopAuthorizationTransaction(_ time.Time, institutionID model.InstitutionID) (*store.DesktopAuthorizationCreation, string, string, string, string) {
	handle, proof, state := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	t := &store.DesktopAuthorizationCreation{ID: model.NewBrowserAuthenticationTransactionID(),
		InstitutionID: institutionID, Issuer: "https://proctor.example.edu", HandleHash: model.HashToken(handle),
		BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), CodeChallenge: model.PKCES256Challenge(verifier),
		ExpectedAuthenticationMethod: "password", DeviceID: "desktop-1", DeviceName: "Test Desktop",
		Lifetime: model.BrowserAuthenticationTransactionLifetime}
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
