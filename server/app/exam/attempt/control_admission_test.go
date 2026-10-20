// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

func TestControlAllowanceSeparatesRenewalAndRecoveryAndSharesSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for range 40 {
		release, err := f.service.enterControl(ctx, f.invocation, false)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	otherPrincipal := f.invocation.Principal()
	otherPrincipal.SessionID = model.NewSessionID()
	other := invocation.New(otherPrincipal, f.invocation.RequestMetadata())
	if _, err := f.service.enterControl(ctx, other, false); err == nil {
		t.Fatal("Session replacement refilled control allowance")
	}
	release, err := f.service.enterControl(ctx, other, true)
	if err != nil {
		t.Fatal("status load consumed renewal allowance", err)
	}
	release()
	// A public status operation rejects before its Store can observe selectors.
	p := &recoveryStoreFake{ExamAttemptStore: f.persistence}
	f.service.deps.Persistence = p
	_, err = f.service.BrowserDeliveryReceipts(ctx, other, BrowserSourceQuery{Access: CandidateAccess{AttemptID: f.attemptID}, ParticipationID: model.NewAttemptParticipationID(), SourceSessionID: "00000000-0000-4000-8000-000000000001"}, 1, 32)
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.delivery.control_rate_limited" || p.selector.SourceSessionID != "" || p.reads != 0 {
		t.Fatal("control overload reached persistence or lost its code", err)
	}
	// Preflight work shares recovery capacity and cannot consume renewal capacity.
	_, err = f.service.PrepareExamSecurityPreflight(ctx, other, PrepareSecurityPreflightCommand{})
	if !errors.As(err, &fault) || fault.Code() != "exam.delivery.control_rate_limited" {
		t.Fatal("preflight preparation bypassed admission", err)
	}
	_, err = f.service.ReportExamSecurityPreflight(ctx, other, ReportSecurityPreflightCommand{})
	if !errors.As(err, &fault) || fault.Code() != "exam.delivery.control_rate_limited" {
		t.Fatal("preflight report bypassed admission", err)
	}
}

func TestControlAllowanceRefillsWithoutClockRollbackOrCancellationCredit(t *testing.T) {
	var allowance controlAdmission
	at := time.Date(2026, time.September, 8, 1, 0, 0, 0, time.UTC)
	user := model.NewUserID()
	for range 40 {
		release, err := allowance.enter(context.Background(), user, at)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, err := allowance.enter(context.Background(), user, at.Add(-time.Hour)); err == nil {
		t.Fatal("clock rollback refilled allowance")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := allowance.enter(ctx, user, at.Add(50*time.Millisecond)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled work entered")
	}
	release, err := allowance.enter(context.Background(), user, at.Add(50*time.Millisecond))
	if err != nil {
		t.Fatal("canceled work consumed refill", err)
	}
	release()
	if _, err := allowance.enter(context.Background(), user, at.Add(50*time.Millisecond)); err == nil {
		t.Fatal("refill exceeded 20 requests per second")
	}
}

func TestControlWorkerAndIdentityStorageBounds(t *testing.T) {
	var allowance controlAdmission
	at := time.Now()
	var wg sync.WaitGroup
	entered := make(chan func(), controlWorkers)
	for range controlWorkers {
		wg.Go(func() {
			release, err := allowance.enter(context.Background(), model.NewUserID(), at)
			if err != nil {
				t.Error(err)
				return
			}
			entered <- release
		})
	}
	wg.Wait()
	close(entered)
	if _, err := allowance.enter(context.Background(), model.NewUserID(), at); err == nil {
		t.Fatal("unbounded control workers")
	}
	for release := range entered {
		release()
	}
	for len(allowance.users) < controlUsers {
		release, err := allowance.enter(context.Background(), model.NewUserID(), at)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, err := allowance.enter(context.Background(), model.NewUserID(), at); err == nil || len(allowance.users) != controlUsers {
		t.Fatal("unbounded control identities")
	}
	release, err := allowance.enter(context.Background(), model.NewUserID(), at.Add(time.Minute))
	if err != nil {
		t.Fatal("idle slot not reclaimed", err)
	}
	release()
	if len(allowance.users) != controlUsers {
		t.Fatal("idle reclamation changed hard bound")
	}
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
	if len(refused) != 0 {
		t.Fatalf("%d of %d concurrent renewals refused: %v", len(refused), renewals, refused[0])
	}
	if peak > controlWorkers {
		t.Fatalf("peak concurrent renewal work = %d, want at most %d", peak, controlWorkers)
	}
}

func TestQueuedControlAdmissionIsBoundedAndRefundsAllowance(t *testing.T) {
	allowance := controlAdmission{lane: controlLane{workers: 1, waiters: 1, wait: 20 * time.Millisecond, admitUntracked: true}}
	at := time.Now()
	holder, err := allowance.enter(context.Background(), model.NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err = allowance.enter(context.Background(), model.NewUserID(), at); !errors.As(err, &fault) ||
		fault.Code() != "exam.delivery.control_rate_limited" {
		t.Fatal("queue length is unbounded", err)
	}
	if err = <-waited; !errors.As(err, &fault) || fault.Code() != "exam.delivery.control_rate_limited" {
		t.Fatal("queued work waited past its deadline", err)
	}
	allowance.mu.Lock()
	tokens := allowance.users[user].tokens
	allowance.mu.Unlock()
	if tokens != controlBurst {
		t.Fatalf("timed-out wait consumed allowance: %d", tokens)
	}
	holder()
	release, err := allowance.enter(context.Background(), user, at)
	if err != nil {
		t.Fatal("released worker was not reusable", err)
	}
	release()
	release()
	if len(allowance.slots) != 0 {
		t.Fatal("repeated release freed an extra worker")
	}
}

func TestLiveControlAdmitsNewUsersWhenIdentityTableIsFull(t *testing.T) {
	live := controlAdmission{lane: liveControlLane}
	delivery := controlAdmission{}
	at := time.Now()
	for _, allowance := range []*controlAdmission{&live, &delivery} {
		for range controlUsers {
			release, err := allowance.enter(context.Background(), model.NewUserID(), at)
			if err != nil {
				t.Fatal(err)
			}
			release()
		}
	}
	release, err := live.enter(context.Background(), model.NewUserID(), at)
	if err != nil {
		t.Fatal("renewal refused only because other Users renewed recently", err)
	}
	release()
	if len(live.users) != controlUsers {
		t.Fatal("untracked admission grew the identity table")
	}
	if _, err = delivery.enter(context.Background(), model.NewUserID(), at); err == nil {
		t.Fatal("recovery lane stopped bounding identities")
	}
}
