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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
	f.auditID = input.AuditEventID.String()
	return f.err == nil, f.err
}
func TestDeliveryExpiryScanAuditsBeforeCommitAndRejectsUnboundedWork(t *testing.T) {
	f := newFixture(t)
	due := expiryDueFixture(f)
	persistence := &expiryDeliveryStoreFake{ExamAttemptStore: f.persistence, due: []store.DeliveryExpiryDue{{Family: "native", SourceID: model.NewID(), AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, SittingID: due.SittingID, ClassID: due.ClassID}}}
	f.service.deps.Persistence = persistence
	result, err := f.service.ScanExpiredDeliveries(context.Background(), expiryScanSystem, 1)
	require.NoError(t, err, "scan=%#v calls=%d order=%v", result, persistence.calls, f.order)
	require.Equal(t, 1, result.Completed, "scan=%#v calls=%d order=%v", result, persistence.calls, f.order)
	require.Equal(t, 1, persistence.calls, "scan=%#v calls=%d order=%v", result, persistence.calls, f.order)
	require.True(t, model.AuditEventID(persistence.auditID).IsValid(), "scan=%#v calls=%d order=%v", result, persistence.calls, f.order)
	// The commit carries the begun audit event, so the audit preceded it.
	require.NotNil(t, f.systemAudit.values, "the expiry was not audited")
	require.Empty(t, f.systemAudit.failedCode)
	for _, limit := range []int{0, 201} {
		_, err = f.service.ScanExpiredDeliveries(context.Background(), expiryScanSystem, limit)
		require.Error(t, err, "unbounded scan accepted")
	}
	persistence.due = append(persistence.due, persistence.due[0])
	_, err = f.service.ScanExpiredDeliveries(context.Background(), expiryScanSystem, 1)
	require.Error(t, err, "oversized persisted page mutated state")
	require.Equal(t, 1, persistence.calls, "oversized persisted page mutated state")
	persistence.due = persistence.due[:1]
	persistence.err = errors.New("database failure")
	_, err = f.service.ScanExpiredDeliveries(context.Background(), expiryScanSystem, 1)
	require.Error(t, err, "lost expiry commit reported success")
}

var expiryScanSystem = invocation.Task("exam-attempt-expiry-test")

func TestExpiryScansRequireTheirSystemIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.ScanExpiredParticipations(context.Background(), invocation.System{}, 1)
	require.Equal(t, "exam.attempt.invalid", apperror.CodeOf(err, ""), "Participation scan without its runtime task")
	_, err = f.service.ScanExpiredDeliveries(context.Background(), invocation.System{}, 1)
	require.Equal(t, "exam.attempt.invalid", apperror.CodeOf(err, ""), "delivery scan without its runtime task")
}
