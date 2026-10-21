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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
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
	require.NoError(t, err)
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
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}

	approval, err := service.ApproveDesktopAuthorization(context.Background(), NewInvocation(foreign, model.RequestMetadata{}),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	require.Nil(t, approval, "foreign approval = %#v, %v", approval, err)
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.rejected"), "foreign approval = %#v, %v", approval, err)
	require.Nil(t, persistence.issued, "foreign approval issued")
	require.Equal(t, 0, auditor.prepareIssue, "foreign approval issued")
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

	_, err := service.ApproveDesktopAuthorization(context.Background(), NewInvocation(model.Principal{}, browser),
		ApproveDesktopAuthorizationCommand{Binding: model.NewCredentialToken(), State: model.NewCredentialToken()})
	require.NoError(t, err)
	require.Equal(t, account, auditor.issueActor, "approval audit actor")
	require.Equal(t, browser, auditor.issueRequest, "approval audit actor")
	exchangeCommand := testDesktopAuthorizationExchangeCommand(
		t, service, at, model.NewCredentialToken(), model.NewCredentialToken(), model.NewCredentialToken(), "",
	)
	desktop := model.RequestMetadata{RequestID: "exchange-request", IPAddress: "192.0.2.31", UserAgent: "desktop"}
	_, err = service.ExchangeDesktopAuthorization(context.Background(), NewInvocation(model.Principal{}, desktop), exchangeCommand)
	require.True(t, apperror.Is(err, "authentication.desktop_authorization.rejected"), "exchange err = %v", err)
	require.Equal(t, account, auditor.exchangeActor, "exchange audit actor")
	require.Equal(t, desktop, auditor.exchangeRequest, "exchange audit actor")
}

func TestDesktopAuthorizationAuditAdapterRecordsActorSessionAndRequest(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, loginAuditInstitutionStore{id: model.NewInstitutionID()}, "node-desktop")
	require.NoError(t, err)
	adapter := desktopAuthorizationAuditAdapter{audit: service}
	account, sessionID, institutionID := model.NewUserID(), model.NewSessionID(), model.NewInstitutionID()
	metadata := model.RequestMetadata{RequestID: "request-desktop", IPAddress: "192.0.2.40", UserAgent: "agent"}
	issue, err := adapter.PrepareIssue(context.Background(),
		NewInvocation(model.Principal{UserID: account, SessionID: sessionID}, metadata), account, institutionID)
	require.NoError(t, err)
	require.Equal(t, account, issue.ActorID)
	require.Equal(t, sessionID, issue.SessionID)
	require.Equal(t, "192.0.2.40", issue.IPAddress)
	require.Equal(t, "agent", issue.UserAgent)
	require.Equal(t, "request-desktop", issue.RequestID)
	require.Equal(t, string(model.SessionClientWeb), issue.ClientType)
	require.Equal(t, institutionID.String(), issue.ScopeID)
	require.Equal(t, model.AuditStatusAttempt, issue.Status)
	exchange, err := adapter.PrepareExchange(context.Background(), NewInvocation(model.Principal{}, metadata), account, institutionID)
	require.NoError(t, err)
	require.Equal(t, account, exchange.ActorID)
	require.Empty(t, exchange.SessionID)
	require.Equal(t, "192.0.2.40", exchange.IPAddress)
	require.Equal(t, string(model.SessionClientDesktop), exchange.ClientType)
	require.Equal(t, "authentication.desktop_session_issue", exchange.Action)
}
