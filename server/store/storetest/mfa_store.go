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
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMFAStore(t *testing.T, ss store.Store) {
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
}

func testMFALifecycleAndSessionAssurance(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	firstHash := model.HashToken(model.NewCredentialToken())
	secondHash := model.HashToken(model.NewCredentialToken())
	now := model.MillisFromTime(pending.CreatedAt) + 1
	activationAudit, activationNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, now)
	activated, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 1_000,
		RecoveryCodes: []*model.MFARecoveryCode{
			{CodeHash: firstHash},
			{CodeHash: secondHash},
		},
		SessionID: session.ID.String(), At: now, AuditEventID: activationAudit.ID.String(), AuditAt: now,
		Notice: activationNotice,
	})
	requireNoError(t, err)
	requireMFATransitionCommitted(t, ctx, ss, user.ID, activationAudit, model.MailTemplateIdentityMFAEnabled, model.MailDeliveryQueued)
	if !activated.Credential.IsActive() ||
		activated.Session.AuthenticationStrength != model.AuthenticationMultiFactor ||
		activated.Session.MFACompletedAt.Millis() != now ||
		len(activated.AccessTokenHashes) != 1 ||
		activated.AccessTokenHashes[0] != model.HashToken(raw.access) {
		t.Fatalf("Activate() = %#v", activated)
	}
	count, err := ss.MFA().CountRecoveryCodes(ctx, user.ID.String())
	requireNoError(t, err)
	if count != 2 {
		t.Fatalf("CountRecoveryCodes() = %d, want 2", count)
	}
	requireNoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.ID.String(), 1_001, "", now+1,
	))
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.ID.String(), 1_001, "", now+2,
	); !store.IsNotFound(err) {
		t.Fatalf("replayed TOTP time step error = %v, want not found", err)
	}
	requireNoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.ID.String(), 0, firstHash, now+3,
	))
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.ID.String(), 0, firstHash, now+4,
	); !store.IsNotFound(err) {
		t.Fatalf("replayed recovery code error = %v, want not found", err)
	}
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.ID.String())
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("remaining recovery codes = %d, want 1", count)
	}
	replacementHash := model.HashToken(model.NewCredentialToken())
	regenerationAudit, regenerationNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFARecoveryCodesRegenerated, now+5)
	requireNoError(t, ss.MFA().ReplaceRecoveryCodes(ctx, &store.MFARecoveryCodesRegeneration{
		UserID: user.ID.String(), RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: replacementHash}}, At: now + 5,
		AuditEventID: regenerationAudit.ID.String(), AuditAt: now + 5, Notice: regenerationNotice,
	}))
	requireMFATransitionCommitted(t, ctx, ss, user.ID, regenerationAudit, model.MailTemplateIdentityMFARecoveryCodesRegenerated, model.MailDeliveryQueued)
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.ID.String())
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("replacement recovery codes = %d, want 1", count)
	}
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.ID.String(), 0, secondHash, now+6,
	); !store.IsNotFound(err) {
		t.Fatalf("superseded recovery code error = %v, want not found", err)
	}
	disableAudit, disableNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFADisabled, now+7)
	disableNotice = suppressedMFASecurityNotice(t, disableNotice, model.TimeFromMillis(now+7))
	disabled, err := ss.MFA().Disable(ctx, &store.MFADisablement{UserID: user.ID.String(), At: now + 7,
		AuditEventID: disableAudit.ID.String(), AuditAt: now + 7, Notice: disableNotice})
	requireNoError(t, err)
	requireMFATransitionCommitted(t, ctx, ss, user.ID, disableAudit, model.MailTemplateIdentityMFADisabled, model.MailDeliverySuppressed)
	if len(disabled.AccessTokenHashes) != 1 ||
		disabled.AccessTokenHashes[0] != model.HashToken(raw.access) {
		t.Fatalf("Disable() = %#v", disabled)
	}
	gotSession, err := ss.Session().Get(ctx, session.ID.String())
	requireNoError(t, err)
	if gotSession.AuthenticationStrength != model.AuthenticationSingleFactor ||
		gotSession.MFACompletedAt.Valid {
		t.Fatalf("disabled session assurance = %#v", gotSession)
	}
	if _, err := ss.MFA().GetByUser(ctx, user.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("GetByUser() after Disable error = %v, want not found", err)
	}
}

func requireMFATransitionCommitted(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, audit *model.AuditEvent, key model.MailTemplateKey, state model.MailDeliveryState) {
	t.Helper()
	completed, err := ss.Audit().Get(ctx, audit.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("%s audit status = %s, want success", key, completed.Status)
	}
	deliveries, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{key}, Limit: 200})
	requireNoError(t, err)
	matched := 0
	for _, delivery := range deliveries {
		if delivery.TargetUserID != userID {
			continue
		}
		matched++
		if delivery.State != state || delivery.TargetInvitationID.IsValid() {
			t.Fatalf("%s delivery = %#v", key, delivery)
		}
		job, jobErr := ss.Job().Get(ctx, delivery.JobID)
		requireNoError(t, jobErr)
		if job.Type != model.JobTypeMailDeliver {
			t.Fatalf("%s job type = %s", key, job.Type)
		}
		if state == model.MailDeliverySuppressed && (len(delivery.EncryptedPayload) != 0 || job.Status != model.JobStatusCanceled) {
			t.Fatalf("disabled %s mail retained work: delivery=%#v job=%#v", key, delivery, job)
		}
	}
	if matched != 1 {
		t.Fatalf("%s deliveries for user = %d, want exactly 1", key, matched)
	}
}

func suppressedMFASecurityNotice(t *testing.T, notice store.MFASecurityNotice, at time.Time) store.MFASecurityNotice {
	t.Helper()
	delivery := notice.Delivery.Clone()
	delivery.State = model.MailDeliverySuppressed
	delivery.PublicFailureCode = model.MailDeliveryDisabledCode
	delivery.EncryptedPayload = nil
	delivery.UpdatedAt = model.TimeUTC(at)
	delivery.Revision = 1
	requireNoError(t, delivery.Validate())
	job, err := notice.Job.RequestCancellation(at)
	requireNoError(t, err)
	notice.Delivery, notice.Job = delivery, job
	return notice
}

func testMFAPendingReplacement(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first := savePendingMFA(t, ctx, ss, user.ID)
	second := savePendingMFA(t, ctx, ss, user.ID)
	if first.ID == second.ID {
		t.Fatal("pending MFA setup was not replaced")
	}
	current, err := ss.MFA().GetByUser(ctx, user.ID.String())
	requireNoError(t, err)
	if current.ID != second.ID {
		t.Fatalf("GetByUser() = %s, want %s", current.ID, second.ID)
	}
}

func testMFARecoveryCodeConsumptionIsSerialized(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	codeHash := model.HashToken(model.NewCredentialToken())
	base := model.MillisFromTime(pending.CreatedAt)
	audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, base+1)
	_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 2_000,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: codeHash}}, SessionID: session.ID.String(), At: base + 1,
		AuditEventID: audit.ID.String(), AuditAt: base + 1, Notice: notice,
	})
	requireNoError(t, err)

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
				user.ID.String(),
				0,
				codeHash,
				base+2+int64(offset),
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
			t.Fatalf("concurrent ConsumeSecondFactor() error = %v", err)
		}
	}
	if successes != 1 || rejected != contenders-1 {
		t.Fatalf("successes=%d rejected=%d", successes, rejected)
	}
}

func testMFATransitionMailAndAuditRollbackTogether(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	pending := savePendingMFA(t, ctx, ss, user.ID)
	at := model.MillisFromTime(pending.CreatedAt) + 1
	_, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at)
	missingAuditID := model.NewAuditEventID()
	_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 4_000,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID.String(), At: at, AuditEventID: missingAuditID.String(), AuditAt: at, Notice: notice,
	})
	if err == nil {
		t.Fatal("Activate() without durable audit attempt succeeded")
	}
	current, getErr := ss.MFA().GetByUser(ctx, user.ID.String())
	requireNoError(t, getErr)
	if current.State != model.MFAStatePending {
		t.Fatalf("credential state after rollback = %s", current.State)
	}
	deliveries, listErr := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityMFAEnabled}, Limit: 200})
	requireNoError(t, listErr)
	for _, delivery := range deliveries {
		if delivery.TargetUserID == user.ID {
			t.Fatalf("failed activation persisted delivery %s", delivery.ID)
		}
	}

	audit, replayNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at+1)
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 4_001,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID.String(), At: at + 1, AuditEventID: audit.ID.String(), AuditAt: at + 1, Notice: replayNotice,
	})
	requireNoError(t, err)
	replayAudit, secondNotice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at+2)
	_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 4_002,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
		SessionID:     session.ID.String(), At: at + 2, AuditEventID: replayAudit.ID.String(), AuditAt: at + 2, Notice: secondNotice,
	})
	if err == nil {
		t.Fatal("replayed activation succeeded")
	}
	requireMFATransitionCommitted(t, ctx, ss, user.ID, audit, model.MailTemplateIdentityMFAEnabled, model.MailDeliveryQueued)
	replayAuditAfter, auditErr := ss.Audit().Get(ctx, replayAudit.ID.String())
	requireNoError(t, auditErr)
	if replayAuditAfter.Status != model.AuditStatusAttempt {
		t.Fatalf("replay audit status = %s, want attempt for caller failure completion", replayAuditAfter.Status)
	}
}

func mfaSecurityNoticeFixture(t *testing.T, ctx context.Context, ss store.Store, user *model.User, key model.MailTemplateKey, at int64) (*model.AuditEvent, store.MFASecurityNotice) {
	t.Helper()
	scopeID := model.NewId()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: "mfa.security_transition",
		Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "mfa-storetest"})
	requireNoError(t, err)
	when := model.TimeFromMillis(at)
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
	credential, err := ss.MFA().SavePending(ctx, &model.MFACredential{
		UserID:           userID,
		State:            model.MFAStatePending,
		EncryptedSecret:  "encrypted-secret",
		EncryptionKeyID:  "0123456789abcdef0123456789abcdef",
		PendingExpiresAt: model.OptionalTimeFrom(now.Add(10 * time.Minute)),
		CreatedAt:        now,
	})
	requireNoError(t, err)
	return credential
}
