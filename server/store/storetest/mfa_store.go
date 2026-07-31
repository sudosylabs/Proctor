// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
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
}

func testMFALifecycleAndSessionAssurance(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, raw := saveSession(t, ctx, ss, user.Id, 10)
	pending := savePendingMFA(t, ctx, ss, user.Id)
	firstHash := model.HashToken(model.NewCredentialToken())
	secondHash := model.HashToken(model.NewCredentialToken())
	now := pending.CreateAt + 1
	activated, err := ss.MFA().Activate(
		ctx,
		pending.Id,
		user.Id,
		1_000,
		[]*model.MFARecoveryCode{
			{CodeHash: firstHash},
			{CodeHash: secondHash},
		},
		session.Id,
		now,
	)
	requireNoError(t, err)
	if !activated.Credential.IsActive() ||
		activated.Session.AuthenticationStrength != model.AuthenticationMultiFactor ||
		activated.Session.MFACompletedAt != now ||
		len(activated.AccessTokenHashes) != 1 ||
		activated.AccessTokenHashes[0] != model.HashToken(raw.access) {
		t.Fatalf("Activate() = %#v", activated)
	}
	count, err := ss.MFA().CountRecoveryCodes(ctx, user.Id)
	requireNoError(t, err)
	if count != 2 {
		t.Fatalf("CountRecoveryCodes() = %d, want 2", count)
	}
	requireNoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.Id, 1_001, "", now+1,
	))
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.Id, 1_001, "", now+2,
	); !store.IsNotFound(err) {
		t.Fatalf("replayed TOTP time step error = %v, want not found", err)
	}
	requireNoError(t, ss.MFA().ConsumeSecondFactor(
		ctx, user.Id, 0, firstHash, now+3,
	))
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.Id, 0, firstHash, now+4,
	); !store.IsNotFound(err) {
		t.Fatalf("replayed recovery code error = %v, want not found", err)
	}
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.Id)
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("remaining recovery codes = %d, want 1", count)
	}
	replacementHash := model.HashToken(model.NewCredentialToken())
	requireNoError(t, ss.MFA().ReplaceRecoveryCodes(
		ctx,
		user.Id,
		[]*model.MFARecoveryCode{{CodeHash: replacementHash}},
		now+5,
	))
	count, err = ss.MFA().CountRecoveryCodes(ctx, user.Id)
	requireNoError(t, err)
	if count != 1 {
		t.Fatalf("replacement recovery codes = %d, want 1", count)
	}
	if err := ss.MFA().ConsumeSecondFactor(
		ctx, user.Id, 0, secondHash, now+6,
	); !store.IsNotFound(err) {
		t.Fatalf("superseded recovery code error = %v, want not found", err)
	}
	disabled, err := ss.MFA().Disable(ctx, user.Id, now+7)
	requireNoError(t, err)
	if len(disabled.AccessTokenHashes) != 1 ||
		disabled.AccessTokenHashes[0] != model.HashToken(raw.access) {
		t.Fatalf("Disable() = %#v", disabled)
	}
	gotSession, err := ss.Session().Get(ctx, session.Id)
	requireNoError(t, err)
	if gotSession.AuthenticationStrength != model.AuthenticationSingleFactor ||
		gotSession.MFACompletedAt != 0 {
		t.Fatalf("disabled session assurance = %#v", gotSession)
	}
	if _, err := ss.MFA().GetByUser(ctx, user.Id); !store.IsNotFound(err) {
		t.Fatalf("GetByUser() after Disable error = %v, want not found", err)
	}
}

func testMFAPendingReplacement(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first := savePendingMFA(t, ctx, ss, user.Id)
	second := savePendingMFA(t, ctx, ss, user.Id)
	if first.Id == second.Id {
		t.Fatal("pending MFA setup was not replaced")
	}
	current, err := ss.MFA().GetByUser(ctx, user.Id)
	requireNoError(t, err)
	if current.Id != second.Id {
		t.Fatalf("GetByUser() = %s, want %s", current.Id, second.Id)
	}
}

func testMFARecoveryCodeConsumptionIsSerialized(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, _, _ := saveSession(t, ctx, ss, user.Id, 10)
	pending := savePendingMFA(t, ctx, ss, user.Id)
	codeHash := model.HashToken(model.NewCredentialToken())
	_, err := ss.MFA().Activate(
		ctx,
		pending.Id,
		user.Id,
		2_000,
		[]*model.MFARecoveryCode{{CodeHash: codeHash}},
		session.Id,
		pending.CreateAt+1,
	)
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
				user.Id,
				0,
				codeHash,
				pending.CreateAt+2+int64(offset),
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

func savePendingMFA(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	userID string,
) *model.MFACredential {
	t.Helper()
	credential, err := ss.MFA().SavePending(ctx, &model.MFACredential{
		UserId:           userID,
		State:            model.MFAStatePending,
		EncryptedSecret:  "encrypted-secret",
		EncryptionKeyId:  "0123456789abcdef",
		PendingExpiresAt: model.GetMillis() + (10 * time.Minute).Milliseconds(),
	})
	requireNoError(t, err)
	return credential
}
