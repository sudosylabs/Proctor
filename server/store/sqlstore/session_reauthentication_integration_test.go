//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPasswordReauthenticationCommitsOnlyCurrentProofAndAudit(t *testing.T) {
	for _, name := range []string{"success", "missing audit", "changed password", "revoked credential", "recovery generation changed"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := mfaRecoveryFixture(t, ctx)
			s := f.persistence
			session, err := s.Session().Get(ctx, f.principal.SessionID.String())
			if err != nil {
				t.Fatal(err)
			}
			audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.actor, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
			input := &store.SessionPasswordReauthentication{SessionID: session.ID, CredentialID: model.SessionCredentialID(f.principal.CredentialID), UserID: f.actor.ID, PasswordProof: passwordProofForSQLTest(t, ctx, s, f.actor.ID), AuditEventID: audit.ID}
			switch name {
			case "missing audit":
				input.AuditEventID = model.NewAuditEventID()
			case "changed password":
				_, err = s.GetMaster().Exec(ctx, `UPDATE password_credentials SET revision=revision+1 WHERE user_id=?`, f.actor.ID.String())
			case "revoked credential":
				_, err = s.GetMaster().Exec(ctx, `UPDATE session_credentials SET revoked_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=?`, input.CredentialID.String())
			case "recovery generation changed":
				_, err = s.GetMaster().Exec(ctx, `INSERT INTO user_mfa_recovery(user_id,generation,reset_at,reenrollment_required,updated_at) VALUES(?,1,clock_timestamp(),true,clock_timestamp())`, f.actor.ID.String())
			}
			if err != nil {
				t.Fatal(err)
			}
			before := model.NowUTC()
			result, err := s.Session().ReauthenticatePasswordWithAudit(ctx, input)
			current, getErr := s.Session().Get(ctx, session.ID.String())
			if getErr != nil {
				t.Fatal(getErr)
			}
			if name != "success" {
				if err == nil || result != nil || current.ReauthenticatedAt.Valid {
					t.Fatalf("failed proof or audit changed Session: %#v %v", current, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Session.ID != session.ID || !current.ReauthenticatedAt.Valid || current.ReauthenticatedAt.Time.Before(before) || current.ReauthenticatedAt.Time.After(model.NowUTC()) || !current.AuthenticatedAt.Equal(session.AuthenticatedAt) || current.MFACompletedAt != session.MFACompletedAt || current.ExpiresAt != session.ExpiresAt || current.IdleExpiresAt != session.IdleExpiresAt || current.AuthenticationStrength != session.AuthenticationStrength || len(result.AccessTokenHashes) != 1 {
				t.Fatalf("reauthentication changed original provenance or expiry: %#v", current)
			}
			storedAudit, err := s.Audit().Get(ctx, audit.ID.String())
			if err != nil || storedAudit.Status != model.AuditStatusSuccess {
				t.Fatal("fresh proof omitted terminal audit")
			}
		})
	}
}

func TestExternalReauthenticationRechecksExactIdentityAndLiveBoundSession(t *testing.T) {
	for _, name := range []string{"success", "wrong subject", "unlinked identity", "revoked credential", "old proof", "disabled provider", "terminal audit"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s := openTestStore(t)
			resetPristineTestStore(t, s)
			seedTestAuthenticationPolicy(t, s, map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly})
			user := saveIntegrationUser(t, ctx, s, &model.User{Username: "fresh-provider", Email: "provider@example.edu"})
			identity, err := s.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus", Subject: "exact-subject"})
			if err != nil {
				t.Fatal(err)
			}
			candidate, credentials := authenticationPolicyTestSession(user.ID, "oidc", "campus", identity.ID)
			session, savedCredentials, err := s.Session().Save(ctx, sessionCreationForSQLTest(t, ctx, s, candidate, credentials, 10))
			if err != nil {
				t.Fatal(err)
			}
			audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, user, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
			auditID := audit.ID.String()
			stateHash, bindingHash := model.HashToken(model.NewCredentialToken()), model.HashToken(model.NewCredentialToken())
			state, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeReauthenticate, TargetUserID: user.ID, SessionID: session.ID, SessionCredentialID: savedCredentials[0].ID, ExternalIdentityID: identity.ID, AuditEventID: auditID, StateHash: stateHash, BindingHash: bindingHash, ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			state, err = s.ExternalLoginState().Consume(ctx, "campus", stateHash, bindingHash)
			if err != nil {
				t.Fatal(err)
			}
			input := &store.SessionExternalReauthentication{StateID: state.ID, ProviderID: "campus", Subject: identity.Subject, AuthenticatedAt: model.NowUTC()}
			switch name {
			case "wrong subject":
				input.Subject = "another-account"
			case "unlinked identity":
				_, err = s.GetMaster().Exec(ctx, `UPDATE external_identities SET archived_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=?`, identity.ID.String())
			case "revoked credential":
				_, err = s.GetMaster().Exec(ctx, `UPDATE session_credentials SET revoked_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=?`, savedCredentials[0].ID.String())
			case "old proof":
				input.AuthenticatedAt = state.CreatedAt.Add(-time.Minute)
			case "disabled provider":
				_, err = s.GetMaster().Exec(ctx, `UPDATE access_policies SET provider_admissions='{}'::jsonb`)
			case "terminal audit":
				_, err = s.Audit().Complete(ctx, audit.ID.String(), model.AuditStatusFail, "authentication.invalid_token", nil, model.GetMillis())
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Session().ReauthenticateExternalWithAudit(ctx, input)
			current, getErr := s.Session().Get(ctx, session.ID.String())
			if getErr != nil {
				t.Fatal(getErr)
			}
			if name != "success" {
				if err == nil || result != nil || current.ReauthenticatedAt.Valid {
					t.Fatalf("invalid provider proof changed Session: %#v %v", current, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !current.ReauthenticatedAt.Time.Equal(input.AuthenticatedAt) || !current.AuthenticatedAt.Equal(session.AuthenticatedAt) || current.MFACompletedAt != session.MFACompletedAt || current.AuthenticationMethod != session.AuthenticationMethod || current.ExternalIdentityID != identity.ID {
				t.Fatalf("fresh provider proof rewrote original provenance: %#v", current)
			}
			if _, err = s.Session().ReauthenticateExternalWithAudit(ctx, input); err == nil {
				t.Fatal("completed one-use proof committed a second audit transition")
			}
		})
	}
}

func TestPasswordReauthenticationRechecksCredentialAfterWaitingForFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := mfaRecoveryFixture(t, ctx)
	s := f.persistence
	audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, f.target, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
	input := &store.SessionPasswordReauthentication{SessionID: f.targetSession.ID, CredentialID: f.targetCredential.ID, UserID: f.target.ID, PasswordProof: passwordProofForSQLTest(t, ctx, s, f.target.ID), AuditEventID: audit.ID}
	pid, release := holdSQLAdvisoryFence(t, ctx, s, "proctor:session-user:"+f.target.ID.String())
	defer release()
	done := make(chan error, 1)
	go func() { _, err := s.Session().ReauthenticatePasswordWithAudit(ctx, input); done <- err }()
	waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, s, pid, 1)
	if _, err := s.GetMaster().Exec(ctx, `UPDATE session_credentials SET revoked_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=?`, f.targetCredential.ID.String()); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err == nil {
		t.Fatal("proof collected before revocation survived the fence")
	}
	current, err := s.Session().Get(ctx, f.targetSession.ID.String())
	if err != nil || current.ReauthenticatedAt.Valid {
		t.Fatal("revoked proof refreshed authentication")
	}
}
