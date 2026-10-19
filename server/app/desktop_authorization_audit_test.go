// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func newAuditedDesktopAuthorizationService(t *testing.T, persistence *desktopAuthorizationStoreFake,
	auditor desktopAuthorizationAuditor, at time.Time,
) *desktopAuthorizationService {
	t.Helper()
	service, err := newDesktopAuthorizationService(
		persistence,
		desktopAuthorizationInstitutionStoreFake{institution: &model.Institution{ID: model.NewInstitutionID()}},
		desktopAuthorizationAccessPolicyFake{enabled: true},
		&accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{}}, auditor,
		&desktopAuthorizationAttemptLimiterFake{},
		testDesktopSessionPolicy(), DesktopAuthorizationPolicy{Issuer: "https://proctor.example.edu"},
		model.NewCredentialToken, func() time.Time { return at },
		testDesktopAuthorizationIdentity(t, "https://proctor.example.edu", func() time.Time { return at }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestDesktopAuthorizationApproveRejectsForeignPrincipal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auditor := &recordingDesktopAuthorizationAuditor{}
	persistence := &desktopAuthorizationStoreFake{contextUserID: model.NewUserID(),
		issueResult: &store.DesktopAuthorizationCodeIssued{CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), CodeExpiresAt: at.Add(time.Minute)}}
	service := newAuditedDesktopAuthorizationService(t, persistence, auditor, at)
	foreign := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}

	approval, err := service.Approve(context.Background(), NewInvocation(foreign, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	if approval != nil || !Is(err, "authentication.desktop_authorization.rejected") {
		t.Fatalf("foreign approval = %#v, %v", approval, err)
	}
	if persistence.issued != nil || auditor.prepareIssue != 0 {
		t.Fatalf("foreign approval issued=%#v audits=%d", persistence.issued, auditor.prepareIssue)
	}
}

func TestDesktopAuthorizationApproveAndExchangeAuditTheApprovingAccount(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	auditor := &recordingDesktopAuthorizationAuditor{}
	account := model.NewUserID()
	persistence := &desktopAuthorizationStoreFake{contextUserID: account, exchangeUserID: account,
		issueResult: &store.DesktopAuthorizationCodeIssued{CallbackURL: "http://127.0.0.1:49152/" + model.NewCredentialToken(), CodeExpiresAt: at.Add(time.Minute)},
		exchangeErr: store.NewErrNotFound("browser_authentication_transaction", "")}
	service := newAuditedDesktopAuthorizationService(t, persistence, auditor, at)
	browser := model.RequestMetadata{RequestID: "approve-request", IPAddress: "192.0.2.30", UserAgent: "browser"}

	if _, err := service.Approve(context.Background(), NewInvocation(model.Principal{}, browser),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()}); err != nil {
		t.Fatal(err)
	}
	if auditor.issueActor != account || auditor.issueRequest != browser {
		t.Fatalf("approval audit actor=%q request=%#v", auditor.issueActor, auditor.issueRequest)
	}
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	desktop := model.RequestMetadata{RequestID: "exchange-request", IPAddress: "192.0.2.31", UserAgent: "desktop"}
	if _, err := service.Exchange(context.Background(), NewInvocation(model.Principal{}, desktop), exchangeCommand); !Is(err, "authentication.desktop_authorization.rejected") {
		t.Fatalf("exchange err = %v", err)
	}
	if auditor.exchangeActor != account || auditor.exchangeRequest != desktop {
		t.Fatalf("exchange audit actor=%q request=%#v", auditor.exchangeActor, auditor.exchangeRequest)
	}
}

func TestDesktopAuthorizationAuditAdapterRecordsActorSessionAndRequest(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, loginAuditInstitutionStore{id: model.NewInstitutionID()}, "node-desktop")
	if err != nil {
		t.Fatal(err)
	}
	adapter := desktopAuthorizationAuditAdapter{audit: service}
	account, sessionID, institutionID := model.NewUserID(), model.NewSessionID(), model.NewInstitutionID()
	metadata := model.RequestMetadata{RequestID: "request-desktop", IPAddress: "192.0.2.40", UserAgent: "agent"}
	issue, err := adapter.PrepareIssue(context.Background(),
		NewInvocation(model.Principal{UserID: account, SessionID: sessionID}, metadata), account, institutionID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.ActorID != account || issue.SessionID != sessionID || issue.IPAddress != "192.0.2.40" || issue.UserAgent != "agent" ||
		issue.RequestID != "request-desktop" || issue.ClientType != string(model.SessionClientWeb) ||
		issue.ScopeID != institutionID.String() || issue.Status != model.AuditStatusAttempt {
		t.Fatalf("issue audit = %#v", issue)
	}
	exchange, err := adapter.PrepareExchange(context.Background(), NewInvocation(model.Principal{}, metadata), account, institutionID)
	if err != nil {
		t.Fatal(err)
	}
	if exchange.ActorID != account || exchange.SessionID != "" || exchange.IPAddress != "192.0.2.40" ||
		exchange.ClientType != string(model.SessionClientDesktop) || exchange.Action != "authentication.desktop_session_issue" {
		t.Fatalf("exchange audit = %#v", exchange)
	}
}
