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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (fake *effectsFake) AttemptSuspendedByManager(context.Context, ManualSuspensionResult) error {
	fake.f.order = append(fake.f.order, "effect.kick")
	fake.kicked++
	return nil
}

func manualSuspensionResultFixture(t *testing.T, f *fixture, managerID model.UserID, revision int64,
	replayed bool,
) *store.ExamAttemptManualSuspensionResult {
	t.Helper()
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
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
	result, err := f.service.SuspendByManager(context.Background(), f.call, command)
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.manualSuspension
	if input == nil || input.ActorUserID != managerID || input.ExpectedAttemptRevision != 3 || input.PrivateReason != reason ||
		input.ManagerOverride || !input.SuspensionID.IsValid() || input.AuditEventID == "" ||
		result.Attempt.State != model.ExamAttemptSuspended || result.Suspension.Source != model.AttemptSuspensionSourceManual ||
		!result.ConnectionClosed || f.effects.kicked != 1 ||
		strings.Join(f.order, ",") != "manager.manage,sitting,audit,manager.get,suspend,effect.kick" {
		t.Fatalf("result=%#v input=%#v order=%v", result, input, f.order)
	}
	if f.audit.action != model.ActionExamSittingManage || f.audit.operation != store.ExamAttemptSuspendOperation ||
		strings.Contains(fmt.Sprintf("%#v", f.audit.values), reason) || strings.Contains(fmt.Sprintf("%#v", result), reason) {
		t.Fatalf("audit=%#v result=%#v", f.audit, result)
	}
	want, err := prepareManualSuspensionIdempotency(f.call, command)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, want)
}

func TestSuspendByManagerReplaySuppressesEffectAndPropagatesOverride(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	f.managerOverride = true
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	f.persistence.manualSuspensionResult = manualSuspensionResultFixture(t, f, managerID, 3, true)
	result, err := f.service.SuspendByManager(context.Background(), f.call, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "override kick",
		IdempotencyKey: "kick-key"})
	if err != nil || !result.Replayed || f.effects.kicked != 0 || !f.persistence.manualSuspension.ManagerOverride ||
		f.audit.action != model.ActionExamSittingManageOverride {
		t.Fatalf("result=%#v err=%v effects=%d input=%#v", result, err, f.effects.kicked, f.persistence.manualSuspension)
	}
}

func TestSuspendByManagerConcealsSelfAndValidatesBeforeAuthorization(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.attempt, _ = managerAttemptFixture(t, f, model.ExamAttemptActive, 3, "")
	_, err := f.service.SuspendByManager(context.Background(), f.call, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "self", IdempotencyKey: "kick-key"})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.not_found" || f.audit.failedCode != "exam.attempt.not_found" ||
		f.persistence.manualSuspension != nil || f.effects.kicked != 0 {
		t.Fatalf("self kick error=%v audit=%#v", err, f.audit)
	}
	for _, reason := range []string{"", " padded ", "\xff", strings.Repeat("é", model.AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		f := newFixture(t)
		useManagerCall(f)
		_, err := f.service.SuspendByManager(context.Background(), f.call, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
			SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: reason, IdempotencyKey: "kick-key"})
		if err == nil || len(f.order) != 0 {
			t.Fatalf("reason=%q error=%v order=%v", reason, err, f.order)
		}
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
	_, err := f.service.SuspendByManager(context.Background(), f.call, ManualSuspensionCommand{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, ExpectedAttemptRevision: 3, PrivateReason: "kick", IdempotencyKey: "kick-key"})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.unavailable" || f.effects.kicked != 0 {
		t.Fatalf("flag-linked manual outcome error = %v", err)
	}
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
	result, err := f.service.Reallow(context.Background(), f.call, ReallowCommand{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		AttemptID: f.attemptID, SuspensionID: suspensionID, ExpectedAttemptRevision: 2, PrivateReason: "candidate returned",
		IdempotencyKey: "reallow-key"})
	if err != nil || result.Suspension.Source != model.AttemptSuspensionSourceManual || f.effects.reallowed != 1 ||
		result.Suspension.ReallowedByUserID != managerID {
		t.Fatalf("Reallow(manual) = %#v, %v", result, err)
	}
}
