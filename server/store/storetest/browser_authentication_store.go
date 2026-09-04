// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/elliptic"
	"encoding/base64"
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
	binding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), handle, proof, state, user.ID)

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
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(code), ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: model.NewAuditEventID().String(), AuditAt: model.GetMillis(),
	}
	if _, rollbackErr := ss.BrowserAuthentication().IssueCode(ctx, issueInput); rollbackErr == nil {
		t.Fatal("IssueCode() succeeded without its durable audit attempt")
	}
	switchedUser := saveUser(t, ctx, ss)
	switchAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, switchedUser.ID, "issue-account-switch")
	switchInput := *issueInput
	switchInput.ExpectedUserID = switchedUser.ID
	switchInput.AuditEventID = switchAudit.ID.String()
	if _, switchErr := ss.BrowserAuthentication().IssueCode(ctx, &switchInput); !store.IsNotFound(switchErr) {
		t.Fatalf("IssueCode(account switch) error = %v, want not found", switchErr)
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
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(model.NewCredentialToken()), ExpectedUserID: user.ID, CodeLifetime: 30 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsNotFound(err) {
		t.Fatalf("IssueCode(replay) error = %v", err)
	}

	exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "exchange")
	exchange := desktopAuthorizationExchange(now, code, state, verifier, exchangeAudit)
	stalePolicyExchange := *exchange
	stalePolicyExchange.DesktopCompatibilityPolicyRevision++
	if _, staleErr := ss.BrowserAuthentication().Exchange(ctx, &stalePolicyExchange); staleErr == nil {
		t.Fatal("Exchange() accepted a stale Desktop Compatibility Policy revision")
	} else {
		var conflict *store.ErrConflict
		if !errors.As(staleErr, &conflict) || conflict.Constraint != "desktop_compatibility_policy_revision" {
			t.Fatalf("Exchange(stale Desktop Compatibility Policy) error = %v", staleErr)
		}
	}
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
	disabledBinding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), disabledHandle, disabledProof, disabledState, disabledUser.ID)
	disabledCode := model.NewCredentialToken()
	disabledIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user")
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(disabledBinding), StateHash: model.HashToken(disabledState),
		CodeHash: model.HashToken(disabledCode), ExpectedUserID: disabledUser.ID, CodeLifetime: 45 * time.Second,
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
	rejectedBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), rejectedHandle, rejectedProof, rejectedState)
	_, err = ss.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(rejectedBinding), UserID: disabledUser.ID,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.GetMillis(),
		Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	if !store.IsNotFound(err) {
		t.Fatalf("AuthenticateDesktopAuthorization(disabled user) error = %v, want not found", err)
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
	cancelBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), cancelHandle, cancelProof, cancelState)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState)})
	requireNoError(t, err)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState)})
	if !store.IsNotFound(err) {
		t.Fatalf("Cancel(replay) error = %v", err)
	}

	t.Run("ActiveAttemptFencesAuthenticationAndExchange", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		issuedTransaction, issuedHandle, issuedProof, issuedState, issuedVerifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
		_, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, issuedTransaction)
		requireNoError(t, createErr)
		issuedBinding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss.BrowserAuthentication(),
			issuedHandle, issuedProof, issuedState, fixture.candidate.ID)
		issuedCode := model.NewCredentialToken()
		issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixture.candidate.ID, "issue-before-attempt")
		_, issueErr := ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
			BindingHash: model.HashToken(issuedBinding), StateHash: model.HashToken(issuedState),
			CodeHash: model.HashToken(issuedCode), ExpectedUserID: fixture.candidate.ID, CodeLifetime: 45 * time.Second,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: issueAudit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, issueErr)

		pending, pendingHandle, pendingProof, pendingState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
		_, createErr = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, pending)
		requireNoError(t, createErr)
		pendingBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), pendingHandle, pendingProof, pendingState)

		continuity := model.HashToken(model.NewCredentialToken())
		connect := &store.ExamAttemptConnect{
			SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
			AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
			ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
			ContinuityCredentialHash: continuity,
			AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
		}
		prepareExamAttemptConnect(t, ctx, ss, connect)
		_, connectErr := ss.ExamAttempt().Connect(ctx, connect,
			examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "desktop-lock", "desktop-lock"))
		requireNoError(t, connectErr)

		authenticated, authenticationErr := ss.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
			&store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(pendingBinding), UserID: fixture.candidate.ID,
				AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: model.GetMillis(),
				Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			})
		requireNoError(t, authenticationErr)
		if authenticated == nil || !authenticated.Denied {
			t.Fatalf("AuthenticateDesktopAuthorization(active Attempt) = %#v", authenticated)
		}
		if _, contextErr := ss.BrowserAuthentication().GetDesktopAuthorizationContext(ctx, model.HashToken(pendingBinding)); !store.IsNotFound(contextErr) {
			t.Fatalf("GetDesktopAuthorizationContext(denied) error = %v", contextErr)
		}

		exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixture.candidate.ID, "exchange-during-attempt")
		denied, exchangeErr := ss.BrowserAuthentication().Exchange(ctx,
			desktopAuthorizationExchange(model.NowUTC(), issuedCode, issuedState, issuedVerifier, exchangeAudit))
		requireNoError(t, exchangeErr)
		if denied == nil || !denied.Denied || denied.Session != nil {
			t.Fatalf("Exchange(active Attempt) = %#v", denied)
		}
		completedAudit, auditErr := ss.Audit().Get(ctx, exchangeAudit.ID.String())
		requireNoError(t, auditErr)
		if completedAudit.Status != model.AuditStatusFail ||
			completedAudit.ErrorCode != "authentication.desktop_authorization.account_session_locked" {
			t.Fatalf("Exchange(active Attempt) audit = %#v", completedAudit)
		}
	})
}

func isInvalidInput(err error) bool {
	var target *store.ErrInvalidInput
	return errors.As(err, &target)
}

func bindDesktopAuthorization(t *testing.T, ctx context.Context, persistence store.BrowserAuthenticationStore, handle, proof, state string) string {
	t.Helper()
	binding := model.NewCredentialToken()
	bound, err := persistence.BindDesktopAuthorization(ctx, &store.DesktopAuthorizationBinding{
		HandleHash: model.HashToken(handle), BrowserProofHash: model.HashToken(proof),
		StateHash: model.HashToken(state), BindingHash: model.HashToken(binding),
	})
	requireNoError(t, err)
	if bound == nil || bound.ExpiresAt.IsZero() {
		t.Fatalf("BindDesktopAuthorization() = %#v", bound)
	}
	return binding
}

func bindAndAuthenticateDesktopAuthorization(t *testing.T, ctx context.Context, persistence store.BrowserAuthenticationStore,
	handle, proof, state string, userID model.UserID,
) string {
	t.Helper()
	binding := bindDesktopAuthorization(t, ctx, persistence, handle, proof, state)
	result, err := persistence.AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(binding), UserID: userID, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	requireNoError(t, err)
	if result == nil || result.Denied {
		t.Fatalf("AuthenticateDesktopAuthorization() = %#v", result)
	}
	return binding
}

func newDesktopAuthorizationTransaction(_ time.Time, institutionID model.InstitutionID) (*store.DesktopAuthorizationCreation, string, string, string, string) {
	handle, proof, state := model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken()
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	publicJWK, thumbprint := storetestDesktopAuthorizationKey()
	t := &store.DesktopAuthorizationCreation{ID: model.NewBrowserAuthenticationTransactionID(),
		InstitutionID: institutionID, Issuer: "https://proctor.example.edu", HandleHash: model.HashToken(handle),
		BrowserProofHash: model.HashToken(proof), StateHash: model.HashToken(state),
		CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), CodeChallenge: model.PKCES256Challenge(verifier),
		DeviceID: "desktop-1", DeviceName: "Test Desktop", ProposedPublicJWK: publicJWK, ProposedKeyThumbprint: thumbprint,
		DesktopRelease: "1.0.0", DesktopBuildID: "storetest", DesktopPlatform: model.DesktopPlatformDarwin,
		DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		Lifetime: model.BrowserAuthenticationTransactionLifetime}
	return t, handle, proof, state, verifier
}

func desktopAuthorizationExchange(at time.Time, code, state, verifier string, audit *model.AuditEvent) *store.DesktopAuthorizationExchange {
	publicJWK, thumbprint := storetestDesktopAuthorizationKey()
	return &store.DesktopAuthorizationExchange{CodeHash: model.HashToken(code), StateHash: model.HashToken(state),
		CodeChallenge: model.PKCES256Challenge(verifier), Issuer: "https://proctor.example.edu",
		ExpectedPublicJWK: publicJWK, ExpectedKeyThumbprint: thumbprint, DesktopRelease: "1.0.0", DesktopBuildID: "storetest",
		DesktopPlatform: model.DesktopPlatformDarwin, DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		DesktopCompatibilityPolicyRevision: 1,
		AccessTokenHash:                    model.HashToken(model.NewCredentialToken()), RefreshTokenHash: model.HashToken(model.NewCredentialToken()),
		AccessLifetime: 5 * time.Minute, RefreshLifetime: time.Hour, IdleLifetime: 30 * time.Minute, AbsoluteLifetime: time.Hour,
		MaximumActive: 10,
		Capabilities:  store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}
}

func storetestDesktopAuthorizationKey() (model.DesktopPublicJWK, string) {
	curve := elliptic.P256().Params()
	publicJWK := model.DesktopPublicJWK{Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(curve.Gx.FillBytes(make([]byte, 32))),
		Y: base64.RawURLEncoding.EncodeToString(curve.Gy.FillBytes(make([]byte, 32)))}
	thumbprint, err := publicJWK.Thumbprint()
	if err != nil {
		panic(err)
	}
	return publicJWK, thumbprint
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
