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
	"github.com/sudosylabs/proctor/server/store"
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
	release, err := allowance.enter(context.Background(), model.NewUserID(), at)
	require.NoError(t, err, "a full identity table refused a new User")
	release()
	require.Len(t, allowance.users, controlUsers, "unbounded control identities")
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
	lane := controlLane{workers: 1, waiters: 1, wait: 20 * time.Millisecond, interval: time.Second, burst: 2}
	allowance := controlAdmission{lane: lane}
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
	credit := allowance.users[user].Value.(*controlAllowance).credit
	allowance.mu.Unlock()
	require.Equal(t, lane.burst*lane.interval.Milliseconds(), credit, "timed-out wait consumed allowance")
	holder()
	release, err := allowance.enter(context.Background(), user, at)
	require.NoError(t, err, "released worker was not reusable")
	release()
	release()
	require.Empty(t, allowance.slots, "repeated release freed an extra worker")
}

func TestControlLanesEvictLeastRecentUserInsteadOfDisablingLimits(t *testing.T) {
	at := time.Now()
	for name, lane := range map[string]controlLane{"live": liveControlLane, "delivery": deliveryControlLane} {
		t.Run(name, func(t *testing.T) {
			allowance := controlAdmission{lane: lane}
			idle := model.NewUserID()
			release, err := allowance.enter(context.Background(), idle, at)
			require.NoError(t, err)
			release()
			for len(allowance.users) < controlUsers {
				release, err = allowance.enter(context.Background(), model.NewUserID(), at)
				require.NoError(t, err)
				release()
			}
			caller := model.NewUserID()
			for range lane.burst {
				release, err = allowance.enter(context.Background(), caller, at)
				require.NoError(t, err, "a new User was refused because other Users were recently active")
				release()
			}
			var fault *apperror.Error
			_, err = allowance.enter(context.Background(), caller, at)
			require.ErrorAs(t, err, &fault, "a full identity table stopped limiting a new User")
			require.Equal(t, "exam.delivery.control_rate_limited", fault.Code())
			require.Len(t, allowance.users, controlUsers, "tracking grew past its bound")
			require.Equal(t, controlUsers, allowance.recency.Len(), "recency index drifted from the table")
			require.NotContains(t, allowance.users, idle, "the least recently admitted User was not the one evicted")
		})
	}
}

func TestLiveControlAllowanceSustainsTwiceTheRenewalCadence(t *testing.T) {
	live := controlAdmission{lane: liveControlLane}
	at := time.Date(2026, time.September, 8, 1, 0, 0, 0, time.UTC)
	user := model.NewUserID()
	for range liveControlLane.burst {
		release, err := live.enter(context.Background(), user, at)
		require.NoError(t, err)
		release()
	}
	_, err := live.enter(context.Background(), user, at)
	require.Error(t, err, "live burst exceeded its bound")
	// Over one renewal interval a User regains exactly two requests.
	at = at.Add(model.AttemptParticipationRenewalInterval)
	for range 2 {
		release, enterErr := live.enter(context.Background(), user, at)
		require.NoError(t, enterErr, "an honest renewal cadence was refused")
		release()
	}
	_, err = live.enter(context.Background(), user, at)
	require.Error(t, err, "live refill exceeded twice the renewal cadence")
}

func TestLiveControlRejectsInvalidRenewalsBeforeAdmission(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	// More malformed requests than any burst: none may take a worker, a queue
	// place, or the caller's allowance.
	for range 2 * deliveryControlLane.burst {
		_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{
			AttemptID: f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
			Generation: 0, Sequence: 1, ContinuityCredential: model.NewCredentialToken(),
		})
		var fault *apperror.Error
		require.ErrorAs(t, err, &fault)
		require.Equal(t, "exam.attempt.invalid", fault.Code())
		_, err = f.service.UpdateExamSecurityCoverage(context.Background(), f.invocation, UpdateSecurityCoverageCommand{
			Access: CandidateAccess{AttemptID: f.attemptID}, ParticipationID: participationID, Generation: 0})
		require.Error(t, err)
		require.NotEqual(t, "exam.delivery.control_rate_limited", apperror.CodeOf(err, ""))
	}
	require.Empty(t, f.service.liveControls.users, "rejected requests were tracked by admission")
	require.Empty(t, f.service.liveControls.slots, "rejected requests held a worker")
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: renewalCoverageResultFixture(),
		AttemptID: f.attemptID, ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		CandidateUserID: f.invocation.Principal().UserID, ParticipationID: participationID, Generation: 3, AcceptedSequence: 7,
		DatabaseTime: f.at, LeaseExpiresAt: f.at.Add(model.AttemptParticipationInitialLease),
	}
	_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{
		SecurityCoverage: renewalCoverageFixture(), AttemptID: f.attemptID, ParticipationID: participationID,
		ConnectionID: f.connectionID, Generation: 3, Sequence: 7, ContinuityCredential: model.NewCredentialToken(),
	})
	require.NoError(t, err, "malformed renewals spent the caller's live allowance")
}
