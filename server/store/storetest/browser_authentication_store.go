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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
				value.DeviceID = strings.Repeat("x", model.SessionDeviceIDMaxLength+1)
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
				_, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, input)
				require.True(t, isInvalidInput(createErr), "CreateDesktopAuthorization() error")
			})
		}
	})
	t.Run("FreshPasswordAuthenticationUsesStoreClock", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			skew     time.Duration
			strength model.AuthenticationStrength
		}{
			{name: "past single factor", skew: -2 * time.Hour, strength: model.AuthenticationSingleFactor},
			{name: "future single factor", skew: 2 * time.Hour, strength: model.AuthenticationSingleFactor},
			{name: "past multi factor", skew: -2 * time.Hour, strength: model.AuthenticationMultiFactor},
			{name: "future multi factor", skew: 2 * time.Hour, strength: model.AuthenticationMultiFactor},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixtureUser := saveUser(t, ctx, ss)
				transaction, handle, proof, state, verifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
				created, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
				require.NoError(t, err)
				binding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), handle, proof, state)
				input := &store.DesktopAuthorizationAuthentication{
					BindingHash: model.HashToken(binding), UserID: fixtureUser.ID, AuthenticationMethod: "password",
					PasswordProof: testPasswordProof(t, ctx, ss, fixtureUser.ID), AuthenticationStrength: test.strength,
					AuthenticatedAt: model.GetMillis() + test.skew.Milliseconds(),
					Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
				}
				if test.strength == model.AuthenticationMultiFactor {
					input.MFACompletedAt = input.AuthenticatedAt
				}
				authenticated, err := ss.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, input)
				require.NoError(t, err)
				require.NotNil(t, authenticated)
				require.False(t, authenticated.Denied)
				code := model.NewCredentialToken()
				issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixtureUser.ID, "clock-issue")
				issued, err := ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
					BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code),
					ExpectedUserID: fixtureUser.ID, CodeLifetime: 45 * time.Second, Capabilities: input.Capabilities,
					AuditEventID: issueAudit.ID, AuditAt: model.GetMillis(),
				})
				require.NoError(t, err)
				require.False(t, issued.CodeExpiresAt.After(created.ExpiresAt), "code outlives its browser transaction")
				exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixtureUser.ID, "clock-exchange")
				result, err := ss.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchange(model.NowUTC(), code, state, verifier, exchangeAudit))
				require.NoError(t, err)
				require.NotNil(t, result, "Desktop Session did not retain fresh Store-clock authentication")
				require.NotNil(t, result.Session, "Desktop Session did not retain fresh Store-clock authentication")
				require.Equal(t, test.strength, result.Session.AuthenticationStrength, "Desktop Session did not retain fresh Store-clock authentication")
				require.False(t, result.Session.AuthenticatedAt.Before(created.ExpiresAt.Add(-transaction.Lifetime)), "Desktop Session did not retain fresh Store-clock authentication")
				require.False(t, result.Session.AuthenticatedAt.After(result.Session.CreatedAt), "Desktop Session did not retain fresh Store-clock authentication")
				if test.strength == model.AuthenticationMultiFactor {
					require.True(t, result.Session.MFACompletedAt.Valid, "fresh MFA did not retain the authentication transition instant")
					require.True(t, result.Session.MFACompletedAt.Time.Equal(result.Session.AuthenticatedAt), "fresh MFA did not retain the authentication transition instant")
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
	require.NoError(t, shortErr)
	require.False(t, shortSaved.ExpiresAt.Before(shortBefore.Add(shortLifetime-time.Second)), "Create(short lifetime) expiry = %s, call window = %s..%s", shortSaved.ExpiresAt, shortBefore, shortAfter)
	require.False(t, shortSaved.ExpiresAt.After(shortAfter.Add(shortLifetime+time.Second)), "Create(short lifetime) expiry = %s, call window = %s..%s", shortSaved.ExpiresAt, shortBefore, shortAfter)
	for _, skew := range []time.Duration{-2 * time.Hour, 2 * time.Hour} {
		nodeNow := model.NowUTC().Add(skew)
		skewed, _, _, _, _ := newDesktopAuthorizationTransaction(nodeNow, institution.ID)
		before := model.NowUTC()
		rebased, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, skewed)
		after := model.NowUTC()
		require.NoError(t, createErr)
		require.False(t, rebased.ExpiresAt.Before(before.Add(model.BrowserAuthenticationTransactionLifetime-time.Second)), "Create(node skew %s) expiry = %s, call window = %s..%s", skew, rebased.ExpiresAt, before, after)
		require.False(t, rebased.ExpiresAt.After(after.Add(model.BrowserAuthenticationTransactionLifetime+time.Second)), "Create(node skew %s) expiry = %s, call window = %s..%s", skew, rebased.ExpiresAt, before, after)
	}
	transaction, handle, proof, state, verifier := newDesktopAuthorizationTransaction(now, institution.ID)
	saved, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	require.Equal(t, transaction.ID, saved.ID)
	require.False(t, saved.ExpiresAt.IsZero())
	binding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss, handle, proof, state, user.ID)

	expiredCreatedAt := now.Add(-10 * time.Minute)
	expiredPending, expiredHandle, expiredProof, expiredState, _ := newDesktopAuthorizationTransaction(expiredCreatedAt, institution.ID)
	expired, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, expiredPending)
	require.NoError(t, err)
	probe.Backdate(t, expired.ID, expiredCreatedAt, expiredCreatedAt.Add(expiredPending.Lifetime))
	secondCreatedAt := now.Add(-9 * time.Minute)
	secondExpired, _, _, _, _ := newDesktopAuthorizationTransaction(secondCreatedAt, institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, secondExpired)
	require.NoError(t, err)
	probe.Backdate(t, secondExpired.ID, secondCreatedAt, secondCreatedAt.Add(secondExpired.Lifetime))
	maintained, err := ss.BrowserAuthentication().Maintain(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, maintained.Expired)
	require.Equal(t, 0, maintained.Purged)
	require.True(t, maintained.More)
	maintained, err = ss.BrowserAuthentication().Maintain(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, maintained.Expired)
	require.Equal(t, 0, maintained.Purged)
	require.False(t, maintained.More)
	reusedProofs, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedProofs.HandleHash = model.HashToken(expiredHandle)
	reusedProofs.BrowserProofHash = model.HashToken(expiredProof)
	reusedProofs.StateHash = model.HashToken(expiredState)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, reusedProofs)
	require.NoError(t, err)

	purgeCreatedAt := now.Add(-25 * time.Hour)
	purgeCandidate, _, _, _, _ := newDesktopAuthorizationTransaction(purgeCreatedAt, institution.ID)
	purgedID := purgeCandidate.ID
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, purgeCandidate)
	require.NoError(t, err)
	probe.Backdate(t, purgeCandidate.ID, purgeCreatedAt, purgeCreatedAt.Add(purgeCandidate.Lifetime))
	maintained, err = ss.BrowserAuthentication().Maintain(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, maintained.Expired)
	require.Equal(t, 1, maintained.Purged)
	reusedID, _, _, _, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	reusedID.ID = purgedID
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, reusedID)
	require.NoError(t, err)
	for _, invalidLimit := range []int{0, 1001} {
		_, err = ss.BrowserAuthentication().Maintain(ctx, invalidLimit)
		require.Error(t, err, "Maintain(%d) accepted invalid limit", invalidLimit)
	}

	code := model.NewCredentialToken()
	issueInput := &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(code), ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	}
	_, rollbackErr := ss.BrowserAuthentication().IssueCode(ctx, issueInput)
	require.Error(t, rollbackErr, "IssueCode() succeeded without its durable audit attempt")
	switchedUser := saveUser(t, ctx, ss)
	switchAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, switchedUser.ID, "issue-account-switch")
	switchInput := *issueInput
	switchInput.ExpectedUserID = switchedUser.ID
	switchInput.AuditEventID = switchAudit.ID
	_, switchErr := ss.BrowserAuthentication().IssueCode(ctx, &switchInput)
	require.True(t, store.IsNotFound(switchErr), "IssueCode(account switch) error")
	issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "issue")
	issueInput.AuditEventID, issueInput.AuditAt = issueAudit.ID, model.GetMillis()
	issueBefore := model.NowUTC()
	issued, err := ss.BrowserAuthentication().IssueCode(ctx, issueInput)
	issueAfter := model.NowUTC()
	require.NoError(t, err)
	require.NoError(t, model.ValidateDesktopAuthorizationCallback(issued.CallbackURL))
	require.False(t, issued.CodeExpiresAt.Before(issueBefore.Add(45*time.Second-time.Second)))
	require.False(t, issued.CodeExpiresAt.After(issueAfter.Add(45*time.Second+time.Second)))
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state),
		CodeHash: model.HashToken(model.NewCredentialToken()), ExpectedUserID: user.ID, CodeLifetime: 30 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: issueAudit.ID, AuditAt: model.GetMillis(),
	})
	require.True(t, store.IsNotFound(err), "IssueCode(replay) error")

	exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, user.ID, "exchange")
	exchange := desktopAuthorizationExchange(now, code, state, verifier, exchangeAudit)
	resolved, err := ss.BrowserAuthentication().ResolveDesktopAuthorizationExchange(ctx, &store.DesktopAuthorizationExchangeProof{
		CodeHash: exchange.CodeHash, StateHash: exchange.StateHash, CodeChallenge: exchange.CodeChallenge,
		Issuer: exchange.Issuer, ExpectedKeyThumbprint: exchange.ExpectedKeyThumbprint,
		DesktopRelease: exchange.DesktopRelease, DesktopBuildID: exchange.DesktopBuildID,
		DesktopPlatform: exchange.DesktopPlatform, DesktopArchitecture: exchange.DesktopArchitecture,
		DesktopRealtimeProtocol: exchange.DesktopRealtimeProtocol,
	})
	require.NoError(t, err)
	require.NotNil(t, resolved, "ResolveDesktopAuthorizationExchange()")
	require.True(t, resolved.ID.IsValid(), "ResolveDesktopAuthorizationExchange()")
	require.Equal(t, user.ID, resolved.UserID, "ResolveDesktopAuthorizationExchange()")
	stalePolicyExchange := *exchange
	stalePolicyExchange.DesktopCompatibilityPolicyRevision++
	if _, staleErr := ss.BrowserAuthentication().Exchange(ctx, &stalePolicyExchange); staleErr == nil {
		require.FailNow(t, "Exchange() accepted a stale Desktop Compatibility Policy revision")
	} else {
		var conflict *store.ErrConflict
		require.ErrorAs(t, staleErr, &conflict)
		require.Equal(t, "desktop_compatibility_policy_revision", conflict.Constraint)
	}
	rollbackExchange := *exchange
	rollbackExchange.AuditEventID = model.NewAuditEventID()
	_, rollbackErr = ss.BrowserAuthentication().Exchange(ctx, &rollbackExchange)
	require.Error(t, rollbackErr, "Exchange() succeeded without its durable audit attempt")
	rollbackSessions, listErr := ss.Session().ListByUser(ctx, user.ID)
	require.NoError(t, listErr)
	require.Empty(t, rollbackSessions)
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
		_, mismatchErr := ss.BrowserAuthentication().Exchange(ctx, &candidate)
		require.True(t, store.IsNotFound(mismatchErr), "Exchange(%s mix-up) error", name)
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
			require.FailNow(t, fmt.Sprintf("Exchange() error = %v", err))
		}
	}
	require.Equal(t, 1, successes, "concurrent exchange success")
	require.Equal(t, 1, rejected, "concurrent exchange success")
	for result := range results {
		if result == nil {
			continue
		}
		require.NotNil(t, result.Session)
		require.Equal(t, model.SessionClientDesktop, result.Session.ClientType)
		require.False(t, result.Session.CreatedAt.Before(exchangeBefore.Add(-time.Second)))
		require.False(t, result.Session.CreatedAt.After(exchangeAfter.Add(time.Second)))
		require.Equal(t, 30*time.Minute, result.Session.IdleExpiresAt.Sub(result.Session.CreatedAt))
		require.Equal(t, time.Hour, result.Session.ExpiresAt.Sub(result.Session.CreatedAt))
		require.Equal(t, 5*time.Minute, result.AccessExpiresAt.Sub(result.Session.CreatedAt))
		require.Equal(t, time.Hour, result.RefreshExpiresAt.Sub(result.Session.CreatedAt))
	}
	sessions, err := ss.Session().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, sessions, 1, "sessions after exchange")

	disabledUser := saveUser(t, ctx, ss)
	disabledTransaction, disabledHandle, disabledProof, disabledState, disabledVerifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, disabledTransaction)
	require.NoError(t, err)
	disabledBinding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss, disabledHandle, disabledProof, disabledState, disabledUser.ID)
	disabledCode := model.NewCredentialToken()
	disabledIssueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "issue-disabled-user")
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(disabledBinding), StateHash: model.HashToken(disabledState),
		CodeHash: model.HashToken(disabledCode), ExpectedUserID: disabledUser.ID, CodeLifetime: 45 * time.Second,
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: disabledIssueAudit.ID, AuditAt: model.GetMillis(),
	})
	require.NoError(t, err)
	disableAudit := saveUserProfileAuditAttempt(t, ctx, ss, disabledUser.ID.String())
	disabled, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledUser.ID, ExpectedRevision: disabledUser.Revision, Disabled: true,
		ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: disableAudit.ID,
		AuditAt: model.GetMillis(), Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	}))
	require.NoError(t, err)
	require.NotNil(t, disabled)
	require.NotNil(t, disabled.User)
	require.True(t, disabled.User.DisabledAt.Valid)
	rejectedIssue, rejectedHandle, rejectedProof, rejectedState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, rejectedIssue)
	require.NoError(t, err)
	rejectedBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), rejectedHandle, rejectedProof, rejectedState)
	_, err = ss.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(rejectedBinding), UserID: disabledUser.ID,
		PasswordProof:        testPasswordProof(t, ctx, ss, disabledUser.ID),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.GetMillis(),
		Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	require.True(t, store.IsNotFound(err), "AuthenticateDesktopAuthorization(disabled user) error")
	disabledExchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, disabledUser.ID, "exchange-disabled-user")
	_, err = ss.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchange(model.NowUTC(), disabledCode, disabledState, disabledVerifier, disabledExchangeAudit))
	require.True(t, store.IsNotFound(err), "Exchange(disabled user) error")
	disabledSessions, listErr := ss.Session().ListByUser(ctx, disabledUser.ID)
	require.NoError(t, listErr)
	require.Empty(t, disabledSessions)
	disabledExchangeAuditAfter, getErr := ss.Audit().Get(ctx, disabledExchangeAudit.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.AuditStatusAttempt, disabledExchangeAuditAfter.Status)

	cancelled, cancelHandle, cancelProof, cancelState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
	_, err = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, cancelled)
	require.NoError(t, err)
	cancelBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), cancelHandle, cancelProof, cancelState)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState)})
	require.NoError(t, err)
	err = ss.BrowserAuthentication().Cancel(ctx, &store.DesktopAuthorizationCancellation{BindingHash: model.HashToken(cancelBinding), StateHash: model.HashToken(cancelState)})
	require.True(t, store.IsNotFound(err), "Cancel(replay) error")

	t.Run("ActiveAttemptFencesAuthenticationAndExchange", func(t *testing.T) {
		fixture := newExamAttemptFixture(t, ctx, ss)
		issuedTransaction, issuedHandle, issuedProof, issuedState, issuedVerifier := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
		_, createErr := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, issuedTransaction)
		require.NoError(t, createErr)
		issuedBinding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss,
			issuedHandle, issuedProof, issuedState, fixture.candidate.ID)
		issuedCode := model.NewCredentialToken()
		issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixture.candidate.ID, "issue-before-attempt")
		_, issueErr := ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
			BindingHash: model.HashToken(issuedBinding), StateHash: model.HashToken(issuedState),
			CodeHash: model.HashToken(issuedCode), ExpectedUserID: fixture.candidate.ID, CodeLifetime: 45 * time.Second,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: issueAudit.ID, AuditAt: model.GetMillis(),
		})
		require.NoError(t, issueErr)

		pending, pendingHandle, pendingProof, pendingState, _ := newDesktopAuthorizationTransaction(model.NowUTC(), institution.ID)
		_, createErr = ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, pending)
		require.NoError(t, createErr)
		pendingBinding := bindDesktopAuthorization(t, ctx, ss.BrowserAuthentication(), pendingHandle, pendingProof, pendingState)

		continuity := model.HashToken(model.NewCredentialToken())
		connect := &store.ExamAttemptConnect{
			SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
			DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
			AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
			ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
			ContinuityCredentialHash: continuity,
			AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis(),
		}
		prepareExamAttemptConnect(t, ctx, ss, connect)
		_, connectErr := ss.ExamAttempt().Connect(ctx, connect,
			examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "desktop-lock", "desktop-lock"))
		require.NoError(t, connectErr)

		authenticated, authenticationErr := ss.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx,
			&store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(pendingBinding), UserID: fixture.candidate.ID,
				PasswordProof:        testPasswordProof(t, ctx, ss, fixture.candidate.ID),
				AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: model.GetMillis(),
				Capabilities:    store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			})
		require.NoError(t, authenticationErr)
		require.NotNil(t, authenticated)
		require.True(t, authenticated.Denied)
		_, contextErr := ss.BrowserAuthentication().GetDesktopAuthorizationContext(ctx, model.HashToken(pendingBinding))
		require.True(t, store.IsNotFound(contextErr), "GetDesktopAuthorizationContext(denied) error")

		exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institution.ID, fixture.candidate.ID, "exchange-during-attempt")
		denied, exchangeErr := ss.BrowserAuthentication().Exchange(ctx,
			desktopAuthorizationExchange(model.NowUTC(), issuedCode, issuedState, issuedVerifier, exchangeAudit))
		require.NoError(t, exchangeErr)
		require.NotNil(t, denied)
		require.True(t, denied.Denied)
		require.Nil(t, denied.Session)
		completedAudit, auditErr := ss.Audit().Get(ctx, exchangeAudit.ID)
		require.NoError(t, auditErr)
		require.Equal(t, model.AuditStatusFail, completedAudit.Status)
		require.Equal(t, "authentication.desktop_authorization.account_session_locked", completedAudit.ErrorCode)
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
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.False(t, bound.ExpiresAt.IsZero())
	return binding
}

func bindAndAuthenticateDesktopAuthorization(t *testing.T, ctx context.Context, ss store.Store,
	handle, proof, state string, userID model.UserID,
) string {
	t.Helper()
	persistence := ss.BrowserAuthentication()
	binding := bindDesktopAuthorization(t, ctx, persistence, handle, proof, state)
	result, err := persistence.AuthenticateDesktopAuthorization(ctx, &store.DesktopAuthorizationAuthentication{
		BindingHash: model.HashToken(binding), UserID: userID, AuthenticationMethod: "password",
		PasswordProof:          testPasswordProof(t, ctx, ss, userID),
		AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.GetMillis(),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Denied)
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
		Capabilities:  store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}, AuditEventID: audit.ID, AuditAt: model.GetMillis()}
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
	require.NoError(t, err)
	return event
}
