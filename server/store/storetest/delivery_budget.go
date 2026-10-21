// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryBudgetStore(t *testing.T, ss store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "delivery-budget")
	source := browserSourceID(8001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	require.NoError(t, err)
	query := store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID}
	before, err := ss.ExamAttempt().DeliveryBudget(ctx, query)
	require.NoError(t, err)
	require.NoError(t, before.Validate())
	require.Equal(t, int64(1), before.Browser.Participation.RetainedRecords)
	require.Equal(t, int64(2), before.Browser.Attempt.AllocatedPositions)
	require.NotEqual(t, int64(0), before.Browser.PendingBytes)
	require.Equal(t, model.NativeOwnerReservationBytes+model.BrowserOwnerReservationBytes, before.ControlMetadataBytes)
	foreign := query
	foreign.Access.CandidateUserID = model.NewUserID()
	_, err = ss.ExamAttempt().DeliveryBudget(ctx, foreign)
	require.True(t, store.IsNotFound(err), "foreign quota access")
	stop := &store.DeliveryDetailsStop{Access: access, Request: model.StopDeliveryDetails{Family: "browser", Reason: model.DeliveryStopLocalLossInventory}, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()}
	key := examCommand(f.candidate.ID, store.DeliveryStopDetailsOperation, "stop-browser", "stop-browser")
	_, err = ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key)
	require.Error(t, err, "stop committed without audit")
	unchanged, err := ss.ExamAttempt().DeliveryBudget(ctx, query)
	require.NoError(t, err)
	require.False(t, unchanged.Browser.Participation.SummaryOnly, "missing audit retained quota latch")
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
	result, err := ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key)
	require.NoError(t, err)
	require.NoError(t, result.Validate("browser"))
	require.False(t, result.Budget.Native.Participation.SummaryOnly)
	require.False(t, result.Budget.Browser.Attempt.SummaryOnly)
	require.Len(t, result.Browser, 1)
	require.Equal(t, int64(0), result.Browser[0].HighestContiguous)
	require.Equal(t, int64(2), result.Browser[0].TerminalMissingThrough)
	require.Equal(t, int64(2), result.Browser[0].SettledThrough)
	require.Equal(t, int64(0), result.Budget.Browser.PendingBytes)
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
	replay, err := ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key)
	require.NoError(t, err)
	require.Equal(t, before.Browser.Attempt.RetainedBytes, replay.Budget.Browser.Attempt.RetainedBytes, "stop/retry spent or refunded quota")
	require.Equal(t, before.ControlMetadataBytes, replay.Budget.ControlMetadataBytes, "stop/retry spent or refunded quota")
	require.Equal(t, before.Browser.Attempt.AllocatedPositions, replay.Budget.Browser.Attempt.AllocatedPositions, "stop/retry spent or refunded quota")
	// A recorded event remains receipt-recoverable after detail collection stops.
	receipts, err := ss.ExamAttempt().BrowserDeliveryReceipts(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}, 1, 64)
	require.NoError(t, err)
	require.Len(t, receipts.Receipts, 1, "cutoff fabricated or erased receipt")
	require.Equal(t, int64(2), receipts.Receipts[0].Sequence, "cutoff fabricated or erased receipt")
	stop.Request.Family = "native"
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
	result, err = ss.ExamAttempt().StopDeliveryDetails(ctx, stop, examCommand(f.candidate.ID, store.DeliveryStopDetailsOperation, "stop-native", "stop-native"))
	require.NoError(t, err)
	require.NoError(t, result.Validate("native"), "Participation stop exhausted the whole Attempt")
	require.False(t, result.Budget.Native.Attempt.SummaryOnly, "Participation stop exhausted the whole Attempt")
	// Neither family cutoff changes live security or Submission eligibility.
	seal := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}
	_, err = ss.ExamSubmission().ResolveSealTarget(ctx, seal)
	require.NoError(t, err)
}
