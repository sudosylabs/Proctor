// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestExamAttemptManualSuspensionStore verifies an Exam Manager's kick: one
// revision-fenced manual episode without an Integrity Flag that ends the live
// generation and Connection as kicked, replays exactly, conceals self-access,
// and is closed by the ordinary re-allow decision.
func TestExamAttemptManualSuspensionStore(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis(),
	}
	prepareExamAttemptConnect(t, ctx, ss, input)
	connected, err := ss.ExamAttempt().Connect(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "kick-connect", "kick-connect"))
	requireNoError(t, err)
	privateReason := "candidate left the examination room"
	kick := func(actor model.UserID, override bool, revision int64) *store.ExamAttemptManualSuspension {
		return &store.ExamAttemptManualSuspension{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			AttemptID: connected.Attempt.ID, SuspensionID: model.NewAttemptSuspensionID(), ActorUserID: actor,
			ManagerOverride: override, ExpectedAttemptRevision: revision, PrivateReason: privateReason,
			AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	}

	self := kick(fixture.candidate.ID, true, connected.Attempt.Revision)
	if result, selfErr := ss.ExamAttempt().SuspendAttempt(ctx, self,
		examCommand(fixture.candidate.ID, store.ExamAttemptSuspendOperation, "kick-self", "kick-self")); result != nil || !store.IsNotFound(selfErr) {
		t.Fatalf("SuspendAttempt(self) = %#v, %v; want concealed not found", result, selfErr)
	}
	stale := kick(fixture.manager.ID, false, connected.Attempt.Revision+1)
	_, err = ss.ExamAttempt().SuspendAttempt(ctx, stale,
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-stale", "kick-stale"))
	assertExamAttemptConflict(t, err, "exam_attempt_revision")

	first := kick(fixture.manager.ID, false, connected.Attempt.Revision)
	command := examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick", "kick")
	kicked, err := ss.ExamAttempt().SuspendAttempt(ctx, first, command)
	requireNoError(t, err)
	if kicked.Replayed || kicked.Attempt == nil || kicked.Attempt.State != model.ExamAttemptSuspended ||
		kicked.Attempt.Revision != connected.Attempt.Revision+1 || kicked.CandidateUserID != fixture.candidate.ID ||
		kicked.Participation == nil || kicked.Participation.ID != connected.Participation.ID ||
		kicked.Participation.EndReason != model.AttemptParticipationEndKicked || !kicked.ConnectionClosed ||
		kicked.Connection == nil || kicked.Connection.ID != connected.Connection.ID ||
		kicked.Connection.CloseReason != model.AttemptConnectionCloseKicked || kicked.Suspension == nil ||
		kicked.Suspension.ID != first.SuspensionID || kicked.Suspension.Source != model.AttemptSuspensionSourceManual ||
		kicked.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonManagerSuspended ||
		!kicked.Suspension.FlagID.IsZero() || kicked.Suspension.SuspendedByUserID != fixture.manager.ID ||
		!kicked.Attempt.UpdatedAt.Equal(kicked.Suspension.StartedAt) {
		t.Fatalf("SuspendAttempt() = %#v", kicked)
	}
	requireSuccessfulAudit(t, ctx, ss, first.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, first.AuditEventID, privateReason)

	snapshot, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	requireNoError(t, err)
	if snapshot.ActiveSuspension == nil || snapshot.ActiveSuspension.ID != first.SuspensionID ||
		snapshot.ActiveSuspension.Source != model.AttemptSuspensionSourceManual ||
		snapshot.ActiveSuspension.SuspendedByUserID != fixture.manager.ID || snapshot.CurrentConnection != nil {
		t.Fatalf("Get(kicked) = %#v", snapshot)
	}
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID,
		ReallowAuthorized: true, Limit: 50})
	requireNoError(t, err)
	if len(statuses.Items) != 1 || statuses.Items[0].Suspension == nil ||
		statuses.Items[0].Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonManagerSuspended ||
		!statuses.Items[0].Suspension.ReallowAvailable || statuses.Items[0].Presence.State != store.SittingCandidateSuspended {
		t.Fatalf("candidate statuses after kick = %#v", statuses)
	}

	again := kick(fixture.manager.ID, false, kicked.Attempt.Revision)
	_, err = ss.ExamAttempt().SuspendAttempt(ctx, again,
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-again", "kick-again"))
	assertExamAttemptConflict(t, err, "exam_attempt_state")

	reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, SuspensionID: first.SuspensionID,
		ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: kicked.Attempt.Revision, PrivateReason: "candidate returned",
		AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "kick-reallow", "kick-reallow"))
	requireNoError(t, err)
	if reallowed.Attempt.State != model.ExamAttemptReady || reallowed.Suspension.State != model.AttemptSuspensionClosed ||
		reallowed.Suspension.Source != model.AttemptSuspensionSourceManual || reallowed.FocusLossWindowReset ||
		reallowed.Suspension.SuspendedByUserID != fixture.manager.ID {
		t.Fatalf("ReallowAttempt(manual) = %#v", reallowed)
	}

	// Exact replay returns the original kick even after the episode closed.
	replay := *first
	replay.SuspensionID = model.NewAttemptSuspensionID()
	replay.AuditEventID, replay.AuditAt = saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, model.GetMillis()
	replayed, err := ss.ExamAttempt().SuspendAttempt(ctx, &replay, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Suspension.ID != first.SuspensionID || replayed.Attempt.State != model.ExamAttemptSuspended ||
		replayed.Attempt.Revision != kicked.Attempt.Revision || !replayed.ConnectionClosed {
		t.Fatalf("SuspendAttempt(replay) = %#v", replayed)
	}
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	var conflict *store.ErrConflict
	if _, err = ss.ExamAttempt().SuspendAttempt(ctx, kick(fixture.manager.ID, false, reallowed.Attempt.Revision),
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-ready", "kick-ready")); !errors.As(err, &conflict) ||
		conflict.Constraint != "exam_attempt_state" {
		t.Fatalf("SuspendAttempt(ready) error = %v, want exam_attempt_state", err)
	}
}
