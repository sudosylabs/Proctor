// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testCorrectionCapabilityGates(t *testing.T, ss store.Store) {
	for caseIndex, test := range []struct {
		name                   string
		instructions, required bool
		selected               []model.CandidateCapability
	}{
		{"browser only", false, true, []model.CandidateCapability{model.CandidateCapabilityBrowser}},
		{"instructions minimum", true, true, []model.CandidateCapability{model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}},
		{"browser notice only", false, false, []model.CandidateCapability{model.CandidateCapabilityBrowser}},
		{"browser deliberate superset", false, true, []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "correction-capabilities-connect")
			before, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
			requireNoError(t, err)
			command := &store.ExamCorrectionApplication{
				RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: fixture.sitting.ID,
				CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: fixture.sitting.Revision,
				ActorUserID: fixture.manager.ID, Resources: []store.ExamCorrectionResourceManifestItem{},
				CandidateSummary: "The current examination reference changed.", AcknowledgementRequired: test.required,
				AffectedCapabilities: test.selected, PrivateReason: "Exercise independent correction gates",
				AppliedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis(),
			}
			if test.instructions {
				instructions := "Corrected instructions."
				command.InstructionsMarkdown = &instructions
			} else {
				policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/reference", HostMatch: model.BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
				requireNoError(t, err)
				command.BrowserPolicy = &policy
			}
			corrected, err := ss.ExamCorrection().Apply(ctx, command, examCommand(fixture.manager.ID, "exam.sitting.correction.apply.v1", "correction-capabilities", "correction-capabilities"))
			requireNoError(t, err)
			snapshot, err := ss.ExamRevision().GetSnapshot(ctx, fixture.examID, corrected.Revision.ID)
			requireNoError(t, err)
			if !slices.Equal(snapshot.CandidateCorrection.AffectedCapabilities, test.selected) {
				t.Fatal("revision lost its selection")
			}
			presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
			requireNoError(t, err)
			if presentation.Capacity != before.Capacity || presentation.Capacity.Validate() != nil ||
				presentation.RuntimeCapabilities.ExamRevision.AdmissionRevisionID != fixture.revisionID ||
				presentation.RuntimeCapabilities.ExamRevision.CurrentRevisionID != corrected.Revision.ID {
				t.Fatalf("correction changed admission capacity or lost current presentation: %#v", presentation)
			}
			blocked := func(capability model.CandidateCapability) bool {
				return test.required && slices.Contains(test.selected, capability)
			}
			pending := []model.CandidateCapability{}
			if test.required {
				pending = test.selected
			}
			if presentation.RuntimeCapabilities.Validate() != nil || !slices.Equal(presentation.RuntimeCapabilities.PendingCorrectionCapabilities, pending) ||
				presentation.RuntimeCapabilities.WorkspaceMutationAllowed == blocked(model.CandidateCapabilityWorkspace) || presentation.RuntimeCapabilities.SubmissionAllowed == blocked(model.CandidateCapabilitySubmission) ||
				len(presentation.LiveCorrections) != 1 || !slices.Equal(presentation.LiveCorrections[0].AffectedCapabilities, test.selected) {
				t.Fatalf("independent capability projection = %#v", presentation)
			}
			if (presentation.BrowserPolicy == nil) != blocked(model.CandidateCapabilityBrowser) {
				t.Fatal("usable browser policy escaped its gate")
			}
			workspaceAccess := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
				ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
				CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID,
				DPoPKeyThumbprint: access.DPoPKeyThumbprint, ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash}
			mutation := &store.ExamAttemptWorkspaceMutation{Access: workspaceAccess, Operation: model.AttemptWorkspaceMutationCreateDirectory,
				EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "after-correction",
				AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
			changed, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, mutation,
				examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "correction-workspace", "correction-workspace"))
			cursor := connected.Workspace.Cursor
			if blocked(model.CandidateCapabilityWorkspace) {
				assertExamAttemptConflict(t, err, "exam_correction_acknowledgement_required")
			} else {
				requireNoError(t, err)
				cursor = changed.Change.Cursor
			}
			source := browserSourceID(501 + caseIndex)
			_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access,
				ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
			if blocked(model.CandidateCapabilityBrowser) {
				assertExamAttemptConflict(t, err, "exam_correction_acknowledgement_required")
			} else {
				requireNoError(t, err)
				_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access,
					ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
					SourceSessionID: source, Events: []model.BrowserActivityEvent{browserActivityEvent(1, model.BrowserActivityOpened, corrected.Revision.ID), browserActivityEvent(2, model.BrowserActivityClosed, corrected.Revision.ID)}})
				requireNoError(t, err)
			}
			seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{
				AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
				ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
				ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
				ExpectedCurrentRevisionID: corrected.Revision.ID, ExpectedWorkspaceCursor: cursor},
				AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
			attachSubmissionReceipt(t, fixture.candidate, seal)
			_, err = ss.ExamSubmission().Seal(ctx, seal, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "correction-submit", "correction-submit"))
			if blocked(model.CandidateCapabilitySubmission) {
				assertSubmissionConflict(t, err, "exam_correction_acknowledgement_required")
			} else {
				requireNoError(t, err)
			}
		})
	}
}

func testCorrectionPublicationWorkspaceRace(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "correction-race-connect")
	instructions := "Updated instructions require acknowledgement."
	correction := &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: fixture.sitting.Revision,
		ActorUserID: fixture.manager.ID, InstructionsMarkdown: &instructions, Resources: []store.ExamCorrectionResourceManifestItem{},
		CandidateSummary: "Instructions changed.", AcknowledgementRequired: true,
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace},
		PrivateReason:        "Verify correction and Workspace serialization", AppliedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID.String(), AuditAt: model.GetMillis()}
	write := &store.ExamAttemptWorkspaceMutation{Access: store.ExamAttemptWorkspaceMutationAccess{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID,
		DPoPKeyThumbprint: access.DPoPKeyThumbprint, ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash},
		Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "racing-correction",
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	correctionKey := examCommand(fixture.manager.ID, "exam.sitting.correction.apply.v1", "publication-race", "publication-race")
	writeKey := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "write-race", "write-race")
	type writeOutcome struct {
		value *store.ExamAttemptWorkspaceMutationResult
		err   error
	}
	start := make(chan struct{})
	completed := make(chan writeOutcome, 1)
	go func() {
		<-start
		value, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, write, writeKey)
		completed <- writeOutcome{value, err}
	}()
	close(start)
	published, publishErr := ss.ExamCorrection().Apply(ctx, correction, correctionKey)
	outcome := <-completed
	requireNoError(t, publishErr)
	if outcome.err == nil {
		if outcome.value.Change.ChangedAt.After(published.EffectiveAt) {
			t.Fatal("Workspace write passed a correction already in effect")
		}
	} else {
		assertExamAttemptConflict(t, outcome.err, "exam_correction_acknowledgement_required")
	}
	// Once publication returns, both a new write and an exact previously
	// accepted replay must see the current Workspace gate.
	write.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, write, writeKey)
	assertExamAttemptConflict(t, err, "exam_correction_acknowledgement_required")
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	requireNoError(t, err)
	if presentation.RuntimeCapabilities.WorkspaceMutationAllowed || presentation.RuntimeCapabilities.ExamRevision.CurrentRevisionID != published.Revision.ID {
		t.Fatal("post-publication state lost its gate")
	}
}
