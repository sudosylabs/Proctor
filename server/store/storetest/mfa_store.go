// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go MFA
// coverage. Proctor additionally verifies pending replacement, transactional
// activation, TOTP replay prevention, single-use hashed recovery codes,
// session assurance transitions, and concurrent consumption.

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMFAStore(t *testing.T, ss store.Store) {
	t.Run("NativeActivationAndSessionExpiry", func(t *testing.T) {
		testMFANativeExpiry(t, ss)
	})
	t.Run("LifecycleAndSessionAssurance", func(t *testing.T) {
		testMFALifecycleAndSessionAssurance(t, ss)
	})
	t.Run("PendingSetupIsReplaceable", func(t *testing.T) {
		testMFAPendingReplacement(t, ss)
	})
	t.Run("RecoveryCodeConsumptionIsSerialized", func(t *testing.T) {
		testMFARecoveryCodeConsumptionIsSerialized(t, ss)
	})
	t.Run("TransitionMailAndAuditRollbackTogether", func(t *testing.T) {
		testMFATransitionMailAndAuditRollbackTogether(t, ss)
	})
	t.Run("SecretKeyUsageCountsLiveCredentials", func(t *testing.T) {
		testMFASecretKeyUsage(t, ss)
	})
}

func testMFASecretKeyUsage(t *testing.T, ss store.Store) {
	ctx := context.Background()
	countFor := func(keyID string) int64 {
		usage, err := ss.MFA().ListSecretKeyUsage(ctx)
		require.NoError(t, err)
		for index, item := range usage {
			if index > 0 {
				require.Less(t, usage[index-1].KeyID, item.KeyID, "ListSecretKeyUsage() is not ordered by key")
			}
			if item.KeyID == keyID {
				return item.Credentials
			}
		}
		return 0
	}
	const keyID = "0123456789abcdef0123456789abcdef"
	before := countFor(keyID)
	savePendingMFA(t, ctx, ss, saveUser(t, ctx, ss).ID)
	require.Equal(t, before+1, countFor(keyID))
}

func testMFALifecycleAndSessionAssurance(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	firstHash := model.HashToken(model.NewCredentialToken())
	secondHash := model.HashToken(model.NewCredentialToken())
	now := model.TimeUTC(pending.CreatedAt).Add(1 * time.Millisecond)
	activationAudit, activationNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, now)
	activated, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{
			{CodeHash: firstHash},
			{CodeHash: secondHash},
		},
		SessionID: session.ID, At: model.TimeUTC(now), AuditEventID: activationAudit.ID, AuditAt: now,
		Notice: activationNotice,
	})
	require.NoError(t, err)
	requireMFATransitionCommitted(t, ctx, ss, user.ID, activationAudit, model.MailTemplateIdentityMFAEnabled, model.MailDeliveryQueued)
	require.True(t, activated.Credential.IsActive())
	require.Equal(t, model.AuthenticationMultiFactor, activated.Session.AuthenticationStrength)
	require.False(t, activated.Session.MFACompletedAt.Time.Before(model.TimeUTC(now.Add(-1*time.Millisecond))))
	require.False(t, activated.Session.MFACompletedAt.Time.After(model.NowUTC()))
	require.Len(t, activated.AccessTokenHashes, 1)
	require.Equal(t, model.HashToken(raw.access), activated.AccessTokenHashes[0])
	count, err := ss.MFA().CountRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, count, "CountRecoveryCodes()")
	// One time step for both calls: recomputing it could cross a 30-second
	// boundary between them and turn the replay into a fresh step.
	step := time.Now().Unix()/30 + 1
	require.NoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.ID, step, "", now.Add(1*time.Millisecond),
	))

	require.True(t, store.IsNotFound(ss.MFA().ConsumeSecondFactor(
		ctx, user.ID, step, "", now.Add(2*time.Millisecond),
	)), "replayed TOTP time step error")
	require.NoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.ID, 0, firstHash, now.Add(3*time.Millisecond),
	))

	require.True(t, store.IsNotFound(ss.MFA().ConsumeSecondFactor(
		ctx, user.ID, 0, firstHash, now.Add(4*time.Millisecond),
	)), "replayed recovery code error")
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count, "remaining recovery codes")
	replacementHash := model.HashToken(model.NewCredentialToken())
	regenerationAudit, regenerationNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFARecoveryCodesRegenerated, now.Add(5*time.Millisecond))
	require.NoError(t, ss.MFA().ReplaceRecoveryCodes(ctx, &store.MFARecoveryCodesRegeneration{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		UserID: user.ID, RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: replacementHash}}, At: now.Add(5 * time.Millisecond),
		AuditEventID: regenerationAudit.ID, AuditAt: now.Add(5 * time.Millisecond), Notice: regenerationNotice,
	}))

	requireMFATransitionCommitted(t, ctx, ss, user.ID, regenerationAudit, model.MailTemplateIdentityMFARecoveryCodesRegenerated, model.MailDeliveryQueued)
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count, "replacement recovery codes")
	require.True(t, store.IsNotFound(ss.MFA().ConsumeSecondFactor(
		ctx, user.ID, 0, secondHash, now.Add(6*time.Millisecond),
	)), "superseded recovery code error")
	disableAudit, disableNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFADisabled, now.Add(7*time.Millisecond))
	disableNotice = suppressedMFASecurityNotice(t, disableNotice, model.TimeUTC(now.Add(7*time.Millisecond)))
	disabled, err := ss.MFA().Disable(ctx, &store.MFADisablement{Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour, UserID: user.ID, At: now.Add(7 * time.Millisecond),
		AuditEventID: disableAudit.ID, AuditAt: now.Add(7 * time.Millisecond), Notice: disableNotice})
	require.NoError(t, err)
	requireMFATransitionCommitted(t, ctx, ss, user.ID, disableAudit, model.MailTemplateIdentityMFADisabled, model.MailDeliverySuppressed)
	require.Len(t, disabled.AccessTokenHashes, 1)
	require.Equal(t, model.HashToken(raw.access), disabled.AccessTokenHashes[0])
	gotSession, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuthenticationSingleFactor, gotSession.AuthenticationStrength)
	require.False(t, gotSession.MFACompletedAt.Valid)
	_, err = ss.MFA().GetByUser(ctx, user.ID)
	require.True(t, store.IsNotFound(err), "GetByUser() after Disable error")
}

func requireMFATransitionCommitted(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, audit *model.AuditEvent, key model.MailTemplateKey, state model.MailDeliveryState) {
	t.Helper()
	completed, err := ss.Audit().Get(ctx, audit.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status, "%s audit status", key)
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 200})
	require.NoError(t, err)
	matched := 0
	for _, delivery := range deliveries {
		if delivery.TargetUserID != userID {
			continue
		}
		matched++
		require.Equal(t, state, delivery.State, "%s delivery = %#v", key, delivery)
		require.False(t, delivery.TargetInvitationID.IsValid(), "%s delivery = %#v", key, delivery)
		job, jobErr := ss.Job().Get(ctx, delivery.JobID)
		require.NoError(t, jobErr)
		require.Equal(t, model.JobTypeMailDeliver, job.Type, "%s job type", key)
		if state == model.MailDeliverySuppressed {
			require.Empty(t, delivery.EncryptedPayload, "disabled %s mail retained work: delivery=%#v job=%#v", key, delivery, job)
			require.Equal(t, model.JobStatusCanceled, job.Status, "disabled %s mail retained work: delivery=%#v job=%#v", key, delivery, job)
		}
	}
	require.Equal(t, 1, matched, "%s deliveries for user", key)
}

func suppressedMFASecurityNotice(t *testing.T, notice store.MFASecurityNotice, at time.Time) store.MFASecurityNotice {
	t.Helper()
	delivery := notice.Delivery.Clone()
	delivery.State = model.MailDeliverySuppressed
	delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	delivery.EncryptedPayload = nil
	delivery.UpdatedAt = model.TimeUTC(at)
	delivery.Revision = 1
	require.NoError(t, delivery.Validate())
	job, err := notice.Job.RequestCancellation(at)
	require.NoError(t, err)
	notice.Delivery, notice.Job = delivery, job
	return notice
}

func testMFAPendingReplacement(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first := savePendingMFA(t, ctx, ss, user.ID)
	second := savePendingMFA(t, ctx, ss, user.ID)
	require.NotEqual(t, second.ID, first.ID, "pending MFA setup was not replaced")
	current, err := ss.MFA().GetByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, current.ID, "GetByUser()")
}

func testMFARecoveryCodeConsumptionIsSerialized(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	codeHash := model.HashToken(model.NewCredentialToken())
	base := model.TimeUTC(pending.CreatedAt)
	audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, base.Add(1*time.Millisecond))
	_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: codeHash}}, SessionID: session.ID, At: model.TimeUTC(base.Add(1 * time.Millisecond)),
		AuditEventID: audit.ID, AuditAt: base.Add(1 * time.Millisecond), Notice: notice,
	})
	require.NoError(t, err)

	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := range contenders {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			<-start
			results <- ss.MFA().ConsumeSecondFactor(
				ctx,
				user.ID,
				0,
				codeHash,
				base.Add(time.Duration(2+offset)*time.Millisecond),
			)
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case store.IsNotFound(err):
			rejected++
		default:
			require.FailNow(t, fmt.Sprintf("concurrent ConsumeSecondFactor() error = %v", err))
		}
	}
	require.Equal(t, 1, successes, "successes")
	require.Equal(t, contenders-1, rejected, "successes")
}

func testMFATransitionMailAndAuditRollbackTogether(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	at := model.TimeUTC(pending.CreatedAt).Add(1 * time.Millisecond)
	_, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at)
	missingAuditID := model.NewAuditEventID()
	_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID, At: model.TimeUTC(at), AuditEventID: missingAuditID, AuditAt: at, Notice: notice,
	})
	require.Error(t, err, "Activate() without durable audit attempt succeeded")
	current, getErr := ss.MFA().GetByUser(ctx, user.ID)
	require.NoError(t, getErr)
	require.Equal(t, model.MFAStatePending, current.State)
	deliveries, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityMFAEnabled}, Limit: 200})
	require.NoError(t, listErr)
	for _, delivery := range deliveries {
		require.NotEqual(t, user.ID, delivery.TargetUserID, "failed activation persisted delivery %s", delivery.ID)
	}

	audit, replayNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at.Add(1*time.Millisecond))
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID, At: model.TimeUTC(at.Add(1 * time.Millisecond)), AuditEventID: audit.ID, AuditAt: at.Add(1 * time.Millisecond), Notice: replayNotice,
	})
	require.NoError(t, err)
	replayAudit, secondNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at.Add(2*time.Millisecond))
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaPrincipal(t, ctx, ss, raw.access), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID, At: model.TimeUTC(at.Add(2 * time.Millisecond)), AuditEventID: replayAudit.ID, AuditAt: at.Add(2 * time.Millisecond), Notice: secondNotice,
	})
	require.Error(t, err, "replayed activation succeeded")
	requireMFATransitionCommitted(t, ctx, ss, user.ID, audit, model.MailTemplateIdentityMFAEnabled, model.MailDeliveryQueued)
	replayAuditAfter, auditErr := ss.Audit().Get(ctx, replayAudit.ID)
	require.NoError(t, auditErr)
	require.Equal(t, model.AuditStatusAttempt, replayAuditAfter.Status, "replay audit status")
}

func mfaSecurityNoticeFixture(t *testing.T, ctx context.Context, ss store.Store, user *model.User, key model.MailTemplateKey, at time.Time) (*model.AuditEvent, store.MFASecurityNotice) {
	t.Helper()
	scopeID := model.NewID()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: "mfa.security_transition",
		Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "mfa-storetest"})
	require.NoError(t, err)
	when := model.TimeUTC(at)
	occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, key, model.JobTypeMailDeliver, when, when.Add(24*time.Hour))
	return audit, store.MFASecurityNotice{Occurrence: occurrence, Delivery: delivery, Job: job}
}

func savePendingMFA(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	userID model.UserID,
) *model.MFACredential {
	t.Helper()
	now := model.NowUTC()
	credential, err := ss.MFA().SeedPending(ctx, &model.MFACredential{
		UserID:           userID,
		State:            model.MFAStatePending,
		EncryptedSecret:  "encrypted-secret",
		EncryptionKeyID:  "0123456789abcdef0123456789abcdef",
		PendingExpiresAt: model.OptionalTimeFrom(now.Add(10 * time.Minute)),
		CreatedAt:        now,
	})
	require.NoError(t, err)
	return credential
}

func mfaPrincipal(t *testing.T, ctx context.Context, ss store.Store, rawAccess string) model.Principal {
	t.Helper()
	credential, session, err := ss.SessionCredential().GetSessionByTokenHash(ctx, model.HashToken(rawAccess), model.SessionCredentialAccess)
	require.NoError(t, err)
	return mfaSessionPrincipal(session, credential)
}
func mfaSessionPrincipal(session *model.Session, credential *model.SessionCredential) model.Principal {
	return model.Principal{UserID: session.UserID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credential.ID), CredentialType: model.CredentialSessionAccess, AuthenticationGeneration: session.AuthenticationGeneration, MFARecoveryRequired: session.MFARecoveryRequired, ClientType: session.ClientType, AuthenticationMethod: session.AuthenticationMethod, AuthenticationProviderID: session.AuthenticationProviderID, ExternalIdentityID: session.ExternalIdentityID, AuthenticationStrength: session.AuthenticationStrength, AuthenticatedAt: session.AuthenticatedAt, ReauthenticatedAt: session.ReauthenticatedAt, MFACompletedAt: session.MFACompletedAt}
}
