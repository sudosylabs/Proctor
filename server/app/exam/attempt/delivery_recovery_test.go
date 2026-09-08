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
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type recoveryStoreFake struct {
	store.ExamAttemptStore
	refusal, statusError error
	status               *model.BrowserSourceStatus
	budget               *model.DeliveryBudgetSnapshot
	reads                int
	selector             store.BrowserDeliveryAccess
	budgetSelector       store.DeliveryBudgetAccess
}

func (f *recoveryStoreFake) BrowserDeliveryReceipts(_ context.Context, a store.BrowserDeliveryAccess, _ int64, _ int) (*model.BrowserReceiptPage, error) {
	f.selector = a
	return nil, f.refusal
}
func (f *recoveryStoreFake) BrowserSourceStatus(_ context.Context, a store.BrowserDeliveryAccess) (*model.BrowserSourceStatus, error) {
	f.reads++
	if a != f.selector {
		return nil, errors.New("ownership selector changed")
	}
	return f.status, f.statusError
}
func (f *recoveryStoreFake) DeliveryBudget(_ context.Context, a store.DeliveryBudgetAccess) (*model.DeliveryBudgetSnapshot, error) {
	f.reads++
	f.budgetSelector = a
	return f.budget, nil
}

func TestBrowserDeliveryRefusalRequiresFreshOwnedRecovery(t *testing.T) {
	for _, name := range []string{"current", "revoked", "wrong source", "wrong participation", "invalid counters", "unrelated error", "admission full"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			part := model.NewAttemptParticipationID()
			source := model.BrowserSourceSessionID("00000000-0000-4000-8000-000000000001")
			p := &recoveryStoreFake{ExamAttemptStore: f.persistence, refusal: store.NewErrConflict("browser_delivery", "declaration_conflict", nil)}
			p.status = &model.BrowserSourceStatus{SourceSessionID: source, AttemptID: f.attemptID, ParticipationID: part, Generation: 1, PolicyRevisionID: f.revision.ID, PolicyDigest: "sha256:" + strings.Repeat("a", 64), StartTransition: "initial", StartedAt: f.at, ServerTime: f.at, DetailMode: "collecting", BrowserDeliveryProgress: model.BrowserDeliveryProgress{MissingRanges: []model.SequenceRange{}}}
			p.budget = &model.DeliveryBudgetSnapshot{ParticipationID: part, Generation: 1, ServerTime: f.at, Native: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(true, false), Attempt: model.NewDeliveryQuotaUsage(true, true), PendingByteLimit: model.DeliveryPendingByteLimit}, Browser: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(false, false), Attempt: model.NewDeliveryQuotaUsage(false, true), PendingByteLimit: model.DeliveryPendingByteLimit}}
			switch name {
			case "revoked":
				p.statusError = store.NewErrNotFound("browser_delivery", string(source))
			case "wrong source":
				p.status.SourceSessionID = "00000000-0000-4000-8000-000000000002"
			case "wrong participation":
				p.budget.ParticipationID = model.NewAttemptParticipationID()
			case "invalid counters":
				p.budget.ControlMetadataBytes = model.DeliveryMetadataLimitBytes + 1
			case "admission full":
				p.refusal = store.NewErrConflict("browser_delivery", "append_rate_limited", store.ErrDeliveryAdmissionFull)
			case "unrelated error":
				p.refusal = store.NewErrNotFound("browser_delivery", string(source))
			}
			f.service.deps.Persistence = p
			_, err := f.service.BrowserDeliveryReceipts(context.Background(), f.call, BrowserSourceQuery{Access: CandidateAccess{AttemptID: f.attemptID}, SourceSessionID: source, ParticipationID: part}, 1, 32)
			var fault *Fault
			if !errors.As(err, &fault) {
				t.Fatalf("missing application fault: %v", err)
			}
			if (fault.Recovery != nil) != (name == "current") {
				t.Fatalf("recovery disclosure for %s: %#v", name, fault.Recovery)
			}
			if name == "current" {
				if fault.Recovery.Validate() != nil || p.reads != 2 || p.budgetSelector.ParticipationID != part || p.budgetSelector.Access != p.selector.Access || !errors.Is(err, p.refusal) {
					t.Fatal("recovery changed ownership, cause or bounds")
				}
			}
			if (name == "unrelated error" || name == "admission full") && p.reads != 0 {
				t.Fatal("unrelated failure caused recovery reads")
			}
		})
	}
}
