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

	"github.com/sudosylabs/proctor/server/app/apperror"
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

func (f *localLoginAuditorFake) BeginLocalLogin(_ context.Context, invocation Invocation, userID model.UserID, clientType model.SessionClientType) (model.AuditEventID, error) {
	if f.pending == nil {
		f.pending = map[string]*localLoginAuditRecord{}
	}
	id := model.NewId()
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

func (f *localLoginAuditorFake) RecordLocalLoginFailure(_ context.Context, invocation Invocation, target model.UserID,
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
	service.loginAudit = audit
	user, err := service.CreateLocalUser(context.Background(), CreateLocalUserCommand{
		User: &model.User{Username: "audited-user", Email: "audited-user@example.edu"}, Password: "CorrectHorseBatteryStaple1!",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, persistence, audit, user
}

func localLoginInvocation() Invocation {
	return NewInvocation(model.Principal{}, model.RequestMetadata{RequestID: "request-login", IPAddress: "192.0.2.10", UserAgent: "login-test"})
}

func TestLocalLoginSuccessIsDurablyAuditedWithActorAndSession(t *testing.T) {
	t.Parallel()
	service, _, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})

	result, err := service.Login(context.Background(), localLoginInvocation(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientWeb, Source: "192.0.2.10:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.pending) != 0 || len(audit.terminal) != 1 {
		t.Fatalf("pending=%#v terminal=%#v", audit.pending, audit.terminal)
	}
	record := audit.terminal[0]
	if record.status != model.AuditStatusSuccess || record.actor != user.ID || record.clientType != model.SessionClientWeb ||
		record.metadata.IPAddress != "192.0.2.10" || record.metadata.UserAgent != "login-test" || record.metadata.RequestID != "request-login" {
		t.Fatalf("success audit = %#v", record)
	}
	projection, ok := record.result.(map[string]any)
	if !ok || projection["id"] != result.Session.ID.String() {
		t.Fatalf("success audit result = %#v, session %s", record.result, result.Session.ID)
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
			if !apperror.Is(err, test.code) {
				t.Fatalf("err = %v", err)
			}
			if len(persistence.sessions) != 0 || len(audit.pending) != 0 || len(audit.terminal) != 1 {
				t.Fatalf("sessions=%d pending=%#v terminal=%#v", len(persistence.sessions), audit.pending, audit.terminal)
			}
			record := audit.terminal[0]
			wantTarget := model.UserID("")
			if test.knownTarget {
				wantTarget = user.ID
			}
			if record.status != model.AuditStatusFail || record.actor != "" || record.target != wantTarget ||
				record.errorCode != test.code || record.clientType != model.SessionClientCLI ||
				record.metadata.IPAddress != "192.0.2.10" || record.metadata.UserAgent != "login-test" {
				t.Fatalf("failure audit = %#v", record)
			}
		})
	}
}

func TestLocalLoginRejectedBeforeAdmissionWritesNoAudit(t *testing.T) {
	t.Parallel()
	service, _, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})
	service.loginRateLimit.MaximumAttempts = 1
	command := LoginCommand{LoginID: user.Email, Password: "incorrect password", ClientType: model.SessionClientCLI, Source: "192.0.2.10:3"}
	if _, err := service.Login(context.Background(), localLoginInvocation(), command); !apperror.Is(err, "authentication.credentials_invalid") {
		t.Fatalf("first err = %v", err)
	}
	if _, err := service.Login(context.Background(), localLoginInvocation(), command); !apperror.Is(err, "authentication.rate_limited") {
		t.Fatalf("limited err = %v", err)
	}
	if len(audit.terminal) != 1 || audit.terminal[0].errorCode != "authentication.credentials_invalid" {
		t.Fatalf("rate-limited attempt was audited: %#v", audit.terminal)
	}
}

func TestLocalLoginRevokesSessionWhenSuccessAuditCannotComplete(t *testing.T) {
	t.Parallel()
	service, persistence, audit, user := newLocalLoginAuditFixture(t, discardAuthenticationMFAVerifier{})
	audit.completeErr = apperror.New("audit.unavailable")

	result, err := service.Login(context.Background(), localLoginInvocation(), LoginCommand{
		LoginID: user.Email, Password: "CorrectHorseBatteryStaple1!", ClientType: model.SessionClientCLI, Source: "192.0.2.10:4",
	})
	if result != nil || !apperror.Is(err, "audit.unavailable") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(persistence.sessions) != 1 {
		t.Fatalf("sessions = %d", len(persistence.sessions))
	}
	for _, session := range persistence.sessions {
		if !session.RevokedAt.Valid || session.RevocationReason != model.SessionRevocationAuthenticationAuditFailed {
			t.Fatalf("unaudited session remains live: %#v", session)
		}
	}
}

func TestAuditServiceLocalLoginEventsCarryRequestIdentity(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	institutionID := model.NewInstitutionID()
	service, err := newAuditService(audits, loginAuditInstitutionStore{id: institutionID}, "node-login")
	if err != nil {
		t.Fatal(err)
	}
	metadata := model.RequestMetadata{RequestID: "request-1", IPAddress: "192.0.2.20", UserAgent: "agent"}
	userID := model.NewUserID()
	event, err := service.BeginLocalLogin(context.Background(), userID, model.SessionClientWeb, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if event.ActorID != userID || event.Action != "authentication.login" || event.Status != model.AuditStatusAttempt ||
		event.AuthMethod != "password" || event.IPAddress != "192.0.2.20" || event.UserAgent != "agent" ||
		event.ScopeID != institutionID.String() || event.Resource.ID != userID.String() {
		t.Fatalf("begin event = %#v", event)
	}
	if err := service.RecordLocalLoginFailure(context.Background(), "", model.SessionClientCLI, metadata,
		"authentication.credentials_invalid"); err != nil {
		t.Fatal(err)
	}
	failed := audits.saved
	if failed.ActorID != "" || failed.Status != model.AuditStatusFail || failed.ErrorCode != "authentication.credentials_invalid" ||
		failed.Resource != (model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}) || failed.IPAddress != "192.0.2.20" {
		t.Fatalf("failure event = %#v", failed)
	}
}

type loginAuditInstitutionStore struct {
	store.InstitutionStore
	id model.InstitutionID
}

func (s loginAuditInstitutionStore) GetSingleton(context.Context) (*model.Institution, error) {
	return &model.Institution{ID: s.id}, nil
}
