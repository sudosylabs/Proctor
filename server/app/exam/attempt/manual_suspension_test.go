// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (f *effectsFake) AttemptSuspendedByManager(context.Context, ManualSuspensionResult) error {
	f.f.order = append(f.f.order, "effect.kick")
	f.kicked++
	return nil
}

func manualSuspensionResultFixture(t *testing.T, f *fixture, managerID model.UserID, revision int64,
	replayed bool,
) *store.ExamAttemptManualSuspensionResult {
	t.Helper()
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	require.NoError(t, err)
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, f.at, revision+1
	participationID := model.NewAttemptParticipationID()
	return &store.ExamAttemptManualSuspensionResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, Attempt: attempt,
		Participation: &store.ExamAttemptParticipationView{ID: participationID, AttemptID: f.attemptID,
			State: model.AttemptParticipationEnded, Generation: 1, StartedAt: f.at.Add(-time.Hour), UpdatedAt: f.at,
			LeaseExpiresAt: f.at.Add(time.Second), EndedAt: model.OptionalTimeFrom(f.at), EndReason: model.AttemptParticipationEndKicked},
		Connection: &store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
			OpenedAt: f.at.Add(-time.Hour), ClosedAt: model.OptionalTimeFrom(f.at), CloseReason: model.AttemptConnectionCloseKicked},
		ConnectionClosed: true,
		Suspension: &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: f.attemptID,
			ParticipationID: participationID, Generation: 1, State: model.AttemptSuspensionActive,
			Source: model.AttemptSuspensionSourceManual, CandidateReason: model.AttemptSuspensionCandidateReasonManagerSuspended,
			StartedAt: f.at, SuspendedByUserID: managerID},
		Replayed: replayed}
}

func TestSuspendByManagerAuthorizesFirstAndKeepsPrivateReasonOutOfAuditAndEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	f.persistence.manualSuspensionResult = manualSuspensionResultFixture(t, f, managerID, 3, false)
	reason := "candidate left the examination room"
	command := ManualSuspensionCommand{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID,
		ExpectedAttemptRevision: 3, PrivateReason: reason, IdempotencyKey: "kick-key"}
	result, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, command)
	require.NoError(t, err)
	input := f.persistence.manualSuspension
	require.NotNil(t, input, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, managerID, input.ActorUserID, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, int64(3), input.ExpectedRevision, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, reason, input.PrivateReason, "result=%#v input=%#v order=%v", result, input, f.order)
	require.False(t, input.ManagerOverride, "result=%#v input=%#v order=%v", result, input, f.order)
	require.True(t, input.SuspensionID.IsValid(), "result=%#v input=%#v order=%v", result, input, f.order)
	require.NotEmpty(t, input.AuditEventID, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, model.ExamAttemptSuspended, result.Attempt.State, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, model.AttemptSuspensionSourceManual, result.Suspension.Source, "result=%#v input=%#v order=%v", result, input, f.order)
	require.True(t, result.ConnectionClosed, "result=%#v input=%#v order=%v", result, input, f.order)
	require.Equal(t, 1, f.effects.kicked, "result=%#v input=%#v order=%v", result, input, f.order)
	// Contract: the Manager authorization precedes the audit, the audit begins
	// before the durable suspension (guardrail 8), and the suspension commits
	// before the kick (guardrail 14).
	require.Equal(t, []string{"manager.manage", "sitting", "audit", "manager.get", "suspend", "effect.kick"}, f.order)
	require.Equal(t, model.ActionExamSittingManage, f.audit.action, "audit=%#v result=%#v", f.audit, result)
	require.Equal(t, store.ExamAttemptSuspendOperation, f.audit.operation, "audit=%#v result=%#v", f.audit, result)
	require.NotContains(t, fmt.Sprintf("%#v", f.audit.values), reason, "audit=%#v result=%#v", f.audit, result)
	require.NotContains(t, fmt.Sprintf("%#v", result), reason, "audit=%#v result=%#v", f.audit, result)
	want, err := prepareManualSuspensionIdempotency(f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, want, f.persistence.idempotency)
}

func TestSuspendByManagerReplaySuppressesEffectAndPropagatesOverride(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	f.managerOverride = true
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	f.persistence.manualSuspensionResult = manualSuspensionResultFixture(t, f, managerID, 3, true)
	result, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "override kick",
		IdempotencyKey: "kick-key"})
	require.NoError(t, err, "result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
	require.True(t, result.Replayed, "result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
	require.Equal(t, 0, f.effects.kicked, "result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
	require.True(t, f.persistence.manualSuspension.ManagerOverride, "result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
	require.Equal(t, model.ActionExamSittingManageOverride, f.audit.action, "result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
}

func TestSuspendByManagerConcealsSelfAndValidatesBeforeAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "self", IdempotencyKey: "kick-key"})
	require.True(t, apperror.Is(err, "resource.not_found"), "self kick error=%v audit=%#v", err, f.audit)
	require.True(t, chainHasCode(err, "exam.attempt.not_found"), "self kick error=%v audit=%#v", err, f.audit)
	require.Equal(t, "exam.attempt.not_found", f.audit.failedCode, "self kick error=%v audit=%#v", err, f.audit)
	require.Nil(t, f.persistence.manualSuspension, "self kick error=%v audit=%#v", err, f.audit)
	require.Equal(t, 0, f.effects.kicked, "self kick error=%v audit=%#v", err, f.audit)
	for _, reason := range []string{"", " padded ", "\xff", strings.Repeat("é", model.AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		f := newFixture(t)
		useManagerCall(f)
		_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
			SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: reason, IdempotencyKey: "kick-key"})
		require.Error(t, err, "reason=%q error=%v order", reason, err)
		require.Empty(t, f.order, "reason=%q error=%v order", reason, err)
	}
}

func TestSuspendByManagerRejectsInconsistentStoreOutcome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	stored := manualSuspensionResultFixture(t, f, managerID, 3, false)
	stored.Suspension.FlagID = model.NewIntegrityFlagID()
	f.persistence.manualSuspensionResult = stored
	_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "kick", IdempotencyKey: "kick-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.attempt.unavailable", fault.Code())
	require.Equal(t, 0, f.effects.kicked)
}

func TestReallowAcceptsClosedManualSuspension(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	suspensionID := model.NewAttemptSuspensionID()
	attempt, active := managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2, suspensionID)
	active.Source, active.CandidateReason, active.FlagID = model.AttemptSuspensionSourceManual,
		model.AttemptSuspensionCandidateReasonManagerSuspended, ""
	active.SuspendedByUserID = model.NewUserID()
	f.persistence.attempt, f.persistence.managerSuspension = attempt, active
	stored := reallowResultFixtureWithReason(t, f, suspensionID, false, model.AttemptSuspensionCandidateReasonManagerSuspended)
	stored.Suspension.Source, stored.Suspension.FlagID = model.AttemptSuspensionSourceManual, ""
	stored.Suspension.SuspendedByUserID = active.SuspendedByUserID
	f.persistence.reallowResult = stored
	result, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		AttemptID: f.attemptID, SuspensionID: suspensionID, ExpectedAttemptRevision: 2, PrivateReason: "candidate returned",
		IdempotencyKey: "reallow-key"})
	require.NoError(t, err, "Reallow(manual) = %#v", result)
	require.Equal(t, model.AttemptSuspensionSourceManual, result.Suspension.Source, "Reallow(manual) = %#v", result)
	require.Equal(t, 1, f.effects.reallowed, "Reallow(manual) = %#v", result)
	require.Equal(t, managerID, result.Suspension.ReallowedByUserID, "Reallow(manual) = %#v", result)
}

func TestSuspendByManagerRecordsLapsedLeaseAsConnectionLossInsteadOfKick(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	useManagerCall(f)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	due := expiryDueFixture(f)
	f.persistence.latestParticipation = &store.ExamAttemptParticipationView{ID: due.ParticipationID, AttemptID: f.attemptID,
		State: model.AttemptParticipationActive, Generation: due.Generation, StartedAt: f.at.Add(-time.Hour), UpdatedAt: f.at.Add(-time.Minute),
		LeaseExpiresAt: due.LeaseExpiresAt}
	f.persistence.manualSuspensionErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	f.persistence.resolvedExpiry = &due
	f.persistence.expireResult = expiryResultFixture(t, f, due, false)
	_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3,
		PrivateReason: "candidate left the examination room", IdempotencyKey: "kick-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v order=%v", err, f.order)
	require.Equal(t, "exam.attempt.connection_lost", fault.Code(), "error=%v order=%v", err, f.order)
	require.Equal(t, fault.Code(), f.audit.failedCode, "error=%v order=%v", err, f.order)
	require.NotNil(t, f.persistence.expireInput, "the lapsed generation was not expired")
	require.Equal(t, due.ParticipationID, f.persistence.expireInput.ParticipationID)
	require.Equal(t, due.Generation, f.persistence.expireInput.Generation)
	require.Equal(t, 1, f.effects.expired, "error=%v order=%v", err, f.order)
	require.Equal(t, 0, f.effects.kicked, "error=%v order=%v", err, f.order)
	// Contract (guardrails 8 and 14): the refused kick stays audited as
	// failed, and the expiry audit begins before its commit and effect.
	require.Equal(t, []string{"manager.manage", "sitting", "audit", "manager.get", "suspend", "expiry.resolve", "system.audit", "expiry.commit", "effect.expire", "audit.fail"}, f.order)
}

func TestSuspendByManagerRefusesPastScheduledEnd(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	useManagerCall(f)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	f.persistence.manualSuspensionErr = store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3,
		PrivateReason: "candidate left the examination room", IdempotencyKey: "kick-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v order=%v", err, f.order)
	require.Equal(t, "exam.attempt.sitting_not_open", fault.Code(), "error=%v order=%v", err, f.order)
	require.Equal(t, fault.Code(), f.audit.failedCode)
	require.Nil(t, f.persistence.expireInput)
	require.Equal(t, 0, f.effects.kicked)
}
