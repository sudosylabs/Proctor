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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestPasswordReauthenticationCommitsOnlyCurrentProofAndAudit(t *testing.T) {
	for _, name := range []string{"success", "missing audit", "changed password", "revoked credential", "recovery generation changed"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f := mfaRecoveryFixture(t, ctx)
			s := f.persistence
			session, err := s.Session().Get(ctx, f.principal.SessionID)
			require.NoError(t, err)
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
			require.NoError(t, err)
			before := model.NowUTC()
			result, err := s.Session().ReauthenticatePassword(ctx, input)
			current, getErr := s.Session().Get(ctx, session.ID)
			require.NoError(t, getErr)
			if name != "success" {
				require.Error(t, err, "failed proof or audit changed Session: %#v", current)
				require.Nil(t, result, "failed proof or audit changed Session: %#v", current)
				require.False(t, current.ReauthenticatedAt.Valid, "failed proof or audit changed Session: %#v", current)
				return
			}
			require.NoError(t, err)
			require.Equal(t, session.ID, result.Session.ID)
			require.True(t, current.ReauthenticatedAt.Valid)
			// The proof is stamped with PostgreSQL time, which may trail or
			// lead the host clock by a few milliseconds.
			require.WithinRange(t, current.ReauthenticatedAt.Time, before.Add(-time.Second), model.NowUTC().Add(time.Second))
			require.True(t, current.AuthenticatedAt.Equal(session.AuthenticatedAt))
			require.Equal(t, session.MFACompletedAt, current.MFACompletedAt)
			require.Equal(t, session.ExpiresAt, current.ExpiresAt)
			require.Equal(t, session.IdleExpiresAt, current.IdleExpiresAt)
			require.Equal(t, session.AuthenticationStrength, current.AuthenticationStrength)
			require.Len(t, result.AccessTokenHashes, 1)
			storedAudit, err := s.Audit().Get(ctx, audit.ID)
			require.NoError(t, err, "fresh proof omitted terminal audit")
			require.Equal(t, model.AuditStatusSuccess, storedAudit.Status, "fresh proof omitted terminal audit")
		})
	}
}

func TestExternalReauthenticationRechecksExactIdentityAndLiveBoundSession(t *testing.T) {
	for _, name := range []string{"success", "leading proof", "wrong subject", "unlinked identity", "revoked credential", "old proof", "future proof", "disabled provider", "terminal audit"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s := openTestStore(t)
			resetPristineTestStore(t, s)
			seedTestAuthenticationPolicy(t, s, map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly})
			user := saveIntegrationUser(t, ctx, s, &model.User{Username: "fresh-provider", Email: "provider@example.edu"})
			identity, err := s.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus", Subject: "exact-subject"})
			require.NoError(t, err)
			candidate, credentials := authenticationPolicyTestSession(user.ID, "oidc", "campus", identity.ID)
			session, savedCredentials, err := storetest.SaveSession(ctx, s, sessionCreationForSQLTest(t, ctx, s, candidate, credentials, 10))
			require.NoError(t, err)
			audit, _ := mfaSQLSecurityNoticeFixture(t, ctx, s, user, model.MailTemplateIdentityMFAEnabled, model.GetMillis())
			auditID := audit.ID.String()
			stateHash, bindingHash := model.HashToken(model.NewCredentialToken()), model.HashToken(model.NewCredentialToken())
			state, err := s.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus", Purpose: model.ExternalAuthenticationPurposeReauthenticate, TargetUserID: user.ID, SessionID: session.ID, SessionCredentialID: savedCredentials[0].ID, ExternalIdentityID: identity.ID, AuditEventID: auditID, StateHash: stateHash, BindingHash: bindingHash, ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
			require.NoError(t, err)
			state, err = s.ExternalLoginState().Consume(ctx, "campus", stateHash, bindingHash)
			require.NoError(t, err)
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
			case "leading proof":
				// A provider clock a little ahead of PostgreSQL is tolerated.
				input.AuthenticatedAt = model.NowUTC().Add(20 * time.Second)
			case "future proof":
				input.AuthenticatedAt = model.NowUTC().Add(2 * time.Minute)
			case "disabled provider":
				_, err = s.GetMaster().Exec(ctx, `UPDATE access_policies SET provider_admissions='{}'::jsonb`)
			case "terminal audit":
				_, err = s.Audit().Complete(ctx, audit.ID, model.AuditStatusFail, "authentication.token_invalid", nil, model.GetMillis())
			}
			require.NoError(t, err)
			result, err := s.Session().ReauthenticateExternal(ctx, input)
			current, getErr := s.Session().Get(ctx, session.ID)
			require.NoError(t, getErr)
			if name == "leading proof" {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.True(t, current.ReauthenticatedAt.Valid)
				require.True(t, current.ReauthenticatedAt.Time.Before(input.AuthenticatedAt),
					"a leading proof is recorded at database time, not %s", input.AuthenticatedAt)
				return
			}
			if name != "success" {
				require.Error(t, err, "invalid provider proof changed Session: %#v", current)
				require.Nil(t, result, "invalid provider proof changed Session: %#v", current)
				require.False(t, current.ReauthenticatedAt.Valid, "invalid provider proof changed Session: %#v", current)
				return
			}
			require.NoError(t, err)
			// The proof time is recorded as given unless it ran ahead of
			// PostgreSQL time, in which case database time is recorded.
			require.False(t, current.ReauthenticatedAt.Time.After(input.AuthenticatedAt))
			require.WithinDuration(t, input.AuthenticatedAt, current.ReauthenticatedAt.Time, time.Second)
			require.True(t, current.AuthenticatedAt.Equal(session.AuthenticatedAt))
			require.Equal(t, session.MFACompletedAt, current.MFACompletedAt)
			require.Equal(t, session.AuthenticationMethod, current.AuthenticationMethod)
			require.Equal(t, identity.ID, current.ExternalIdentityID)
			_, err = s.Session().ReauthenticateExternal(ctx, input)
			require.Error(t, err, "completed one-use proof committed a second audit transition")
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
	pid, release := holdSQLAdvisoryFence(t, ctx, s, advisoryLockKey("session-user", f.target.ID.String()))
	defer release()
	done := make(chan error, 1)
	go func() { _, err := s.Session().ReauthenticatePassword(ctx, input); done <- err }()
	waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, s, pid, 1)
	_, err := s.GetMaster().Exec(ctx, `UPDATE session_credentials SET revoked_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=?`, f.targetCredential.ID.String())
	require.NoError(t, err)
	release()
	require.Error(t, (<-done), "proof collected before revocation survived the fence")
	current, err := s.Session().Get(ctx, f.targetSession.ID)
	require.NoError(t, err, "revoked proof refreshed authentication")
	require.False(t, current.ReauthenticatedAt.Valid, "revoked proof refreshed authentication")
}
