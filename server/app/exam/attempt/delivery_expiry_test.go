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

type expiryDeliveryStoreFake struct {
	store.ExamAttemptStore
	due     []store.DeliveryExpiryDue
	calls   int
	auditID string
	err     error
}

func (f *expiryDeliveryStoreFake) ListExpiredDeliveries(context.Context, int) ([]store.DeliveryExpiryDue, error) {
	return f.due, nil
}
func (f *expiryDeliveryStoreFake) ExpireDelivery(_ context.Context, input *store.DeliveryExpiry) (bool, error) {
	f.calls++
	f.auditID = input.AuditEventID
	return f.err == nil, f.err
}
func TestDeliveryExpiryScanAuditsBeforeCommitAndRejectsUnboundedWork(t *testing.T) {
	f := newFixture(t)
	due := expiryDueFixture(f)
	persistence := &expiryDeliveryStoreFake{ExamAttemptStore: f.persistence, due: []store.DeliveryExpiryDue{{Family: "native", SourceID: model.NewId(), AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, SittingID: due.SittingID, ClassID: due.ClassID}}}
	f.service.deps.Persistence = persistence
	result, err := f.service.ScanExpiredDeliveries(context.Background(), 1)
	if err != nil || result.Completed != 1 || persistence.calls != 1 || !model.IsValidId(persistence.auditID) || strings.Join(f.order, ",") != "system.audit" {
		t.Fatalf("scan=%#v calls=%d order=%v: %v", result, persistence.calls, f.order, err)
	}
	for _, limit := range []int{0, 201} {
		if _, err := f.service.ScanExpiredDeliveries(context.Background(), limit); err == nil {
			t.Fatal("unbounded scan accepted")
		}
	}
	persistence.due = append(persistence.due, persistence.due[0])
	if _, err := f.service.ScanExpiredDeliveries(context.Background(), 1); err == nil || persistence.calls != 1 {
		t.Fatal("oversized persisted page mutated state")
	}
	persistence.due = persistence.due[:1]
	persistence.err = errors.New("database failure")
	if _, err := f.service.ScanExpiredDeliveries(context.Background(), 1); err == nil {
		t.Fatal("lost expiry commit reported success")
	}
}
