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

	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	privateReason := "candidate left the examination room"
	kick := func(actor model.UserID, override bool, revision int64) *store.ExamAttemptManualSuspension {
		return &store.ExamAttemptManualSuspension{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
			AttemptID: connected.Attempt.ID, SuspensionID: model.NewAttemptSuspensionID(), ActorUserID: actor,
			ManagerOverride: override, ExpectedAttemptRevision: revision, PrivateReason: privateReason,
			AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	}

	self := kick(fixture.candidate.ID, true, connected.Attempt.Revision)
	result, selfErr := ss.ExamAttempt().SuspendAttempt(ctx, self,
		examCommand(fixture.candidate.ID, store.ExamAttemptSuspendOperation, "kick-self", "kick-self"))
	require.Nil(t, result, "SuspendAttempt(self)")
	require.True(t, store.IsNotFound(selfErr), "SuspendAttempt(self)")
	stale := kick(fixture.manager.ID, false, connected.Attempt.Revision+1)
	_, err = ss.ExamAttempt().SuspendAttempt(ctx, stale,
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-stale", "kick-stale"))
	requireConflictConstraint(t, err, "exam_attempt_revision")

	first := kick(fixture.manager.ID, false, connected.Attempt.Revision)
	command := examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick", "kick")
	kicked, err := ss.ExamAttempt().SuspendAttempt(ctx, first, command)
	require.NoError(t, err)
	require.False(t, kicked.Replayed)
	require.NotNil(t, kicked.Attempt)
	require.Equal(t, model.ExamAttemptSuspended, kicked.Attempt.State)
	require.Equal(t, connected.Attempt.Revision+1, kicked.Attempt.Revision)
	require.Equal(t, fixture.candidate.ID, kicked.CandidateUserID)
	require.NotNil(t, kicked.Participation)
	require.Equal(t, connected.Participation.ID, kicked.Participation.ID)
	require.Equal(t, model.AttemptParticipationEndKicked, kicked.Participation.EndReason)
	require.True(t, kicked.ConnectionClosed)
	require.NotNil(t, kicked.Connection)
	require.Equal(t, connected.Connection.ID, kicked.Connection.ID)
	require.Equal(t, model.AttemptConnectionCloseKicked, kicked.Connection.CloseReason)
	require.NotNil(t, kicked.Suspension)
	require.Equal(t, first.SuspensionID, kicked.Suspension.ID)
	require.Equal(t, model.AttemptSuspensionSourceManual, kicked.Suspension.Source)
	require.Equal(t, model.AttemptSuspensionCandidateReasonManagerSuspended, kicked.Suspension.CandidateReason)
	require.True(t, kicked.Suspension.FlagID.IsZero())
	require.Equal(t, fixture.manager.ID, kicked.Suspension.SuspendedByUserID)
	require.True(t, kicked.Attempt.UpdatedAt.Equal(kicked.Suspension.StartedAt))
	requireSuccessfulAudit(t, ctx, ss, first.AuditEventID)
	assertReviewAuditPrivate(t, ctx, ss, first.AuditEventID, privateReason)

	snapshot, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot.ActiveSuspension)
	require.Equal(t, first.SuspensionID, snapshot.ActiveSuspension.ID)
	require.Equal(t, model.AttemptSuspensionSourceManual, snapshot.ActiveSuspension.Source)
	require.Equal(t, fixture.manager.ID, snapshot.ActiveSuspension.SuspendedByUserID)
	require.Nil(t, snapshot.CurrentConnection)
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID,
		ReallowAuthorized: true, Limit: 50})
	require.NoError(t, err)
	require.Len(t, statuses.Items, 1)
	require.NotNil(t, statuses.Items[0].Suspension)
	require.Equal(t, model.AttemptSuspensionCandidateReasonManagerSuspended, statuses.Items[0].Suspension.CandidateReason)
	require.True(t, statuses.Items[0].Suspension.ReallowAvailable)
	require.Equal(t, store.SittingCandidateSuspended, statuses.Items[0].Presence.State)

	again := kick(fixture.manager.ID, false, kicked.Attempt.Revision)
	_, err = ss.ExamAttempt().SuspendAttempt(ctx, again,
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-again", "kick-again"))
	requireConflictConstraint(t, err, "exam_attempt_state")

	reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, SuspensionID: first.SuspensionID,
		ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: kicked.Attempt.Revision, PrivateReason: "candidate returned",
		AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()},
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "kick-reallow", "kick-reallow"))
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptReady, reallowed.Attempt.State)
	require.Equal(t, model.AttemptSuspensionClosed, reallowed.Suspension.State)
	require.Equal(t, model.AttemptSuspensionSourceManual, reallowed.Suspension.Source)
	require.False(t, reallowed.FocusLossWindowReset)
	require.Equal(t, fixture.manager.ID, reallowed.Suspension.SuspendedByUserID)

	// Exact replay returns the original kick even after the episode closed.
	replay := *first
	replay.SuspensionID = model.NewAttemptSuspensionID()
	replay.AuditEventID, replay.AuditAt = saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID, model.GetMillis()
	replayed, err := ss.ExamAttempt().SuspendAttempt(ctx, &replay, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.SuspensionID, replayed.Suspension.ID)
	require.Equal(t, model.ExamAttemptSuspended, replayed.Attempt.State)
	require.Equal(t, kicked.Attempt.Revision, replayed.Attempt.Revision)
	require.True(t, replayed.ConnectionClosed)
	requireSuccessfulAudit(t, ctx, ss, replay.AuditEventID)
	var conflict *store.ErrConflict
	_, err = ss.ExamAttempt().SuspendAttempt(ctx, kick(fixture.manager.ID, false, reallowed.Attempt.Revision),
		examCommand(fixture.manager.ID, store.ExamAttemptSuspendOperation, "kick-ready", "kick-ready"))
	require.ErrorAs(t, err, &conflict, "SuspendAttempt(ready) error")
	require.Equal(t, "exam_attempt_state", conflict.Constraint, "SuspendAttempt(ready) error")
}
