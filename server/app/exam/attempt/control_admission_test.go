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
	"github.com/sudosylabs/proctor/server/model"
	"sync"
	"testing"
	"time"
)

func TestControlAllowanceSeparatesRenewalAndRecoveryAndSharesSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for range 40 {
		release, err := f.service.enterControl(ctx, f.call, false)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	otherPrincipal := f.call.Principal()
	otherPrincipal.SessionID = model.NewSessionID()
	other := NewCall(otherPrincipal, f.call.RequestMetadata())
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
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.delivery.control_rate_limited" || p.selector.SourceSessionID != "" || p.reads != 0 {
		t.Fatal("control overload reached persistence or lost its code", err)
	}
	// Preflight work shares recovery capacity and cannot consume renewal capacity.
	_, err = f.service.PrepareSecurityPreflight(ctx, other, PrepareSecurityPreflightCommand{})
	if !errors.As(err, &fault) || fault.Code != "exam.delivery.control_rate_limited" {
		t.Fatal("preflight preparation bypassed admission", err)
	}
	_, err = f.service.ReportSecurityPreflight(ctx, other, ReportSecurityPreflightCommand{})
	if !errors.As(err, &fault) || fault.Code != "exam.delivery.control_rate_limited" {
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
