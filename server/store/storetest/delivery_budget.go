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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDeliveryBudgetStore(t *testing.T, ss store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, connected, access := newBrowserActivityFixture(t, ctx, ss, "delivery-budget")
	source := browserSourceID(8001)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
	_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, f.revisionID)}})
	requireNoError(t, err)
	query := store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID}
	before, err := ss.ExamAttempt().DeliveryBudget(ctx, query)
	requireNoError(t, err)
	if before.Validate() != nil || before.Browser.Participation.RetainedRecords != 1 || before.Browser.Attempt.AllocatedPositions != 2 || before.Browser.PendingBytes == 0 || before.ControlMetadataBytes != model.NativeOwnerReservationBytes+model.BrowserOwnerReservationBytes {
		t.Fatalf("persisted delivery budget=%#v", before)
	}
	foreign := query
	foreign.Access.CandidateUserID = model.NewUserID()
	if _, err := ss.ExamAttempt().DeliveryBudget(ctx, foreign); !store.IsNotFound(err) {
		t.Fatalf("foreign quota access: %v", err)
	}
	stop := &store.DeliveryDetailsStop{Access: access, Request: model.StopDeliveryDetails{Family: "browser", Reason: model.DeliveryStopLocalLossInventory}, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	key := examCommand(f.candidate.ID, store.DeliveryStopDetailsOperation, "stop-browser", "stop-browser")
	if _, err := ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key); err == nil {
		t.Fatal("stop committed without audit")
	}
	unchanged, err := ss.ExamAttempt().DeliveryBudget(ctx, query)
	requireNoError(t, err)
	if unchanged.Browser.Participation.SummaryOnly {
		t.Fatal("missing audit retained quota latch")
	}
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	result, err := ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key)
	requireNoError(t, err)
	if result.Validate("browser") != nil || result.Budget.Native.Participation.SummaryOnly || result.Budget.Browser.Attempt.SummaryOnly || len(result.Browser) != 1 || result.Browser[0].HighestContiguous != 0 || result.Browser[0].TerminalMissingThrough != 2 || result.Browser[0].SettledThrough != 2 || result.Budget.Browser.PendingBytes != 0 {
		t.Fatalf("family cutoff=%#v", result)
	}
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	replay, err := ss.ExamAttempt().StopDeliveryDetails(ctx, stop, key)
	requireNoError(t, err)
	if replay.Budget.Browser.Attempt.RetainedBytes != before.Browser.Attempt.RetainedBytes || replay.Budget.ControlMetadataBytes != before.ControlMetadataBytes || replay.Budget.Browser.Attempt.AllocatedPositions != before.Browser.Attempt.AllocatedPositions {
		t.Fatal("stop/retry spent or refunded quota")
	}
	// A recorded event remains receipt-recoverable after detail collection stops.
	receipts, err := ss.ExamAttempt().BrowserDeliveryReceipts(ctx, store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}, 1, 64)
	requireNoError(t, err)
	if len(receipts.Receipts) != 1 || receipts.Receipts[0].Sequence != 2 {
		t.Fatal("cutoff fabricated or erased receipt")
	}
	stop.Request.Family = "native"
	stop.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	result, err = ss.ExamAttempt().StopDeliveryDetails(ctx, stop, examCommand(f.candidate.ID, store.DeliveryStopDetailsOperation, "stop-native", "stop-native"))
	requireNoError(t, err)
	if result.Validate("native") != nil || result.Budget.Native.Attempt.SummaryOnly {
		t.Fatal("Participation stop exhausted the whole Attempt")
	}
	// Neither family cutoff changes live security or Submission eligibility.
	seal := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}
	if _, err := ss.ExamSubmission().ResolveSealTarget(ctx, seal); err != nil {
		t.Fatalf("historical detail stop denied Submission: %v", err)
	}
}
