// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

func TestControlAllowanceSeparatesRenewalAndRecoveryAndSharesSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for range 40 {
		release, err := f.service.enterControl(ctx, f.invocation, false)
		require.NoError(t, err)
		release()
	}
	otherPrincipal := f.invocation.Principal()
	otherPrincipal.SessionID = model.NewSessionID()
	other := invocation.New(otherPrincipal, f.invocation.RequestMetadata())
	_, err := f.service.enterControl(ctx, other, false)
	require.Error(t, err, "Session replacement refilled control allowance")
	release, err := f.service.enterControl(ctx, other, true)
	require.NoError(t, err, "status load consumed renewal allowance")
	release()
	// A public status operation rejects before its Store can observe selectors.
	p := &recoveryStoreFake{ExamAttemptStore: f.persistence}
	f.service.deps.Persistence = p
	_, err = f.service.BrowserDeliveryReceipts(ctx, other, BrowserSourceQuery{Access: CandidateAccess{AttemptID: f.attemptID}, ParticipationID: model.NewAttemptParticipationID(), SourceSessionID: "00000000-0000-4000-8000-000000000001"}, 1, 32)
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "control overload reached persistence or lost its code")
	require.Equal(t, "exam.delivery.control_rate_limited", fault.Code(), "control overload reached persistence or lost its code")
	require.Empty(t, p.selector.SourceSessionID, "control overload reached persistence or lost its code")
	require.Equal(t, 0, p.reads, "control overload reached persistence or lost its code")
	// Preflight work shares recovery capacity and cannot consume renewal capacity.
	_, err = f.service.PrepareExamSecurityPreflight(ctx, other, PrepareSecurityPreflightCommand{})
	require.ErrorAs(t, err, &fault, "preflight preparation bypassed admission")
	require.Equal(t, "exam.delivery.control_rate_limited", fault.Code(), "preflight preparation bypassed admission")
	_, err = f.service.ReportExamSecurityPreflight(ctx, other, ReportSecurityPreflightCommand{})
	require.ErrorAs(t, err, &fault, "preflight report bypassed admission")
	require.Equal(t, "exam.delivery.control_rate_limited", fault.Code(), "preflight report bypassed admission")
}

func TestControlAllowanceRefillsWithoutClockRollbackOrCancellationCredit(t *testing.T) {
	var allowance controlAdmission
	at := time.Date(2026, time.September, 8, 1, 0, 0, 0, time.UTC)
	user := model.NewUserID()
	for range 40 {
		release, err := allowance.enter(context.Background(), user, at)
		require.NoError(t, err)
		release()
	}
	_, err := allowance.enter(context.Background(), user, at.Add(-time.Hour))
	require.Error(t, err, "clock rollback refilled allowance")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = allowance.enter(ctx, user, at.Add(50*time.Millisecond))
	require.ErrorIs(t, err, context.Canceled, "canceled work entered")
	release, err := allowance.enter(context.Background(), user, at.Add(50*time.Millisecond))
	require.NoError(t, err, "canceled work consumed refill")
	release()
	_, err = allowance.enter(context.Background(), user, at.Add(50*time.Millisecond))
	require.Error(t, err, "refill exceeded 20 requests per second")
}

func TestControlWorkerAndIdentityStorageBounds(t *testing.T) {
	var allowance controlAdmission
	at := time.Now()
	var wg sync.WaitGroup
	entered := make(chan func(), controlWorkers)
	for range controlWorkers {
		wg.Go(func() {
			release, err := allowance.enter(context.Background(), model.NewUserID(), at)
			if !assert.NoError(t, err) {
				return
			}
			entered <- release
		})
	}
	wg.Wait()
	close(entered)
	_, err := allowance.enter(context.Background(), model.NewUserID(), at)
	require.Error(t, err, "unbounded control workers")
	for release := range entered {
		release()
	}
	for len(allowance.users) < controlUsers {
		release, err := allowance.enter(context.Background(), model.NewUserID(), at)
		require.NoError(t, err)
		release()
	}
	_, err = allowance.enter(context.Background(), model.NewUserID(), at)
	require.Error(t, err, "unbounded control identities")
	require.Len(t, allowance.users, controlUsers, "unbounded control identities")
	release, err := allowance.enter(context.Background(), model.NewUserID(), at.Add(time.Minute))
	require.NoError(t, err, "idle slot not reclaimed")
	release()
	require.Len(t, allowance.users, controlUsers, "idle reclamation changed hard bound")
}

func TestLiveRenewalsBeyondWorkerCapQueueInsteadOfRefusing(t *testing.T) {
	f := newFixture(t)
	live := &f.service.liveControls
	const renewals = 4 * controlWorkers
	at := time.Now()
	var (
		mu      sync.Mutex
		active  int
		peak    int
		refused []error
		wg      sync.WaitGroup
	)
	start := make(chan struct{})
	for range renewals {
		wg.Go(func() {
			<-start
			release, err := live.enter(context.Background(), model.NewUserID(), at)
			if err != nil {
				mu.Lock()
				refused = append(refused, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			release()
		})
	}
	close(start)
	wg.Wait()
	require.Empty(t, refused)
	require.LessOrEqual(t, peak, controlWorkers, "peak concurrent renewal work")
}

func TestQueuedControlAdmissionIsBoundedAndRefundsAllowance(t *testing.T) {
	allowance := controlAdmission{lane: controlLane{workers: 1, waiters: 1, wait: 20 * time.Millisecond, admitUntracked: true}}
	at := time.Now()
	holder, err := allowance.enter(context.Background(), model.NewUserID(), at)
	require.NoError(t, err)
	user := model.NewUserID()
	waited := make(chan error, 1)
	go func() {
		_, waitErr := allowance.enter(context.Background(), user, at)
		waited <- waitErr
	}()
	for {
		allowance.mu.Lock()
		waiting := allowance.waiting
		allowance.mu.Unlock()
		if waiting == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	var fault *apperror.Error
	_, err = allowance.enter(context.Background(), model.NewUserID(), at)
	require.ErrorAs(t, err, &fault, "queue length is unbounded")
	require.Equal(t, "exam.delivery.control_rate_limited", fault.Code(), "queue length is unbounded")
	err = <-waited
	require.ErrorAs(t, err, &fault, "queued work waited past its deadline")
	require.Equal(t, "exam.delivery.control_rate_limited", fault.Code(), "queued work waited past its deadline")
	allowance.mu.Lock()
	tokens := allowance.users[user].tokens
	allowance.mu.Unlock()
	require.Equal(t, int64(controlBurst), tokens, "timed-out wait consumed allowance")
	holder()
	release, err := allowance.enter(context.Background(), user, at)
	require.NoError(t, err, "released worker was not reusable")
	release()
	release()
	require.Empty(t, allowance.slots, "repeated release freed an extra worker")
}

func TestLiveControlAdmitsNewUsersWhenIdentityTableIsFull(t *testing.T) {
	live := controlAdmission{lane: liveControlLane}
	delivery := controlAdmission{}
	at := time.Now()
	for _, allowance := range []*controlAdmission{&live, &delivery} {
		for range controlUsers {
			release, err := allowance.enter(context.Background(), model.NewUserID(), at)
			require.NoError(t, err)
			release()
		}
	}
	release, err := live.enter(context.Background(), model.NewUserID(), at)
	require.NoError(t, err, "renewal refused only because other Users renewed recently")
	release()
	require.Len(t, live.users, controlUsers, "untracked admission grew the identity table")
	_, err = delivery.enter(context.Background(), model.NewUserID(), at)
	require.Error(t, err, "recovery lane stopped bounding identities")
}
