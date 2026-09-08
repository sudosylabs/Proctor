// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"testing"
)

// ExecutionWorkspaceFixture supplies real admitted authority with an empty
// Workspace for tests that compose a host with the persistence conformance suite.
type ExecutionWorkspaceFixture struct {
	SetPaused func(bool) error
	Access    store.ExamAttemptWorkspaceMutationAccess
	NewAudit  func() string
	Command   func(string) *store.CommandIdempotency
}

func NewExecutionWorkspaceFixture(t *testing.T, ctx context.Context, ss store.Store) ExecutionWorkspaceFixture {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "host-connect", "host-connect"))
	requireNoError(t, err)
	result := ExecutionWorkspaceFixture{Access: store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: connect.ContinuityCredentialHash}}
	result.NewAudit = func() string { return saveExamAttemptAudit(t, ctx, ss, fixture).ID.String() }
	result.Command = func(key string) *store.CommandIdempotency {
		return examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, key, key)
	}
	snapshot, err := ss.ExecutionGrant().WorkspaceSnapshot(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	for _, node := range snapshot.Nodes {
		if node.Path != "cmd" {
			continue
		}
		_, err = ss.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{Access: result.Access, Operation: model.AttemptWorkspaceMutationDeleteEntry, EntryID: node.EntryID, ExpectedPath: node.Path, Recursive: true, ExpectedWorkspaceCursor: &snapshot.Cursor, AuditEventID: result.NewAudit(), AuditAt: model.GetMillis()}, result.Command("empty-fixture"))
		requireNoError(t, err)
	}
	result.SetPaused = func(paused bool) error {
		transition := &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision, PrivateReason: "host control integration", ChangedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()}
		if paused {
			value, err := ss.ExamSitting().Pause(ctx, transition, examCommand(fixture.manager.ID, "exam.sitting.pause.v1", "host-pause", "host-pause"))
			if err == nil {
				fixture.sitting = value.Value.Sitting
			}
			return err
		}
		value, err := ss.ExamSitting().Resume(ctx, transition, examCommand(fixture.manager.ID, "exam.sitting.resume.v1", "host-resume", "host-resume"))
		if err == nil {
			fixture.sitting = value.Value.Sitting
		}
		return err
	}
	return result
}
