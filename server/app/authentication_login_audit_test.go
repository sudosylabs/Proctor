// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type localLoginAuditRecord struct {
	status     model.AuditStatus
	actor      model.UserID
	target     model.UserID
	clientType model.SessionClientType
	metadata   model.RequestMetadata
	errorCode  string
	result     any
}

// localLoginAuditorFake keeps the terminal outcome of every local login audit
// so tests assert what became durable rather than which calls were made.
type localLoginAuditorFake struct {
	pending     map[string]*localLoginAuditRecord
	terminal    []localLoginAuditRecord
	completeErr error
}

func (f *localLoginAuditorFake) BeginLocalLogin(_ context.Context, invocation invocation.Invocation, userID model.UserID, clientType model.SessionClientType) (model.AuditEventID, error) {
	if f.pending == nil {
		f.pending = map[string]*localLoginAuditRecord{}
	}
	id := model.NewID()
	f.pending[id] = &localLoginAuditRecord{status: model.AuditStatusAttempt, actor: userID, target: userID,
		clientType: clientType, metadata: invocation.RequestMetadata()}
	return model.AuditEventID(id), nil
}

func (f *localLoginAuditorFake) CompleteLocalLogin(_ context.Context, auditID model.AuditEventID, status model.AuditStatus, errorCode string, result any) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	record, ok := f.pending[auditID.String()]
	if !ok {
		return errors.New("unknown audit attempt")
	}
	delete(f.pending, auditID.String())
	record.status, record.errorCode, record.result = status, errorCode, result
	f.terminal = append(f.terminal, *record)
	return nil
}

func (f *localLoginAuditorFake) RecordLocalLoginFailure(_ context.Context, invocation invocation.Invocation, target model.UserID,
	clientType model.SessionClientType, errorCode string,
) error {
	f.terminal = append(f.terminal, localLoginAuditRecord{status: model.AuditStatusFail, target: target,
		clientType: clientType, metadata: invocation.RequestMetadata(), errorCode: errorCode})
	return nil
}

type failingLoginMFAVerifier struct {
	discardAuthenticationMFAVerifier
}

func (failingLoginMFAVerifier) verifyLogin(context.Context, string, string, time.Time) (model.AuthenticationStrength, int64, error) {
	return "", 0, apperror.New("authentication.mfa.code_invalid")
}

func newLocalLoginAuditFixture(t *testing.T, mfa authenticationMFAVerifier) (*authenticationService, *authenticationStoreFake, *localLoginAuditorFake, *model.User) {
	t.Helper()
	persistence := newAuthenticationStoreFake()
	service := newTestAuthenticationServiceWithPorts(t, persistence, newAuthenticationCacheFake(), mfa,
		discardAuthenticationPATResolver{}, model.NewCredentialToken, time.Now)
	audit := &localLoginAuditorFake{}
	service.deps.LoginAudit = audit
	user, err := service.CreateLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{Username: "audited-user", Email: "audited-user@example.edu"}, Password: "CorrectHorseBatteryStaple1!",
	})
	require.NoError(t, err)
	return service, persistence, audit, user
}

func localLoginInvocation() invocation.Invocation {
	return invocation.New(model.Principal{}, model.RequestMetadata{RequestID: "request-login", IPAddress: "192.0.2.10", UserAgent: "login-test"})
}

func TestLocalLoginSuccessIsDurablyAuditedWithActorAndSession(t *testing.T) {
	t.Parallel()
	service, persistence, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})

	result, err := service.Login(context.Background(), localLoginInvocation(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientWeb, Source: "192.0.2.10:1",
	})
	require.NoError(t, err)
	// The application begins the attempt; Session creation completes it in
	// the same transaction, so the application records no terminal outcome.
	require.Len(t, audit.pending, 1, "pending")
	require.Empty(t, audit.terminal, "pending")
	for id, record := range audit.pending {
		require.Equal(t, user.ID, record.actor)
		require.Equal(t, model.SessionClientWeb, record.clientType)
		require.Equal(t, "192.0.2.10", record.metadata.IPAddress)
		require.Equal(t, "login-test", record.metadata.UserAgent)
		require.Equal(t, "request-login", record.metadata.RequestID)
		projection, ok := persistence.completedAudits[id]
		require.True(t, ok, "Session creation completion = %#v, session %s", persistence.completedAudits, result.Session.ID)
		require.Equal(t, result.Session.ID.String(), projection["id"], "Session creation completion = %#v, session %s", persistence.completedAudits, result.Session.ID)
	}
}

func TestLocalLoginFailuresAreDurablyAudited(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		mfa         authenticationMFAVerifier
		loginID     string
		password    string
		code        string
		knownTarget bool
	}{
		{name: "wrong password", mfa: discardAuthenticationMFAVerifier{}, loginID: "audited-user@example.edu",
			password: "incorrect password", code: "authentication.credentials_invalid", knownTarget: true},
		{name: "unknown account", mfa: discardAuthenticationMFAVerifier{}, loginID: "nobody@example.edu",
			password: "CorrectHorseBatteryStaple1!", code: "authentication.credentials_invalid"},
		{name: "mfa failure", mfa: failingLoginMFAVerifier{}, loginID: "audited-user@example.edu",
			password: "CorrectHorseBatteryStaple1!", code: "authentication.mfa.code_invalid", knownTarget: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, persistence, audit, user := newLocalLoginAuditFixture(t, test.mfa)
			_, err := service.Login(context.Background(), localLoginInvocation(), LoginCommand{
				LoginID: test.loginID, Password: test.password, ClientType: model.SessionClientCLI, Source: "192.0.2.10:2",
			})
			require.True(t, apperror.Is(err, test.code), "err = %v", err)
			require.Empty(t, persistence.sessions, "sessions")
			require.Empty(t, audit.pending, "sessions")
			require.Len(t, audit.terminal, 1, "sessions")
			record := audit.terminal[0]
			wantTarget := model.UserID("")
			if test.knownTarget {
				wantTarget = user.ID
			}
			require.Equal(t, model.AuditStatusFail, record.status)
			require.Empty(t, record.actor)
			require.Equal(t, wantTarget, record.target)
			require.Equal(t, test.code, record.errorCode)
			require.Equal(t, model.SessionClientCLI, record.clientType)
			require.Equal(t, "192.0.2.10", record.metadata.IPAddress)
			require.Equal(t, "login-test", record.metadata.UserAgent)
		})
	}
}

func TestLocalLoginRejectedBeforeAdmissionWritesNoAudit(t *testing.T) {
	t.Parallel()
	service, _, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})
	service.deps.LoginRateLimit.MaximumAttempts = 1
	command := LoginCommand{LoginID: user.Email, Password: "incorrect password", ClientType: model.SessionClientCLI, Source: "192.0.2.10:3"}
	_, err := service.Login(context.Background(), localLoginInvocation(), command)
	require.True(t, apperror.Is(err, "authentication.credentials_invalid"), "first err = %v", err)
	_, err = service.Login(context.Background(), localLoginInvocation(), command)
	require.True(t, apperror.Is(err, "authentication.rate_limited"), "limited err = %v", err)
	require.Len(t, audit.terminal, 1)
	require.Equal(t, "authentication.credentials_invalid", audit.terminal[0].errorCode)
}

func TestLocalLoginIssuesNoSessionWhenSuccessAuditCannotComplete(t *testing.T) {
	t.Parallel()
	service, persistence, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})
	persistence.auditCompleteErr = errors.New("audit completion unavailable")

	result, err := service.Login(context.Background(), localLoginInvocation(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientCLI, Source: "192.0.2.10:4",
	})
	require.Nil(t, result, "result")
	require.Error(t, err, "result")
	require.Empty(t, persistence.sessions, "sessions")
	require.Empty(t, audit.pending, "pending")
	require.Len(t, audit.terminal, 1, "pending")
	require.Equal(t, model.AuditStatusFail, audit.terminal[0].status, "pending")
}

func TestAuditServiceLocalLoginEventsCarryRequestIdentity(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	institutionID := model.NewInstitutionID()
	service, err := newAuditService(auditServiceDependencies{Audits: audits, Institutions: loginAuditInstitutionStore{id: institutionID}, NodeID: "node-login"})
	require.NoError(t, err)
	metadata := model.RequestMetadata{RequestID: "request-1", IPAddress: "192.0.2.20", UserAgent: "agent"}
	userID := model.NewUserID()
	event, err := service.BeginLocalLogin(context.Background(), userID, model.SessionClientWeb, metadata)
	require.NoError(t, err)
	require.Equal(t, userID, event.ActorID)
	require.Equal(t, "authentication.login", event.Action)
	require.Equal(t, model.AuditStatusAttempt, event.Status)
	require.Equal(t, "password", event.AuthMethod)
	require.Equal(t, "192.0.2.20", event.IPAddress)
	require.Equal(t, "agent", event.UserAgent)
	require.Equal(t, institutionID.String(), event.ScopeID)
	require.Equal(t, userID.String(), event.Resource.ID)
	require.NoError(t, service.RecordLocalLoginFailure(context.Background(), "", model.SessionClientCLI, metadata,
		"authentication.credentials_invalid"))
	failed := audits.saved
	require.Empty(t, failed.ActorID)
	require.Equal(t, model.AuditStatusFail, failed.Status)
	require.Equal(t, "authentication.credentials_invalid", failed.ErrorCode)
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, failed.Resource)
	require.Equal(t, "192.0.2.20", failed.IPAddress)
}

type loginAuditInstitutionStore struct {
	store.InstitutionStore
	id model.InstitutionID
}

func (s loginAuditInstitutionStore) GetSingleton(context.Context) (*model.Institution, error) {
	return &model.Institution{ID: s.id}, nil
}
