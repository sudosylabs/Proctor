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
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestConnectDelegatesAtomicAdmissionAndPassesOnlyCredentialHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	command := validConnectCommand(f, credential, "test-key")
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.AdmissionRevisionID != f.revision.ID || result.ClassID != f.sitting.ClassID ||
		!result.FirstAdmission || !result.ConnectionOpened || f.effects.opened != 1 {
		t.Fatalf("connection result = %#v", result)
	}
	input := f.persistence.connect
	if input == nil || input.ContinuityCredentialHash != model.HashToken(credential) ||
		strings.Contains(input.ContinuityCredentialHash, credential) || input.InitialConfiguration == nil ||
		input.InitialConfiguration.UserSettingsRevision != f.configuration.UserSettingsRevision || input.DesktopBuild != f.desktopBuild ||
		input.DesktopCompatibilityPolicyRevision != 1 ||
		input.ConfigurationManifestFingerprint != model.CurrentAttemptConfigurationManifestFingerprint() || result.Configuration.Digest != f.configuration.Digest ||
		result.RuntimeCapabilities.AttemptConfiguration.Digest != f.configuration.Digest {
		t.Fatalf("persistence credential = %#v", input)
	}
	if got := strings.Join(f.order, ","); got != "sitting,audit,connect,effect.open" {
		t.Fatalf("order = %s", got)
	}
	if strings.Contains(strings.ToLower(fmt.Sprintf("%#v", result.Participation)), "credential") ||
		strings.Contains(fmt.Sprintf("%#v", result.Participation), credential) ||
		strings.Contains(fmt.Sprintf("%#v", result.Participation), model.HashToken(credential)) {
		t.Fatalf("credential leaked through connection result: %#v", result.Participation)
	}
	if f.audit.values["continuity_credential"] != nil || f.audit.values["credential_hash"] != nil {
		t.Fatalf("credential entered audit = %#v", f.audit.values)
	}
	wantIdempotency, prepareErr := prepareConnectIdempotency(f.invocation, command)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	if strings.Contains(auditCapture, credential) || strings.Contains(auditCapture, model.HashToken(credential)) {
		t.Fatalf("credential material entered audit = %s", auditCapture)
	}
}

func TestAcknowledgeCorrectionUsesExactParticipationFenceAndSemanticIdempotency(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	correctionRevisionID := model.NewExamRevisionID()
	currentRevisionID := model.NewExamRevisionID()
	f.persistence.correctionAcknowledgementTarget = &store.ExamAttemptCorrectionAcknowledgementTarget{
		AttemptID: f.attemptID, ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, CorrectionRevisionID: correctionRevisionID, CurrentRevisionID: currentRevisionID,
	}
	f.persistence.correctionAcknowledgementResult = &store.ExamAttemptCorrectionAcknowledgementResult{
		AttemptID: f.attemptID, CorrectionRevisionID: correctionRevisionID, CurrentRevisionID: currentRevisionID,
		AcknowledgedAt: f.at,
	}
	f.persistence.presentation = &store.CandidateExamPresentation{
		Capacity:  model.DefaultExamCapacityPolicy(),
		AttemptID: f.attemptID, SittingID: f.sitting.ID,
		RuntimeCapabilities: runtimeCapabilitiesFixture(f, currentRevisionID), BrowserPolicy: disabledCandidateBrowserPolicyFixture(currentRevisionID),
		Title: "Algorithms", Resources: []store.CandidateExamResource{},
	}
	command := AcknowledgeCorrectionCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{
			AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: credential,
		}, ParticipationID: participationID, Generation: 4},
		CorrectionRevisionID: correctionRevisionID, ExpectedCurrentRevisionID: currentRevisionID,
		IdempotencyKey: "acknowledge-key",
	}

	result, err := f.service.AcknowledgeExamAttemptCorrection(context.Background(), f.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.correctionAcknowledgement
	principal := f.invocation.Principal()
	if input == nil || input.Access.AttemptID != f.attemptID || input.Access.CandidateUserID != principal.UserID ||
		input.Access.SessionID != principal.SessionID || input.Access.ConnectionID != f.connectionID ||
		input.Access.DesktopRegistrationID != principal.DesktopRegistrationID || input.Access.DPoPKeyThumbprint != principal.DPoPKeyThumbprint ||
		input.Access.ContinuityCredentialHash != model.HashToken(credential) || input.ParticipationID != participationID ||
		input.Generation != 4 || input.CorrectionRevisionID != correctionRevisionID ||
		input.ExpectedCurrentRevisionID != currentRevisionID || input.AuditEvent == nil || !input.AuditEvent.ID.IsZero() ||
		input.AuditEvent.Status != model.AuditStatusAttempt {
		t.Fatalf("acknowledgement input = %#v", input)
	}
	if result.CorrectionRevisionID != correctionRevisionID || result.CurrentRevisionID != currentRevisionID ||
		result.AcknowledgedAt.Time != f.at {
		t.Fatalf("acknowledgement result = %#v", result)
	}
	wantIdempotency, prepareErr := applicationidempotency.Require(f.invocation, store.ExamAttemptCorrectionAcknowledgementOperation,
		command.IdempotencyKey, struct {
			AttemptID                 string `json:"exam_attempt_id"`
			ParticipationID           string `json:"participation_id"`
			Generation                int64  `json:"generation"`
			CorrectionRevisionID      string `json:"correction_revision_id"`
			ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
		}{f.attemptID.String(), participationID.String(), 4, correctionRevisionID.String(), currentRevisionID.String()})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
	if got := strings.Join(f.order, ","); got != "correction.acknowledgement.resolve,audit.authorization,audit.prepare,correction.acknowledgement.commit" {
		t.Fatalf("order = %s", got)
	}
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	if strings.Contains(auditCapture, credential) || strings.Contains(auditCapture, model.HashToken(credential)) ||
		strings.Contains(auditCapture, participationID.String()) || strings.Contains(auditCapture, principal.SessionID.String()) ||
		strings.Contains(auditCapture, f.connectionID.String()) {
		t.Fatalf("private acknowledgement material entered audit: %s", auditCapture)
	}
}

func TestAcknowledgeCorrectionReplayReturnsRetainedResultAfterFreshAuthorizationAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	correctionRevisionID := model.NewExamRevisionID()
	currentRevisionID := model.NewExamRevisionID()
	f.persistence.correctionAcknowledgementTarget = &store.ExamAttemptCorrectionAcknowledgementTarget{
		AttemptID: f.attemptID, ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, CorrectionRevisionID: correctionRevisionID, CurrentRevisionID: currentRevisionID,
	}
	f.persistence.correctionAcknowledgementResult = &store.ExamAttemptCorrectionAcknowledgementResult{
		AttemptID: f.attemptID, CorrectionRevisionID: correctionRevisionID, CurrentRevisionID: currentRevisionID,
		AcknowledgedAt: f.at.Add(-time.Minute), Replayed: true,
	}
	f.persistence.presentation = &store.CandidateExamPresentation{
		Capacity:  model.DefaultExamCapacityPolicy(),
		AttemptID: f.attemptID, SittingID: f.sitting.ID,
		RuntimeCapabilities: runtimeCapabilitiesFixture(f, currentRevisionID), BrowserPolicy: disabledCandidateBrowserPolicyFixture(currentRevisionID),
		Title: "Algorithms", Resources: []store.CandidateExamResource{},
	}
	command := AcknowledgeCorrectionCommand{Access: validWorkspaceMutationAccess(f), CorrectionRevisionID: correctionRevisionID,
		ExpectedCurrentRevisionID: currentRevisionID, IdempotencyKey: "replay-key"}

	result, err := f.service.AcknowledgeExamAttemptCorrection(context.Background(), f.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.AcknowledgedAt.Time != f.at.Add(-time.Minute) || f.persistence.correctionAcknowledgementCalls != 1 ||
		strings.Join(f.order, ",") != "correction.acknowledgement.resolve,audit.authorization,audit.prepare,correction.acknowledgement.commit" {
		t.Fatalf("result=%#v calls=%d order=%v", result, f.persistence.correctionAcknowledgementCalls, f.order)
	}
	// An exact replay retains its acknowledgement time while a later correction
	// controls the fresh capability response.
	laterRevisionID := model.NewExamRevisionID()
	caps := runtimeCapabilitiesFixture(f, laterRevisionID)
	caps.ExamRevision.AcknowledgementRequired = true
	caps.PendingCorrectionCapabilities = []model.CandidateCapability{model.CandidateCapabilityBrowser}
	caps.Browser = store.CandidateBrowserCapability{State: store.CandidateBrowserAcknowledgementRequired,
		PolicyRevisionID: laterRevisionID, PolicyDigest: "sha256:" + strings.Repeat("a", 64)}
	f.persistence.presentation.RuntimeCapabilities = caps
	f.persistence.presentation.BrowserPolicy = nil
	f.persistence.presentation.LiveCorrections = []model.CandidateLiveCorrection{{RevisionID: laterRevisionID,
		RevisionNumber: 9, EffectiveAt: f.at, Summary: "Browser policy changed again.",
		ChangedAreas:            []model.ExamCorrectionChangedArea{model.ExamCorrectionChangedBrowserPolicy},
		AffectedCapabilities:    []model.CandidateCapability{model.CandidateCapabilityBrowser},
		AcknowledgementRequired: true, AcknowledgementState: model.CorrectionAcknowledgementPending}}
	result, err = f.service.AcknowledgeExamAttemptCorrection(context.Background(), f.invocation, command)
	if err != nil || result.CurrentRevisionID != laterRevisionID || result.AcknowledgedAt.Time != f.at.Add(-time.Minute) ||
		result.RuntimeCapabilities.Browser.State != store.CandidateBrowserAcknowledgementRequired ||
		!result.RuntimeCapabilities.WorkspaceMutationAllowed || !result.RuntimeCapabilities.SubmissionAllowed {
		t.Fatalf("stale acknowledgement reopened capability: %#v, %v", result, err)
	}

}

func TestCreateWorkspaceDirectoryRevalidatesMutationAccessAuditsAndPublishesSafeChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	entryID := model.NewAttemptWorkspaceEntryID()
	workspaceID := model.NewExamAttemptWorkspaceID()
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 9, EntryID: entryID,
		EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory,
		NewPath: "src", ChangedAt: f.at}
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = &store.ExamAttemptWorkspaceMutationResult{SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, WorkspaceID: workspaceID,
		Entry: &store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: model.StarterWorkspaceEntryDirectory, Path: "src"}, Change: change}

	result, err := f.service.CreateCandidateExamWorkspaceDirectory(context.Background(), f.invocation, CreateWorkspaceDirectoryCommand{Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID,
		ContinuityCredential: credential}, ParticipationID: participationID, Generation: 4},
		Path: "src", IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := f.invocation.Principal()
	access := f.workspace.resolvedAccess
	if access.AttemptID != f.attemptID || access.ParticipationID != participationID || access.Generation != 4 ||
		access.CandidateUserID != principal.UserID || access.SessionID != principal.SessionID || access.ConnectionID != f.connectionID ||
		access.ContinuityCredentialHash != model.HashToken(credential) || f.workspace.mutation == nil ||
		f.workspace.mutation.EntryID != entryID || f.workspace.mutation.DestinationPath != "src" ||
		f.workspace.mutation.Operation != model.AttemptWorkspaceMutationCreateDirectory || !f.workspace.mutation.AuditEventID.IsValid() ||
		result.Change.Cursor != 9 || f.effects.workspaceChanged != 1 {
		t.Fatalf("access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	}
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	if len(f.audit.values) != 2 || f.audit.values["exam_attempt_id"] != f.attemptID.String() || f.audit.values["operation"] != string(model.AttemptWorkspaceMutationCreateDirectory) || strings.Contains(auditCapture, credential) ||
		strings.Contains(auditCapture, model.HashToken(credential)) || strings.Contains(auditCapture, participationID.String()) {
		t.Fatalf("private mutation material entered audit: %s", auditCapture)
	}
	if got := strings.Join(f.order, ","); got != "workspace.resolve,audit,workspace.apply,effect.workspace" {
		t.Fatalf("order = %s", got)
	}
	wantIdempotency, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID,
		model.AttemptWorkspaceMutationCreateDirectory, struct {
			Path string `json:"path"`
		}{"src"})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.workspace.idempotency, wantIdempotency)
}

func TestCreateWorkspaceFileStagesBeforeAtomicMutationAndSuppressesPrivateEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	entryID := model.NewAttemptWorkspaceEntryID()
	workspaceID := model.NewExamAttemptWorkspaceID()
	version := model.NewWorkspaceContentVersion()
	checksum := strings.Repeat("a", 64)
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, entryID, model.StarterWorkspaceEntryFile,
		model.AttemptWorkspaceMutationCreateFile, "", "main.go", version, false)
	f.content.attemptContent = &model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 4, SHA256: checksum}

	result, err := f.service.CreateCandidateExamWorkspaceFile(context.Background(), f.invocation, CreateWorkspaceFileCommand{Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID,
		ContinuityCredential: credential}, ParticipationID: participationID, Generation: 2},
		Path: "main.go", MediaType: "text/plain", ExpectedSHA256: checksum, Body: strings.NewReader("main"), Size: 4,
		IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.workspace.reservation == nil || f.workspace.ready == nil || f.workspace.mutation == nil ||
		f.workspace.reservation.ObjectID != f.content.attemptObjectID || f.workspace.ready.ObjectID != f.content.attemptObjectID ||
		f.workspace.ready.ContentVersion != version || f.workspace.mutation.ObjectID != f.content.attemptObjectID ||
		f.workspace.mutation.DestinationPath != "main.go" || result.Entry == nil || result.Entry.ContentVersion != version ||
		f.effects.workspaceChanged != 1 {
		t.Fatalf("reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	}
	capture := fmt.Sprintf("%#v", f.audit.values)
	if strings.Contains(capture, "main.go") || strings.Contains(capture, checksum) || strings.Contains(capture, credential) ||
		strings.Contains(capture, model.HashToken(credential)) {
		t.Fatalf("private file material entered audit: %s", capture)
	}
	if got := strings.Join(f.order, ","); got != "workspace.resolve,workspace.reserve,content.stage,workspace.ready,audit,workspace.apply,effect.workspace" {
		t.Fatalf("order = %s", got)
	}
	wantIdempotency, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID,
		model.AttemptWorkspaceMutationCreateFile, struct {
			Path, MediaType, SHA256 string
			Size                    int64
		}{"main.go", "text/plain", checksum, 4})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.workspace.idempotency, wantIdempotency)
}

func TestReplaceWorkspaceFilePassesOwnedIdempotencyToStore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID, entryID := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID()
	currentVersion, nextVersion := model.NewWorkspaceContentVersion(), model.NewWorkspaceContentVersion()
	checksum := strings.Repeat("c", 64)
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, entryID, model.StarterWorkspaceEntryFile,
		model.AttemptWorkspaceMutationReplaceFile, "main.go", "main.go", nextVersion, false)
	f.content.attemptContent = &model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 4, SHA256: checksum}
	command := ReplaceWorkspaceFileCommand{Access: validWorkspaceMutationAccess(f),
		EntryID: entryID, ExpectedPath: "main.go", ExpectedContentVersion: currentVersion, MediaType: "text/plain",
		ExpectedSHA256: checksum, Body: strings.NewReader("main"), Size: 4, IdempotencyKey: "replace-key"}
	if _, err := f.service.ReplaceCandidateExamWorkspaceFile(context.Background(), f.invocation, command); err != nil {
		t.Fatal(err)
	}
	want, err := prepareWorkspaceMutationIdempotency(f.invocation, "replace-key", f.attemptID,
		model.AttemptWorkspaceMutationReplaceFile, struct {
			EntryID, Path, Version, MediaType, SHA256 string
			Size                                      int64
		}{entryID.String(), "main.go", currentVersion.String(), "text/plain", checksum, 4})
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, f.workspace.idempotency, want)
}

func TestWorkspaceFileReplayReclaimsLosingStageAndPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID, entryID := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID()
	version, checksum := model.NewWorkspaceContentVersion(), strings.Repeat("b", 64)
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, entryID, model.StarterWorkspaceEntryFile,
		model.AttemptWorkspaceMutationCreateFile, "", "retry.txt", version, true)
	f.workspace.proposedEntryID = model.NewAttemptWorkspaceEntryID()
	f.workspace.proposedVersion = model.NewWorkspaceContentVersion()
	f.content.attemptContent = &model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 1, SHA256: checksum}
	_, err := f.service.CreateCandidateExamWorkspaceFile(context.Background(), f.invocation, CreateWorkspaceFileCommand{Access: validWorkspaceMutationAccess(f), Path: "retry.txt", MediaType: "text/plain", ExpectedSHA256: checksum,
		Body: strings.NewReader("x"), Size: 1, IdempotencyKey: "test-key",
	})
	if err != nil || len(f.workspace.reclaimable) != 1 || f.effects.workspaceChanged != 0 ||
		f.workspace.mutation.EntryID != f.workspace.proposedEntryID || f.workspace.mutation.EntryID == entryID ||
		f.workspace.ready.ContentVersion != f.workspace.proposedVersion || resultVersion(f.workspace.mutationResult) != version {
		t.Fatalf("error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	}
}

func TestWorkspaceDirectoryReplayAcceptsRetainedEntryInsteadOfFreshProposal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID, retainedID := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID()
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, retainedID, model.StarterWorkspaceEntryDirectory,
		model.AttemptWorkspaceMutationCreateDirectory, "", "src", "", true)
	f.workspace.proposedEntryID = model.NewAttemptWorkspaceEntryID()
	result, err := f.service.CreateCandidateExamWorkspaceDirectory(context.Background(), f.invocation, CreateWorkspaceDirectoryCommand{Access: validWorkspaceMutationAccess(f), Path: "src", IdempotencyKey: "test-key"})
	if err != nil || result.Entry == nil || result.Entry.EntryID != retainedID || f.workspace.mutation.EntryID != f.workspace.proposedEntryID ||
		f.workspace.mutation.EntryID == retainedID || f.effects.workspaceChanged != 0 {
		t.Fatalf("result=%#v mutation=%#v error=%v", result, f.workspace.mutation, err)
	}
}

func TestWorkspaceReadyObjectReclamationDistinguishesStableAndUnknownApplyOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		err         error
		wantCode    string
		wantReclaim int
	}{
		{name: "stable conflict", err: store.NewErrConflict("attempt_workspace", "attempt_workspace_path", errors.New("collision")),
			wantCode: "exam.attempt.workspace.path_conflict", wantReclaim: 1},
		{name: "outcome unknown", err: errors.New("database acknowledgement lost"),
			wantCode: "exam.attempt.unavailable", wantReclaim: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
				ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: model.NewExamAttemptWorkspaceID()}
			f.workspace.mutationErr = test.err
			checksum := strings.Repeat("d", 64)
			f.content.attemptContent = &model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 1, SHA256: checksum}
			_, err := f.service.CreateCandidateExamWorkspaceFile(context.Background(), f.invocation, CreateWorkspaceFileCommand{Access: validWorkspaceMutationAccess(f),
				Path: "work.txt", MediaType: "text/plain", ExpectedSHA256: checksum, Body: strings.NewReader("x"), Size: 1,
				IdempotencyKey: "test-key"})
			var fault *apperror.Error
			if !errors.As(err, &fault) || fault.Code() != test.wantCode || len(f.workspace.reclaimable) != test.wantReclaim {
				t.Fatalf("error=%v reclaimable=%v", err, f.workspace.reclaimable)
			}
		})
	}
}

func TestWorkspaceMetadataMutationsCarrySelectiveEntryFences(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		operation model.AttemptWorkspaceMutationKind
		invoke    func(*fixture, model.AttemptWorkspaceEntryID, model.WorkspaceContentVersion) error
		check     func(*testing.T, *store.ExamAttemptWorkspaceMutation)
	}{
		{name: "move", operation: model.AttemptWorkspaceMutationMoveEntry, invoke: func(f *fixture, id model.AttemptWorkspaceEntryID, _ model.WorkspaceContentVersion) error {
			_, err := f.service.MoveCandidateExamWorkspaceEntry(context.Background(), f.invocation, MoveWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: id, ExpectedPath: "old", DestinationPath: "new", IdempotencyKey: "test-key"})
			return err
		}, check: func(t *testing.T, mutation *store.ExamAttemptWorkspaceMutation) {
			if mutation.ExpectedPath != "old" || mutation.DestinationPath != "new" {
				t.Fatalf("mutation=%#v", mutation)
			}
		}},
		{name: "delete_file", operation: model.AttemptWorkspaceMutationDeleteEntry, invoke: func(f *fixture, id model.AttemptWorkspaceEntryID, version model.WorkspaceContentVersion) error {
			_, err := f.service.DeleteCandidateExamWorkspaceEntry(context.Background(), f.invocation, DeleteWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: id, ExpectedPath: "old", ExpectedContentVersion: version, IdempotencyKey: "test-key"})
			return err
		}, check: func(t *testing.T, mutation *store.ExamAttemptWorkspaceMutation) {
			if mutation.ExpectedPath != "old" || mutation.ExpectedContentVersion.IsZero() {
				t.Fatalf("mutation=%#v", mutation)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			workspaceID, entryID, version := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID(), model.NewWorkspaceContentVersion()
			f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
				ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
			f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, entryID, model.StarterWorkspaceEntryFile,
				test.operation, "old", map[bool]string{true: "new"}[test.operation == model.AttemptWorkspaceMutationMoveEntry], version, false)
			if test.operation == model.AttemptWorkspaceMutationDeleteEntry {
				f.workspace.mutationResult.Entry = nil
			}
			if err := test.invoke(f, entryID, version); err != nil {
				t.Fatal(err)
			}
			test.check(t, f.workspace.mutation)
			var semantic any
			switch test.operation {
			case model.AttemptWorkspaceMutationMoveEntry:
				semantic = struct{ EntryID, ExpectedPath, DestinationPath string }{entryID.String(), "old", "new"}
			case model.AttemptWorkspaceMutationDeleteEntry:
				semantic = struct{ EntryID, ExpectedPath, ExpectedContentVersion string }{entryID.String(), "old", version.String()}
			}
			want, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID, test.operation, semantic)
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			assertStoreBoundaryCommand(t, f.workspace.idempotency, want)
		})
	}
}

func TestConnectRejectsMalformedCredentialAndPATBeforeReads(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*fixture){
		func(f *fixture) {
			f.invocation = invocation.New(model.Principal{UserID: f.userID}, model.RequestMetadata{})
		},
		func(f *fixture) {
			principal := f.invocation.Principal()
			principal.CredentialType = model.CredentialPersonalAccessToken
			principal.SessionID = ""
			principal.AuthenticationStrength = ""
			principal.AuthenticatedAt = time.Time{}
			principal.ClientType = model.SessionClientCLI
			principal.CredentialScopes = []string{string(model.ActionExamSittingView)}
			f.invocation = invocation.New(principal, model.RequestMetadata{})
		},
	} {
		f := newFixture(t)
		mutate(f)
		_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, ConnectCommand{
			SittingID: f.sitting.ID, ContinuityCredential: "not-canonical", IdempotencyKey: "test-key",
		})
		if err == nil || len(f.order) != 0 {
			t.Fatalf("error=%v order=%v", err, f.order)
		}
	}
}

func TestRenewParticipationBindsAuthenticatedConnectionAndPassesOnlyCredentialHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	databaseNow := f.at.Add(5 * time.Second)
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: renewalCoverageResultFixture(),
		AttemptID: f.attemptID, ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		CandidateUserID: f.invocation.Principal().UserID, ParticipationID: participationID, Generation: 3, AcceptedSequence: 7,
		DatabaseTime: databaseNow, LeaseExpiresAt: databaseNow.Add(model.AttemptParticipationInitialLease),
	}
	result, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
		AttemptID: f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
		Generation: 3, Sequence: 7, ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.renew
	principal := f.invocation.Principal()
	if input == nil || input.AttemptID != f.attemptID || input.ParticipationID != participationID ||
		input.ConnectionID != f.connectionID || input.CandidateUserID != principal.UserID || input.SessionID != principal.SessionID ||
		input.Generation != 3 || input.Sequence != 7 || input.ContinuityCredentialHash != model.HashToken(credential) ||
		result.AcceptedSequence != 7 || !result.DatabaseTime.Equal(databaseNow) || f.effects.renewed != 1 ||
		strings.Contains(fmt.Sprintf("%#v", result), credential) || strings.Contains(fmt.Sprintf("%#v", result), model.HashToken(credential)) {
		t.Fatalf("renewal input/result = %#v / %#v", input, result)
	}
}

func TestDuplicateParticipationRenewalDoesNotRepublishRenewal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	participationID := model.NewAttemptParticipationID()
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: renewalCoverageResultFixture(),
		AttemptID: f.attemptID, ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		CandidateUserID: f.invocation.Principal().UserID, ParticipationID: participationID, Generation: 2,
		AcceptedSequence: 4, DatabaseTime: f.at, LeaseExpiresAt: f.at.Add(model.AttemptParticipationInitialLease), Duplicate: true,
	}
	_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
		AttemptID: f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
		Generation: 2, Sequence: 4, ContinuityCredential: model.NewCredentialToken(),
	})
	if err != nil || f.effects.renewed != 0 {
		t.Fatalf("error=%v renewal effects=%d", err, f.effects.renewed)
	}
}

func TestRenewParticipationRejectsInvalidPrincipalAndFieldsBeforeStore(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*fixture, *RenewParticipationCommand){
		func(f *fixture, command *RenewParticipationCommand) {
			f.invocation = invocation.New(model.Principal{}, model.RequestMetadata{})
		},
		func(_ *fixture, command *RenewParticipationCommand) { command.AttemptID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.ParticipationID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.ConnectionID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.Generation = 0 },
		func(_ *fixture, command *RenewParticipationCommand) { command.Sequence = 0 },
		func(_ *fixture, command *RenewParticipationCommand) { command.ContinuityCredential = "not-canonical" },
	} {
		f := newFixture(t)
		command := RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(), AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(),
			ConnectionID: f.connectionID, Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken()}
		mutate(f, &command)
		if _, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, command); err == nil || f.persistence.renew != nil {
			t.Fatalf("RenewParticipation(%#v) error=%v input=%#v", command, err, f.persistence.renew)
		}
	}
}

func TestRenewParticipationRejectsInconsistentStoreProjection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: renewalCoverageResultFixture(),
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), Generation: 1,
		AcceptedSequence: 1, DatabaseTime: f.at, LeaseExpiresAt: f.at.Add(model.AttemptParticipationInitialLease),
	}
	_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
		Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestRenewParticipationMapsStableCandidateFaults(t *testing.T) {
	t.Parallel()
	tests := []struct{ constraint, code string }{
		{"attempt_participation_credential", "exam.attempt.continuity_invalid"},
		{"attempt_participation_generation", "exam.attempt.connection_closed"},
		{"attempt_participation_sequence", "exam.attempt.renewal_conflict"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.constraint, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.persistence.renewErr = store.NewErrConflict("attempt_participation", test.constraint, nil)
			_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
				AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
				Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken(),
			})
			if !chainHasCode(err, test.code) {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestLateRenewalUsesExactExpiryTransitionAndReturnsSafeConnectionLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.renewErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	f.persistence.resolvedExpiry = &due
	f.persistence.expireResult = expiryResultFixture(t, f, due, false)
	_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
		AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, ConnectionID: f.connectionID,
		Generation: due.Generation, Sequence: 5, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_lost" || f.effects.expired != 1 ||
		strings.Join(f.order, ",") != "renew,expiry.resolve,system.audit,expiry.commit,effect.expire" {
		t.Fatalf("error=%v effects=%#v order=%v", err, f.effects, f.order)
	}
}

func TestLateRenewalConcealsScannerWinningBeforeTargetResolution(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.renewErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	f.persistence.resolveExpiryErr = store.NewErrNotFound("attempt_participation", "ended")
	_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, RenewParticipationCommand{SecurityCoverage: renewalCoverageFixture(),
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
		Generation: 2, Sequence: 5, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_lost" || f.effects.expired != 0 ||
		strings.Join(f.order, ",") != "renew,expiry.resolve" {
		t.Fatalf("error=%v effects=%#v order=%v", err, f.effects, f.order)
	}
}

func TestScanExpiredParticipationsCommitsAuditedAggregateBeforeSafeEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{due}
	f.persistence.expireResult = expiryResultFixture(t, f, due, false)
	result, err := f.service.ScanExpiredParticipations(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.expireInput
	if result.Due != 1 || result.Completed != 1 || result.Replayed != 0 || input == nil ||
		input.AttemptID != due.AttemptID || input.ParticipationID != due.ParticipationID || input.Generation != due.Generation ||
		!input.EvidenceID.IsValid() || !input.FlagID.IsValid() || !input.SuspensionID.IsValid() ||
		!input.AuditEventID.IsValid() || input.AuditAt < 1 || f.effects.expired != 1 ||
		strings.Join(f.order, ",") != "expiry.list,system.audit,expiry.commit,effect.expire" {
		t.Fatalf("scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	}
	if f.systemAudit.values["continuity_credential"] != nil || f.systemAudit.values["credential_hash"] != nil ||
		f.systemAudit.values["private_reason"] != nil {
		t.Fatalf("sensitive expiry audit values = %#v", f.systemAudit.values)
	}
	for _, forbidden := range []string{"continuity_credential", "credential_hash", "private_reason"} {
		if strings.Contains(strings.ToLower(fmt.Sprintf("%#v", f.systemAudit.values)), forbidden) {
			t.Fatalf("expiry audit capture contains %q: %#v", forbidden, f.systemAudit.values)
		}
	}
}

func TestScanExpiredParticipationPublishesSuspensionAfterTransportAlreadyClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{due}
	f.persistence.expireResult = expiryResultFixture(t, f, due, false)
	f.persistence.expireResult.ConnectionClosed = false
	f.persistence.expireResult.Connection.CloseReason = model.AttemptConnectionCloseTransport

	result, err := f.service.ScanExpiredParticipations(context.Background(), 1)
	if err != nil || result.Completed != 1 || f.effects.expired != 1 {
		t.Fatalf("scan=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestScanExpiredParticipationsSuppressesReplayEffectsAndRejectsUnboundedLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{due}
	f.persistence.expireResult = expiryResultFixture(t, f, due, true)
	result, err := f.service.ScanExpiredParticipations(context.Background(), 1)
	if err != nil || result.Replayed != 1 || f.effects.expired != 0 {
		t.Fatalf("scan=%#v error=%v effects=%#v", result, err, f.effects)
	}
	for _, limit := range []int{0, 201} {
		other := newFixture(t)
		if _, err = other.service.ScanExpiredParticipations(context.Background(), limit); err == nil || len(other.order) != 0 {
			t.Fatalf("limit=%d error=%v order=%v", limit, err, other.order)
		}
	}
}

func TestScanExpiredParticipationsFailsAuditAndPublishesNothingOnCommitFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{expiryDueFixture(f)}
	f.persistence.expireErr = store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	_, err := f.service.ScanExpiredParticipations(context.Background(), 10)
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_closed" ||
		f.systemAudit.failedCode != fault.Code() || f.effects.expired != 0 ||
		strings.Join(f.order, ",") != "expiry.list,system.audit,expiry.commit,system.audit.fail" {
		t.Fatalf("error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	}
}

func TestReallowRequiresExactSuspensionAndKeepsPrivateReasonOutOfEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	managerID := useManagerCall(f)
	suspensionID := model.NewAttemptSuspensionID()
	f.persistence.attempt, f.persistence.managerSuspension = managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2, suspensionID)
	f.persistence.reallowResult = reallowResultFixture(t, f, suspensionID, false)
	reason := "manager verified secure connectivity"
	result, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: reason, IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.reallow
	if input == nil || input.SuspensionID != suspensionID || input.ExpectedAttemptRevision != 2 || input.PrivateReason != reason ||
		input.ActorUserID != managerID || input.ManagerOverride || result.Attempt.State != model.ExamAttemptReady ||
		result.Suspension.State != model.AttemptSuspensionClosed || f.effects.reallowed != 1 ||
		strings.Join(f.order, ",") != "manager.manage,sitting,audit,manager.get,reallow,effect.reallow" {
		t.Fatalf("result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	}
	if f.audit.values["private_reason"] != nil || strings.Contains(fmt.Sprintf("%#v", result), reason) {
		t.Fatalf("private reason escaped persistence input: audit=%#v result=%#v", f.audit.values, result)
	}
	for _, capture := range []any{f.audit.values, result} {
		if strings.Contains(fmt.Sprintf("%#v", capture), reason) {
			t.Fatalf("private reason escaped persistence input: %#v", capture)
		}
	}
	wantIdempotency, prepareErr := prepareReallowIdempotency(f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: reason, IdempotencyKey: "test-key",
	})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
}

func TestReallowAcceptsClosedFocusLossSuspension(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	useManagerCall(f)
	suspensionID := model.NewAttemptSuspensionID()
	f.persistence.attempt, f.persistence.managerSuspension = managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2, suspensionID)
	f.persistence.reallowResult = reallowResultFixtureWithReason(t, f, suspensionID, false,
		model.AttemptSuspensionCandidateReasonFocusLossPolicy)
	result, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: "manager reviewed the focus loss", IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatalf("Reallow() error = %v, want committed focus-loss re-allow to succeed", err)
	}
	if result.Suspension.CandidateReason != model.AttemptSuspensionCandidateReasonFocusLossPolicy {
		t.Fatalf("Reallow() suspension reason = %q", result.Suspension.CandidateReason)
	}
	if f.effects.reallowed != 1 {
		t.Fatalf("Reallow() effects = %d, want candidate reconnect effect", f.effects.reallowed)
	}
}

func TestManagerAttemptOperationsAuthorizeBeforeReadingAttempts(t *testing.T) {
	t.Parallel()
	denied := apperror.New("authorization.denied")
	operations := map[string]func(*fixture, model.ExamAttemptID) error{
		"get": func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.GetExamAttempt(context.Background(), f.invocation, GetManagedAttemptQuery{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID})
			return err
		},
		"reallow": func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID, SuspensionID: model.NewAttemptSuspensionID(),
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		},
		"end": func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.EndExamAttemptByManager(context.Background(), f.invocation, ManagerEndCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID,
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		},
		"suspend": func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID,
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		},
		"browser_activity": func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.ListExamAttemptBrowserActivity(context.Background(), f.invocation, BrowserActivityPageQuery{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID, Limit: 10})
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var codes []string
			for _, existing := range []bool{true, false} {
				f := newFixture(t)
				useManagerCall(f)
				f.managerErr = denied
				f.persistence.attempt, f.persistence.managerSuspension = managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2,
					model.NewAttemptSuspensionID())
				attemptID := f.attemptID
				if !existing {
					attemptID = model.NewExamAttemptID()
				}
				err := operation(f, attemptID)
				var fault *apperror.Error
				if !errors.As(err, &fault) {
					t.Fatalf("existing=%t error = %v", existing, err)
				}
				codes = append(codes, fault.Code())
				if slices.Contains(f.order, "manager.get") || f.audit.values != nil {
					t.Fatalf("existing=%t denied caller reached Attempt state: order=%v audit=%#v", existing, f.order, f.audit.values)
				}
			}
			if codes[0] != codes[1] || codes[0] != "resource.not_found" {
				t.Fatalf("denied responses distinguish Attempt existence: %v", codes)
			}
		})
	}
}

func TestManagerAttemptMissingAfterAuthorizationCompletesFailedAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	useManagerCall(f)
	f.persistence.attempt, f.persistence.managerSuspension = managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2,
		model.NewAttemptSuspensionID())
	f.persistence.getErr = store.NewErrNotFound("exam_attempt", "missing")
	_, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: model.NewExamAttemptID(), SuspensionID: model.NewAttemptSuspensionID(),
		ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
	if !apperror.Is(err, "resource.not_found") || !chainHasCode(err, "exam.attempt.not_found") || f.audit.failedCode != "exam.attempt.not_found" ||
		f.persistence.reallow != nil {
		t.Fatalf("error=%v audit=%#v order=%v", err, f.audit, f.order)
	}
}

func TestReallowValidatesReasonBeforeAuthorization(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"", " padded ", "\xff", strings.Repeat("é", model.AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		f := newFixture(t)
		_, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
			ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: model.NewAttemptSuspensionID(),
			ExpectedAttemptRevision: 1, PrivateReason: reason, IdempotencyKey: "test-key",
		})
		if err == nil || len(f.order) != 0 || f.persistence.reallow != nil {
			t.Fatalf("reason=%q error=%v order=%v", reason, err, f.order)
		}
	}
}

func TestReallowReplaySuppressesEffectAndPropagatesAuthorizedOverride(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	useManagerCall(f)
	f.managerOverride = true
	suspensionID := model.NewAttemptSuspensionID()
	f.persistence.attempt, f.persistence.managerSuspension = managerAttemptFixture(t, f, model.ExamAttemptSuspended, 2, suspensionID)
	f.persistence.reallowResult = reallowResultFixture(t, f, suspensionID, true)
	result, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: "authorized override review", IdempotencyKey: "test-key",
	})
	if err != nil || !result.Replayed || !f.persistence.reallow.ManagerOverride || f.effects.reallowed != 0 {
		t.Fatalf("result=%#v input=%#v error=%v effects=%#v", result, f.persistence.reallow, err, f.effects)
	}
}

func expiryDueFixture(f *fixture) store.ExamAttemptParticipationExpiryDue {
	return store.ExamAttemptParticipationExpiryDue{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, LeaseExpiresAt: f.at.Add(-time.Second)}
}

func expiryResultFixture(t *testing.T, f *fixture, due store.ExamAttemptParticipationExpiryDue, replayed bool) *store.ExamAttemptParticipationExpiryResult {
	t.Helper()
	attempt, err := model.NewExamAttempt(due.AttemptID, due.ExamID, due.SittingID, due.CandidateUserID, f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, f.at, 2
	participation := &store.ExamAttemptParticipationView{ID: due.ParticipationID, AttemptID: due.AttemptID,
		State: model.AttemptParticipationEnded, Generation: due.Generation, RenewalSequence: 4,
		StartedAt: f.at.Add(-time.Minute), UpdatedAt: f.at, LeaseExpiresAt: due.LeaseExpiresAt,
		EndedAt: model.OptionalTimeFrom(f.at), EndReason: model.AttemptParticipationEndLeaseExpired}
	connection := &store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
		OpenedAt: f.at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(f.at), CloseReason: model.AttemptConnectionCloseLeaseExpired}
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), due.AttemptID, due.Generation, model.IntegrityPolicyConnectionLoss, f.at)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := model.NewConnectionLossEvidence(model.NewIntegrityEvidenceID(), due.AttemptID, due.ParticipationID,
		flag.ID, due.Generation, due.LeaseExpiresAt, f.at)
	if err != nil {
		t.Fatal(err)
	}
	suspension := &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: due.AttemptID,
		ParticipationID: due.ParticipationID, FlagID: flag.ID, Generation: due.Generation, State: model.AttemptSuspensionActive,
		Source: model.AttemptSuspensionSourcePolicy, CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: f.at}
	return &store.ExamAttemptParticipationExpiryResult{ExamID: due.ExamID, SittingID: due.SittingID, ClassID: due.ClassID,
		CandidateUserID: due.CandidateUserID, Attempt: attempt, Participation: participation, Connection: connection,
		ConnectionClosed: true, Evidence: evidence, Flag: flag, Suspension: suspension, DatabaseTime: f.at, Replayed: replayed}
}

func reallowResultFixture(t *testing.T, f *fixture, suspensionID model.AttemptSuspensionID, replayed bool) *store.ExamAttemptReallowResult {
	t.Helper()
	return reallowResultFixtureWithReason(t, f, suspensionID, replayed, model.AttemptSuspensionCandidateReasonSecureContinuityLost)
}

func reallowResultFixtureWithReason(t *testing.T, f *fixture, suspensionID model.AttemptSuspensionID, replayed bool,
	reason model.AttemptSuspensionCandidateReason) *store.ExamAttemptReallowResult {
	t.Helper()
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempt.UpdatedAt, attempt.Revision = f.at, 3
	attempt.State = model.ExamAttemptReady
	suspension := &store.ExamAttemptSuspensionView{ID: suspensionID, AttemptID: f.attemptID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 2,
		State: model.AttemptSuspensionClosed, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: reason, StartedAt: f.at.Add(-time.Minute),
		EndedAt: model.OptionalTimeFrom(f.at), ReallowedByUserID: f.invocation.Principal().UserID}
	return &store.ExamAttemptReallowResult{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, Attempt: attempt, Suspension: suspension, Replayed: replayed}
}

func TestConnectReplayAfterCorrectionAndPauseSuppressesTransientOpenEvent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectionOpened = false
	f.sitting.State = model.ExamSittingPaused
	f.sitting.ExamRevisionID = model.NewExamRevisionID()
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	if err != nil || !result.Replayed || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect" {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestReconnectThatOpensANewConnectionPublishesOneOpenEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	if err != nil || result.FirstAdmission || !result.ConnectionOpened || result.Replayed || f.effects.opened != 1 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestDifferentKeyConvergenceOnExistingOpenConnectionPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	f.persistence.connectionOpened = false
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	if err != nil || result.FirstAdmission || result.ConnectionOpened || result.Replayed || f.effects.opened != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestConnectReplayOfClosedConnectionRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectClosed = true
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectReplayOfEndedParticipationRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.participationEnded = true
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectRejectsAConnectionOutcomeBoundToAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectionSession = model.NewSessionID()
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.unavailable" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectFailureCompletesAuditAndPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.sitting_not_open" ||
		f.audit.failedCode != fault.Code() || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect,audit.fail" {
		t.Fatalf("error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	}
}

func TestExpiredReplayReturnsCandidateSafeConnectionLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.connection_lost" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestProtectedPresentationUsesCurrentRevisionAndSanitizesCandidateMarkdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	currentID := model.NewExamRevisionID()
	f.persistence.presentation = &store.CandidateExamPresentation{
		Capacity:  model.DefaultExamCapacityPolicy(),
		AttemptID: f.attemptID, SittingID: f.sitting.ID,
		RuntimeCapabilities: runtimeCapabilitiesFixture(f, currentID), BrowserPolicy: disabledCandidateBrowserPolicyFixture(currentID),
		Title: "Algorithms", InstructionsMarkdown: "# Rules\nUse **Go**.\n<script>alert('x')</script>\n[bad](javascript:alert(1))\n![tracker](https://example.test/pixel.png)\n[handbook](https://example.test/handbook)",
		Resources: []store.CandidateExamResource{{ResourceID: model.NewExamResourceID(), DisplayName: "Reference",
			DescriptionMarkdown: "Read _carefully_. <iframe src=https://evil.test></iframe> [data](data:text/html,bad)", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 4, SHA256: strings.Repeat("a", 64)}},
	}
	result, err := f.service.GetCandidateExamPresentation(context.Background(), f.invocation, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Capacity != f.persistence.presentation.Capacity ||
		result.RuntimeCapabilities.ExamRevision.AdmissionRevisionID != f.revision.ID ||
		result.RuntimeCapabilities.ExamRevision.CurrentRevisionID != currentID ||
		!result.RuntimeCapabilities.FocusLossCollectionEnabled ||
		!strings.Contains(result.InstructionsMarkdown, "# Rules") || !strings.Contains(result.InstructionsMarkdown, "**Go**") ||
		!strings.Contains(result.InstructionsMarkdown, "[handbook](https://example.test/handbook)") ||
		!strings.Contains(result.Resources[0].DescriptionMarkdown, "_carefully_") {
		t.Fatalf("presentation = %#v", result)
	}
	for _, forbidden := range []string{"<script", "alert('x')", "javascript:", "![tracker]", "pixel.png", "<iframe", "evil.test", "data:text/html"} {
		if strings.Contains(result.InstructionsMarkdown+result.Resources[0].DescriptionMarkdown, forbidden) {
			t.Fatalf("unsafe Markdown survived %q: %#v", forbidden, result)
		}
	}
	if f.persistence.candidateAccess.ContinuityCredentialHash != model.HashToken(credential) ||
		f.persistence.candidateAccess.CandidateUserID != f.userID || f.persistence.candidateAccess.SessionID != f.invocation.Principal().SessionID {
		t.Fatalf("candidate selector = %#v", f.persistence.candidateAccess)
	}
}

func TestProtectedPresentationRejectsInvalidFrozenCapacity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		capacity model.ExamCapacityPolicy
	}{
		{name: "missing"},
		{name: "total below file limit", capacity: model.ExamCapacityPolicy{ResourceMaximumCount: 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 2, WorkspaceMaximumTotalBytes: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.persistence.presentation = &store.CandidateExamPresentation{
				AttemptID: f.attemptID, SittingID: f.sitting.ID, Capacity: test.capacity,
				RuntimeCapabilities: runtimeCapabilitiesFixture(f, f.revision.ID), BrowserPolicy: disabledCandidateBrowserPolicyFixture(f.revision.ID),
			}
			_, err := f.service.GetCandidateExamPresentation(context.Background(), f.invocation, CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()})
			var fault *apperror.Error
			if !errors.As(err, &fault) || fault.Code() != "exam.attempt.unavailable" {
				t.Fatalf("invalid capacity was not rejected: %v", err)
			}
		})
	}
}

func TestStarterOriginWorkspaceFileOpensPinnedStarterBytesWithAttemptObjectIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	entryID := model.NewAttemptWorkspaceEntryID()
	starterID := model.NewStarterWorkspaceObjectID()
	attemptObjectID := model.NewAttemptWorkspaceObjectID()
	version := model.NewWorkspaceContentVersion()
	f.workspace.file = &store.CandidateWorkspaceContent{
		Entry: store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: model.StarterWorkspaceEntryFile,
			Path: "cmd/main.go", MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
		StorageOrigin: model.AttemptWorkspaceStorageStarter, StarterObjectID: starterID,
		AttemptObjectID: attemptObjectID, ContentVersion: version,
	}
	opened, err := f.service.OpenCandidateExamWorkspaceFile(context.Background(), f.invocation, OpenWorkspaceFileQuery{Access: CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	if f.content.starterID != starterID || opened.ContentVersion != version || opened.MediaType != "text/x-go" {
		t.Fatalf("opened=%#v starter=%s", opened, f.content.starterID)
	}
}

func TestProtectedPresentationRejectsAConnectionFromAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.authorizedSession = f.invocation.Principal().SessionID
	principal := f.invocation.Principal()
	principal.SessionID = model.NewSessionID()
	otherSession := invocation.New(principal, f.invocation.RequestMetadata())
	_, err := f.service.GetCandidateExamPresentation(context.Background(), otherSession, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	})
	if !apperror.Is(err, "resource.not_found") || !chainHasCode(err, "exam.attempt.not_found") ||
		f.persistence.candidateAccess.SessionID != principal.SessionID {
		t.Fatalf("error=%v access=%#v", err, f.persistence.candidateAccess)
	}
}

func TestContentSelectorsPreserveCandidateAccessErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	invalidCall := invocation.New(model.Principal{}, model.RequestMetadata{})
	access := CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()}
	for _, open := range []func() error{
		func() error {
			_, err := f.service.OpenCandidateExamResource(context.Background(), invalidCall, OpenResourceQuery{Access: access, ResourceID: model.NewExamResourceID()})
			return err
		},
		func() error {
			_, err := f.service.OpenCandidateExamWorkspaceFile(context.Background(), invalidCall, OpenWorkspaceFileQuery{Access: access, EntryID: model.NewAttemptWorkspaceEntryID()})
			return err
		},
	} {
		var fault *apperror.Error
		if err := open(); !errors.As(err, &fault) || fault.Code() != "authentication.token_invalid" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestWorkspaceListUsesPathFreeSnapshotCursor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID := model.NewExamAttemptWorkspaceID()
	f.workspace.page = &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: 7,
		Items: []store.CandidateAttemptWorkspaceItem{{EntryID: model.NewAttemptWorkspaceEntryID(),
			Kind: model.StarterWorkspaceEntryDirectory, Path: "cmd"}}, HasMore: true}
	page, err := f.service.ListCandidateExamWorkspace(context.Background(), f.invocation, WorkspaceQuery{Access: CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	}, ExpectedCursor: -1, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.WorkspaceID != workspaceID || page.Cursor != 7 || len(page.Items) != 1 || !page.HasMore || page.RefreshRequired ||
		f.workspace.options.ExpectedCursor != -1 || !f.workspace.options.AfterEntryID.IsZero() {
		t.Fatalf("page=%#v options=%#v", page, f.workspace.options)
	}
}

func TestWorkspaceListRejectsUnboundedOrInvalidCursorsBeforeStore(t *testing.T) {
	t.Parallel()
	access := CandidateAccess{ContinuityCredential: model.NewCredentialToken()}
	for _, query := range []WorkspaceQuery{
		{Access: access, ExpectedCursor: -1, Limit: 0},
		{Access: access, ExpectedCursor: -1, Limit: 201},
		{Access: access, ExpectedCursor: -2, Limit: 20},
		{Access: access, ExpectedCursor: -1, AfterEntryID: model.NewAttemptWorkspaceEntryID(), Limit: 20},
	} {
		f := newFixture(t)
		query.Access.AttemptID, query.Access.ConnectionID = f.attemptID, f.connectionID
		_, err := f.service.ListCandidateExamWorkspace(context.Background(), f.invocation, query)
		var fault *apperror.Error
		if !errors.As(err, &fault) || fault.Code() != "exam.attempt.invalid" || f.workspace.lists != 0 {
			t.Fatalf("query=%#v error=%v calls=%d", query, err, f.workspace.lists)
		}
	}
}

func TestWorkspaceJournalReturnsOrderedRecoveryWithoutPathsInSelector(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID, entryID := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID()
	f.workspace.journalPage = &store.CandidateWorkspaceJournalPage{WorkspaceID: workspaceID, CurrentCursor: 5,
		Entries: []model.AttemptWorkspaceJournalEntry{{WorkspaceID: workspaceID, Cursor: 5, EntryID: entryID,
			EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory,
			NewPath: "src", ChangedAt: f.at}}}
	page, err := f.service.ListCandidateExamWorkspaceJournal(context.Background(), f.invocation, WorkspaceJournalQuery{Access: CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	}, AfterCursor: 4, Limit: 20})
	if err != nil || page.CurrentCursor != 5 || len(page.Entries) != 1 || f.workspace.journalOptions.AfterCursor != 4 {
		t.Fatalf("page=%#v options=%#v error=%v", page, f.workspace.journalOptions, err)
	}
}

func TestOpenAttemptOriginWorkspaceFileUsesOpaqueAttemptObject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	entryID, objectID, version := model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceObjectID(), model.NewWorkspaceContentVersion()
	f.workspace.file = &store.CandidateWorkspaceContent{Entry: store.CandidateAttemptWorkspaceItem{EntryID: entryID,
		Kind: model.StarterWorkspaceEntryFile, Path: "answer.txt", ContentVersion: version, MediaType: "text/plain",
		SizeBytes: 4, SHA256: strings.Repeat("c", 64)}, StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: objectID, ContentVersion: version}
	f.content.openAttemptBody = "work"
	opened, err := f.service.OpenCandidateExamWorkspaceFile(context.Background(), f.invocation, OpenWorkspaceFileQuery{Access: CandidateAccess{AttemptID: f.attemptID,
		ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	if f.content.openAttemptID != objectID || opened.ContentVersion != version {
		t.Fatalf("opened=%#v object=%s", opened, f.content.openAttemptID)
	}
}

func TestManagerListAuthorizesBeforeBoundedStoreRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rows := make([]store.ExamAttemptManagerSnapshot, 3)
	for index := range rows {
		attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), f.sitting.ExamID, f.sitting.ID, model.NewUserID(), f.revision.ID, f.at.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		rows[index].Attempt = attempt
	}
	f.persistence.managerRows = rows
	page, err := f.service.ListExamAttempts(context.Background(), f.invocation, ListManagedAttemptsQuery{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !page.HasMore || f.persistence.managerOptions.Limit != 3 ||
		strings.Join(f.order, ",") != "manager.authorize,manager.list" {
		t.Fatalf("page=%#v options=%#v order=%v", page, f.persistence.managerOptions, f.order)
	}
}

func TestManagerListValidatesAndDeepClonesActiveSuspension(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, f.at, 2
	suspension := &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: attempt.ID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 2,
		State: model.AttemptSuspensionActive, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: f.at}
	f.persistence.managerRows = []store.ExamAttemptManagerSnapshot{{Attempt: attempt, ActiveSuspension: suspension}}
	page, err := f.service.ListExamAttempts(context.Background(), f.invocation, ListManagedAttemptsQuery{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].ActiveSuspension == nil || page.Items[0].ActiveSuspension.ID != suspension.ID {
		t.Fatalf("page=%#v error=%v", page, err)
	}
	suspension.ID = model.NewAttemptSuspensionID()
	if page.Items[0].ActiveSuspension.ID == suspension.ID {
		t.Fatal("manager page retained Store-owned Suspension pointer")
	}
}

func TestTrustedCloseCommitsBeforeManagerEffectAndSuppressesNoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	connected, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, credential, "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	f.order = nil
	f.persistence.closeChanged = true
	closed, err := f.service.CloseExamAttemptConnection(context.Background(), f.invocation, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	})
	if err != nil || !closed.Changed || strings.Join(f.order, ",") != "audit,close,effect.close" {
		t.Fatalf("closed=%#v error=%v order=%v", closed, err, f.order)
	}
	f.order = nil
	f.persistence.closeChanged = false
	if _, err = f.service.CloseExamAttemptConnection(context.Background(), f.invocation, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	}); err != nil {
		t.Fatal(err)
	}
	if f.effects.closed != 1 {
		t.Fatalf("close effects = %d", f.effects.closed)
	}
}

func TestTrustedCloseStillFencesCandidateAndSessionOwnership(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*model.Principal){
		func(principal *model.Principal) { principal.UserID = model.NewUserID() },
		func(principal *model.Principal) { principal.SessionID = model.NewSessionID() },
	} {
		f := newFixture(t)
		connected, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
		if err != nil {
			t.Fatal(err)
		}
		principal := f.invocation.Principal()
		mutate(&principal)
		_, err = f.service.CloseExamAttemptConnection(context.Background(), invocation.New(principal, f.invocation.RequestMetadata()), CloseConnectionCommand{
			AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
			ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
		})
		if !apperror.Is(err, "resource.not_found") || !chainHasCode(err, "exam.attempt.not_found") || f.effects.closed != 0 {
			t.Fatalf("error=%v effects=%#v", err, f.effects)
		}
	}
}

func TestTrustedCloseRejectsUnknownReasonBeforeAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.CloseExamAttemptConnection(context.Background(), f.invocation, CloseConnectionCommand{
		AttemptID: model.NewExamAttemptID(), SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: model.NewAttemptConnectionID(), Reason: "unknown",
	})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.attempt.invalid" || len(f.order) != 0 {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
}

type fixture struct {
	service                   *Service
	invocation                invocation.Invocation
	userID                    model.UserID
	attemptID                 model.ExamAttemptID
	connectionID              model.AttemptConnectionID
	at                        time.Time
	sitting                   *model.ExamSitting
	revision                  *model.ExamRevision
	order                     []string
	persistence               *attemptStoreFake
	workspace                 *attemptWorkspaceStoreFake
	submissions               *submissionStoreFake
	mail                      *submissionMailFake
	audit                     *auditFake
	systemAudit               *systemAuditFake
	effects                   *effectsFake
	content                   *contentFake
	managerOverride           bool
	managerErr                error
	focusSignalID             model.FocusLossSignalID
	focusFlagID               model.IntegrityFlagID
	discrepancyID             model.IntegrityDiscrepancyID
	submissionID              model.SubmissionID
	submissionAuthorizationID model.SubmissionID
	configuration             model.AttemptConfiguration
	desktopBuild              model.DesktopBuildTuple
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{userID: model.NewUserID(), attemptID: model.NewExamAttemptID(), connectionID: model.NewAttemptConnectionID(),
		at: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
	f.sitting = &model.ExamSitting{ID: model.NewExamSittingID(), ExamID: model.NewExamID(), ExamRevisionID: model.NewExamRevisionID(),
		ClassID: model.NewClassID(), State: model.ExamSittingOpen, ScheduledStartAt: f.at.Add(-time.Hour), ScheduledEndAt: f.at.Add(time.Hour),
		CreatedAt: f.at.Add(-2 * time.Hour), UpdatedAt: f.at, Revision: 2}
	f.revision = &model.ExamRevision{ID: f.sitting.ExamRevisionID, ExamID: f.sitting.ExamID, StarterWorkspace: []model.ExamRevisionStarterWorkspaceEntry{
		{EntryID: model.NewStarterWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryDirectory, Path: "cmd"},
		{EntryID: model.NewStarterWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "cmd/main.go", ObjectID: model.NewStarterWorkspaceObjectID(),
			ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
	}}
	f.persistence = &attemptStoreFake{f: f, firstAdmission: true, connectionOpened: true}
	f.workspace = &attemptWorkspaceStoreFake{f: f}
	f.submissions = &submissionStoreFake{f: f}
	f.mail = &submissionMailFake{f: f}
	f.audit = &auditFake{f: f}
	f.systemAudit = &systemAuditFake{f: f}
	f.effects = &effectsFake{f: f}
	f.content = &contentFake{f: f}
	f.desktopBuild = model.DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: "attempt-test",
		Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1,
		AttemptConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(),
		DesktopSettingsRegistryFingerprint:      "fnv1a64:" + strings.Repeat("b", 16), DesktopTarget: "darwin-arm64", ConfigurationManifest: model.EmptyAttemptConfigurationManifest(),
		CapabilityMatrixIdentity: "attempt-test-matrix"}
	f.desktopBuild.NativeAgreement = syntheticNativeAgreement(t, f.desktopBuild)
	var configurationErr error
	f.configuration, configurationErr = (model.AttemptConfigurationCandidate{ManifestFingerprint: f.desktopBuild.AttemptConfigurationManifestFingerprint, RegistryFingerprint: f.desktopBuild.DesktopSettingsRegistryFingerprint, UserSettingsRevision: model.NewUserSettingsRevision(), DesktopBuild: f.desktopBuild.DesktopBuildID, DesktopTarget: f.desktopBuild.TargetTuple(), Presentation: model.AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}).Freeze(model.NewId())
	if configurationErr != nil {
		t.Fatal(configurationErr)
	}
	service, err := New(Dependencies{
		Persistence: f.persistence, Workspace: f.workspace, Submissions: f.submissions, Sittings: &sittingFake{f: f}, Managers: &managerFake{f: f},
		Auditor: f.audit, SystemAuditor: f.systemAudit, Effects: f.effects, EffectFailures: f.effects, Content: f.content, Mail: f.mail,
		DesktopBuilds: &desktopBuildResolverFake{f: f},
		Now:           func() time.Time { return f.at }, NewAttemptID: model.NewExamAttemptID, NewWorkspaceID: model.NewExamAttemptWorkspaceID,
		NewParticipation: model.NewAttemptParticipationID, NewConnection: model.NewAttemptConnectionID,
		NewEvidence: model.NewIntegrityEvidenceID, NewFlag: func() model.IntegrityFlagID {
			if f.focusFlagID.IsValid() {
				return f.focusFlagID
			}
			return model.NewIntegrityFlagID()
		}, NewSuspension: model.NewAttemptSuspensionID,
		NewFocusLossSignal: func() model.FocusLossSignalID {
			if f.focusSignalID.IsValid() {
				return f.focusSignalID
			}
			return model.NewFocusLossSignalID()
		},
		NewDiscrepancy: func() model.IntegrityDiscrepancyID {
			if f.discrepancyID.IsValid() {
				return f.discrepancyID
			}
			return model.NewIntegrityDiscrepancyID()
		},
		NewWorkspaceEntry:   func() model.AttemptWorkspaceEntryID { return entryIDOrNew(f.workspace) },
		NewWorkspaceObject:  model.NewAttemptWorkspaceObjectID,
		NewWorkspaceVersion: func() model.WorkspaceContentVersion { return workspaceVersionOrNew(f.workspace) },
		NewSubmission: func() model.SubmissionID {
			if f.submissionID.IsValid() {
				return f.submissionID
			}
			return model.NewSubmissionID()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, DesktopRegistrationID: model.NewDesktopRegistrationID(),
		DPoPKeyThumbprint: strings.Repeat("A", 43), RegisteredDesktopKey: true,
		DesktopRelease: "1.0.0", DesktopBuildID: "attempt-test", DesktopPlatform: model.DesktopPlatformDarwin,
		DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		AuthenticatedAt: f.at}, model.RequestMetadata{RequestID: "attempt-test"})
	return f
}

func validConnectCommand(f *fixture, credential, key string) ConnectCommand {
	configuration := f.configuration.Clone()
	return ConnectCommand{Security: model.ConnectSecurity{Kind: "preflight", PreflightID: "fixture-preflight", ReportDigest: model.SHA256Fingerprint([]byte("fixture-report"))}, SittingID: f.sitting.ID, ContinuityCredential: credential,
		ConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(),
		InitialConfiguration:             &configuration.AttemptConfigurationCandidate, IdempotencyKey: key}
}

func useManagerCall(f *fixture) model.UserID {
	principal := f.invocation.Principal()
	principal.UserID = model.NewUserID()
	f.invocation = invocation.New(principal, f.invocation.RequestMetadata())
	return principal.UserID
}

func managerAttemptFixture(t *testing.T, f *fixture, state model.ExamAttemptState, revision int64,
	suspensionID model.AttemptSuspensionID,
) (*model.ExamAttempt, *store.ExamAttemptSuspensionView) {
	t.Helper()
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID,
		f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempt.State, attempt.UpdatedAt, attempt.Revision = state, f.at, revision
	if err = attempt.Validate(); err != nil {
		t.Fatal(err)
	}
	if state != model.ExamAttemptSuspended {
		return attempt, nil
	}
	suspension := &store.ExamAttemptSuspensionView{ID: suspensionID, AttemptID: attempt.ID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 1,
		State: model.AttemptSuspensionActive, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: f.at.Add(-time.Minute)}
	return attempt, suspension
}

func runtimeCapabilitiesFixture(f *fixture, currentRevisionID model.ExamRevisionID) store.CandidateRuntimeCapabilities {
	configuration := f.configuration.Clone()
	return store.CandidateRuntimeCapabilities{SchemaVersion: 1, ServerTime: f.at,
		InteractionState: store.CandidateInteractionInteractive,
		AttemptConfiguration: store.CandidateAttemptConfiguration{Revision: configuration.Revision, Presentation: configuration.Presentation,
			ApprovedCommands: append([]string{}, configuration.ApprovedCommands...), ApprovedKeybindings: append([]string{}, configuration.ApprovedKeybindings...), Digest: configuration.Digest},
		FocusLossCollectionEnabled: true, WorkspaceMutationAllowed: true, SubmissionAllowed: true,
		Browser: store.CandidateBrowserCapability{State: store.CandidateBrowserDisabled},
		ExamRevision: store.CandidateExamRevisionCapability{AdmissionRevisionID: f.revision.ID,
			CurrentRevisionID: currentRevisionID},
		Departure: store.CandidateDepartureCapability{Allowed: false, Reason: "attempt_in_progress"}}
}

type desktopBuildResolverFake struct{ f *fixture }

func (f *desktopBuildResolverFake) ResolveAttemptDesktopBuild(context.Context, model.Principal) (DesktopBuildResolution, error) {
	return DesktopBuildResolution{Build: f.f.desktopBuild, CompatibilityPolicyRevision: 1}, nil
}

type sittingFake struct{ f *fixture }

func (f *sittingFake) Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	f.f.order = append(f.f.order, "sitting")
	return &store.ExamSittingSnapshot{Sitting: f.f.sitting}, nil
}

type managerFake struct{ f *fixture }

func (f *managerFake) AuthorizeSittingView(context.Context, invocation.Invocation, model.ExamSittingID) error {
	f.f.order = append(f.f.order, "manager.authorize")
	return f.f.managerErr
}
func (f *managerFake) AuthorizeSittingManage(context.Context, invocation.Invocation, model.ExamSittingID) (bool, error) {
	f.f.order = append(f.f.order, "manager.manage")
	return f.f.managerOverride, f.f.managerErr
}
func (f *managerFake) AuthorizeSubmissionView(_ context.Context, _ invocation.Invocation, submissionID model.SubmissionID) error {
	f.f.order = append(f.f.order, "submission.authorize")
	f.f.submissionAuthorizationID = submissionID
	return nil
}
func (f *managerFake) AuthorizeBrowserActivityView(context.Context, invocation.Invocation, model.ExamSittingID) (model.AcademicUnitID, error) {
	f.f.order = append(f.f.order, "browser.authorize")
	return model.NewAcademicUnitID(), f.f.managerErr
}

type auditFake struct {
	f          *fixture
	values     map[string]any
	failedCode string
	action     model.Action
	resource   model.Resource
	scopeType  model.RoleScopeType
	scopeID    string
	operation  string
	failErr    error
}

type systemAuditFake struct {
	f          *fixture
	values     map[string]any
	failedCode string
}

func (f *systemAuditFake) Begin(_ context.Context, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, values map[string]any) (model.AuditEventID, error) {
	f.f.order = append(f.f.order, "system.audit")
	f.values = values
	return model.NewAuditEventID(), nil
}
func (f *systemAuditFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	f.f.order = append(f.f.order, "system.audit.fail")
	f.failedCode = code
	return nil
}

func (f *auditFake) BeginAtScope(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, values, _ map[string]any) (model.AuditEventID, error) {
	f.f.order = append(f.f.order, "audit")
	f.values = values
	f.action, f.resource, f.scopeType, f.scopeID, f.operation = action, resource, scopeType, scopeID, operation
	return model.NewAuditEventID(), nil
}
func (f *auditFake) PrepareAtScope(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource,
	scopeType model.RoleScopeType, scopeID, operation string, values map[string]any,
) (*model.AuditEvent, error) {
	f.f.order = append(f.f.order, "audit.prepare")
	f.values = values
	f.action, f.resource, f.scopeType, f.scopeID, f.operation = action, resource, scopeType, scopeID, operation
	return &model.AuditEvent{Action: string(action), Resource: resource, ScopeType: scopeType, ScopeID: scopeID,
		Status: model.AuditStatusAttempt, NodeID: "test", Parameters: []byte(`{"operation":"` + operation + `"}`)}, nil
}
func (f *auditFake) RecordAuthorizationDecision(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource,
	_ model.RoleScopeType, _ string, _ bool,
) error {
	f.f.order = append(f.f.order, "audit.authorization")
	return nil
}
func (f *auditFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	f.f.order = append(f.f.order, "audit.fail")
	f.failedCode = code
	return f.failErr
}
func (f *auditFake) Complete(_ context.Context, _ model.AuditEventID, _ map[string]any) error {
	f.f.order = append(f.f.order, "audit.complete")
	return nil
}

type effectsFake struct {
	f                *fixture
	opened, closed   int
	renewed          int
	expired          int
	reallowed        int
	workspaceChanged int
	focusLoss        int
	submitted        int
	kicked           int
}

func (f *effectsFake) BrowserActivityGapChanged(context.Context, model.ExamID, model.ExamSittingID) error {
	f.f.order = append(f.f.order, "effect.browser_gap")
	return nil
}

func (f *effectsFake) ConnectionOpened(context.Context, ConnectionResult) error {
	f.f.order = append(f.f.order, "effect.open")
	f.opened++
	return nil
}
func (f *effectsFake) ConnectionClosed(context.Context, ConnectionClosedResult) error {
	f.f.order = append(f.f.order, "effect.close")
	f.closed++
	return nil
}
func (f *effectsFake) ParticipationRenewed(context.Context, ParticipationRenewal) error {
	f.f.order = append(f.f.order, "effect.renew")
	f.renewed++
	return nil
}
func (f *effectsFake) ParticipationExpired(context.Context, ParticipationExpiry) error {
	f.f.order = append(f.f.order, "effect.expire")
	f.expired++
	return nil
}
func (f *effectsFake) AttemptReallowed(context.Context, ReallowResult) error {
	f.f.order = append(f.f.order, "effect.reallow")
	f.reallowed++
	return nil
}
func (f *effectsFake) WorkspaceChanged(context.Context, WorkspaceMutationResult) error {
	f.f.order = append(f.f.order, "effect.workspace")
	f.workspaceChanged++
	return nil
}
func (f *effectsFake) FocusLossEvaluated(context.Context, FocusLossEvaluation) error {
	f.f.order = append(f.f.order, "effect.focus_loss")
	f.focusLoss++
	return nil
}
func (f *effectsFake) AttemptSubmitted(context.Context, SubmissionResult) error {
	f.f.order = append(f.f.order, "effect.submit")
	f.submitted++
	return nil
}
func (f *effectsFake) AttemptSealedForSittingClose(context.Context, AutomaticSubmissionResult) error {
	f.f.order = append(f.f.order, "effect.automatic_submit")
	f.submitted++
	return nil
}
func (f *effectsFake) Report(_ context.Context, kind string, _ error) {
}

type contentFake struct {
	f               *fixture
	starterID       model.StarterWorkspaceObjectID
	attemptObjectID model.AttemptWorkspaceObjectID
	attemptContent  *model.AttemptWorkspaceContent
	openAttemptID   model.AttemptWorkspaceObjectID
	openAttemptBody string
}

func (f *contentFake) StageAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID, _ io.Reader, _ int64, _ string) (*model.AttemptWorkspaceContent, error) {
	f.f.order = append(f.f.order, "content.stage")
	f.attemptObjectID = id
	return f.attemptContent, nil
}
func (f *contentFake) OpenAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	f.openAttemptID = id
	return io.NopCloser(strings.NewReader(f.openAttemptBody)), nil
}

func (*contentFake) OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("resource")), nil
}
func (f *contentFake) OpenStarterWorkspaceObject(_ context.Context, id model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	f.starterID = id
	return io.NopCloser(strings.NewReader("workspace")), nil
}

type attemptStoreFake struct {
	f                               *fixture
	connect                         *store.ExamAttemptConnect
	connectErr                      error
	getErr                          error
	replayed                        bool
	firstAdmission                  bool
	connectionOpened                bool
	connectClosed                   bool
	participationEnded              bool
	connectionSession               model.SessionID
	closeChanged                    bool
	attempt                         *model.ExamAttempt
	workspace                       *model.ExamAttemptWorkspace
	participation                   *store.ExamAttemptParticipationView
	connection                      *model.AttemptConnection
	presentation                    *store.CandidateExamPresentation
	candidateAccess                 store.CandidateAttemptAccess
	authorizedSession               model.SessionID
	managerOptions                  store.ExamAttemptManagerListOptions
	managerRows                     []store.ExamAttemptManagerSnapshot
	managerSuspension               *store.ExamAttemptSuspensionView
	renew                           *store.ExamAttemptParticipationRenewal
	renewResult                     *store.ExamAttemptParticipationRenewalResult
	renewErr                        error
	focusAccess                     store.ExamAttemptFocusLossAccess
	focusTarget                     *store.ExamAttemptFocusLossTarget
	focusSignal                     *store.ExamAttemptFocusLossSignal
	focusResult                     *store.ExamAttemptFocusLossResult
	focusErr                        error
	lateFocusTarget                 *store.ExamAttemptFocusLossDiscrepancyTarget
	lateFocusInput                  *store.ExamAttemptFocusLossDiscrepancy
	lateFocusResult                 *store.ExamAttemptFocusLossDiscrepancyResult
	lateFocusErr                    error
	expiryDue                       []store.ExamAttemptParticipationExpiryDue
	resolvedExpiry                  *store.ExamAttemptParticipationExpiryDue
	resolveExpiryErr                error
	expireInput                     *store.ExamAttemptParticipationExpiry
	expireResult                    *store.ExamAttemptParticipationExpiryResult
	expireErr                       error
	reallow                         *store.ExamAttemptReallow
	reallowResult                   *store.ExamAttemptReallowResult
	reallowErr                      error
	manualSuspension                *store.ExamAttemptManualSuspension
	manualSuspensionResult          *store.ExamAttemptManualSuspensionResult
	manualSuspensionErr             error
	correctionAcknowledgementTarget *store.ExamAttemptCorrectionAcknowledgementTarget
	correctionAcknowledgement       *store.ExamAttemptCorrectionAcknowledgement
	correctionAcknowledgementResult *store.ExamAttemptCorrectionAcknowledgementResult
	correctionAcknowledgementErr    error
	correctionAcknowledgementCalls  int
	idempotency                     *store.CommandIdempotency
}

type attemptWorkspaceStoreFake struct {
	f               *fixture
	resolvedAccess  store.ExamAttemptWorkspaceMutationAccess
	target          *store.ExamAttemptWorkspaceMutationTarget
	mutation        *store.ExamAttemptWorkspaceMutation
	mutationResult  *store.ExamAttemptWorkspaceMutationResult
	mutationErr     error
	reservation     *store.ExamAttemptWorkspaceObjectReservation
	ready           *store.ExamAttemptWorkspaceObjectReady
	reclaimable     []model.AttemptWorkspaceObjectID
	lists           int
	options         store.CandidateWorkspaceListOptions
	page            *store.CandidateAttemptWorkspacePage
	file            *store.CandidateWorkspaceContent
	journalOptions  store.CandidateWorkspaceJournalOptions
	journalPage     *store.CandidateWorkspaceJournalPage
	proposedEntryID model.AttemptWorkspaceEntryID
	proposedVersion model.WorkspaceContentVersion
	idempotency     *store.CommandIdempotency
}

func entryIDOrNew(fake *attemptWorkspaceStoreFake) model.AttemptWorkspaceEntryID {
	if fake.proposedEntryID.IsValid() {
		return fake.proposedEntryID
	}
	result := fake.mutationResult
	if result != nil && result.Entry != nil && result.Entry.EntryID.IsValid() {
		return result.Entry.EntryID
	}
	return model.NewAttemptWorkspaceEntryID()
}

func workspaceVersionOrNew(fake *attemptWorkspaceStoreFake) model.WorkspaceContentVersion {
	if fake.proposedVersion.IsValid() {
		return fake.proposedVersion
	}
	result := fake.mutationResult
	if result != nil && result.Change.ContentVersion.IsValid() {
		return result.Change.ContentVersion
	}
	return model.NewWorkspaceContentVersion()
}

func resultVersion(result *store.ExamAttemptWorkspaceMutationResult) model.WorkspaceContentVersion {
	if result == nil {
		return ""
	}
	return result.Change.ContentVersion
}

func validWorkspaceMutationAccess(f *fixture) WorkspaceMutationAccess {
	return WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID,
		ContinuityCredential: model.NewCredentialToken()}, ParticipationID: model.NewAttemptParticipationID(), Generation: 1}
}

func workspaceMutationResultFixture(f *fixture, workspaceID model.ExamAttemptWorkspaceID, entryID model.AttemptWorkspaceEntryID,
	kind model.StarterWorkspaceEntryKind, operation model.AttemptWorkspaceMutationKind, oldPath, newPath string,
	version model.WorkspaceContentVersion, replayed bool,
) *store.ExamAttemptWorkspaceMutationResult {
	path := newPath
	if path == "" {
		path = oldPath
	}
	entry := &store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: kind, Path: path}
	if kind == model.StarterWorkspaceEntryFile {
		entry.ContentVersion, entry.MediaType, entry.SizeBytes, entry.SHA256 = version, "text/plain", 1, strings.Repeat("a", 64)
	}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 2, EntryID: entryID, EntryKind: kind,
		Operation: operation, OldPath: oldPath, NewPath: newPath, ContentVersion: version, ChangedAt: f.at}
	if operation == model.AttemptWorkspaceMutationDeleteEntry {
		change.ContentVersion = ""
	}
	return &store.ExamAttemptWorkspaceMutationResult{SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, WorkspaceID: workspaceID, Entry: entry, Change: change, Replayed: replayed}
}

func (f *attemptWorkspaceStoreFake) List(_ context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	f.lists++
	f.options = options
	return f.page, nil
}
func (f *attemptWorkspaceStoreFake) ResolveFile(context.Context, store.CandidateAttemptAccess, model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	return f.file, nil
}
func (f *attemptWorkspaceStoreFake) ListJournal(_ context.Context, options store.CandidateWorkspaceJournalOptions) (*store.CandidateWorkspaceJournalPage, error) {
	f.journalOptions = options
	return f.journalPage, nil
}
func (f *attemptWorkspaceStoreFake) ResolveMutationTarget(_ context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	f.f.order = append(f.f.order, "workspace.resolve")
	f.resolvedAccess = access
	return f.target, nil
}
func (f *attemptWorkspaceStoreFake) ReserveObject(_ context.Context, input *store.ExamAttemptWorkspaceObjectReservation) (*model.AttemptWorkspaceObject, error) {
	f.f.order = append(f.f.order, "workspace.reserve")
	f.reservation = input
	return model.NewStagedAttemptWorkspaceObject(input.ObjectID, f.target.WorkspaceID, f.f.at, f.f.at.Add(time.Hour))
}
func (f *attemptWorkspaceStoreFake) MarkObjectReady(_ context.Context, input *store.ExamAttemptWorkspaceObjectReady) (*model.AttemptWorkspaceObject, error) {
	f.f.order = append(f.f.order, "workspace.ready")
	f.ready = input
	object, err := model.NewStagedAttemptWorkspaceObject(input.ObjectID, f.target.WorkspaceID, f.f.at, f.f.at.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	if err = object.MarkContentReady(input.ContentVersion, input.Content.MediaType, input.Content.SizeBytes, input.Content.SHA256, f.f.at.Add(time.Second)); err != nil {
		return nil, err
	}
	return object, nil
}
func (f *attemptWorkspaceStoreFake) ApplyMutation(_ context.Context, mutation *store.ExamAttemptWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutationResult, error) {
	f.f.order = append(f.f.order, "workspace.apply")
	f.mutation, f.idempotency = mutation, command
	return f.mutationResult, f.mutationErr
}

func (f *attemptWorkspaceStoreFake) MarkObjectReclaimable(_ context.Context, id model.AttemptWorkspaceObjectID) error {
	f.reclaimable = append(f.reclaimable, id)
	return nil
}
func (*attemptWorkspaceStoreFake) ClaimObjectsForCleanup(context.Context, int, string) ([]model.AttemptWorkspaceObject, error) {
	return nil, nil
}
func (*attemptWorkspaceStoreFake) CompleteObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error {
	return nil
}
func (*attemptWorkspaceStoreFake) ReleaseObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error {
	return nil
}

func (f *attemptStoreFake) Connect(_ context.Context, input *store.ExamAttemptConnect, command *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	f.f.order = append(f.f.order, "connect")
	f.connect, f.idempotency = input, command
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	var err error
	f.attempt, err = model.NewExamAttempt(input.AttemptID, f.f.sitting.ExamID, input.SittingID, input.CandidateUserID, f.f.revision.ID, f.f.at)
	if err != nil {
		return nil, err
	}
	f.workspace, err = model.NewExamAttemptWorkspace(input.WorkspaceID, input.AttemptID, f.f.at)
	if err != nil {
		return nil, err
	}
	f.participation = &store.ExamAttemptParticipationView{ID: input.ParticipationID, AttemptID: input.AttemptID,
		State: model.AttemptParticipationActive, Generation: 1, StartedAt: f.f.at, UpdatedAt: f.f.at,
		LeaseExpiresAt: f.f.at.Add(model.AttemptParticipationInitialLease)}
	if f.participationEnded {
		f.participation.State = model.AttemptParticipationEnded
		f.participation.UpdatedAt = f.f.at.Add(time.Second)
		f.participation.EndedAt = model.OptionalTimeFrom(f.f.at.Add(time.Second))
		f.participation.EndReason = model.AttemptParticipationEndInterrupted
	}
	connectionSession := input.SessionID
	if f.connectionSession.IsValid() {
		connectionSession = f.connectionSession
	}
	f.connection, err = model.NewAttemptConnection(input.ConnectionID, input.AttemptID, input.ParticipationID, connectionSession, f.f.at)
	if err != nil {
		return nil, err
	}
	if f.connectClosed {
		if err = f.connection.Close(model.AttemptConnectionCloseTransport, f.f.at.Add(time.Second)); err != nil {
			return nil, err
		}
	}
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: "fixture-policy", Revision: f.f.revision.ID.String(), Ordinal: 1, InstitutionID: model.NewInstitutionID(), ExamRevisionID: f.f.revision.ID, SittingID: f.f.sitting.ID, Scope: model.SecurityPolicyScope{Kind: "admission", AdmissionScopeID: input.Security.PreflightID}, IssuedAt: f.f.at, ActiveFrom: f.f.at, ActivationTime: f.f.at, Selections: model.DefaultExamPolicySet(), Build: f.f.desktopBuild})
	if err != nil {
		return nil, err
	}
	policy, err := resolved.Policy.RebindAttempt(input.AttemptID, f.f.at)
	if err != nil {
		return nil, err
	}
	security := model.AdmittedSecurity{Policy: policy, PolicyContentDigest: resolved.PolicyContentDigest, PreflightID: input.Security.PreflightID, PreflightReportDigest: input.Security.ReportDigest, PreflightPolicyDigest: resolved.Policy.Digest, ParticipationID: input.ParticipationID, Generation: 1, SecuritySessionID: "fixture-security-session", DeliveryStreamID: "fixture-delivery", RenewalIntervalSeconds: 5}
	configuration := f.f.configuration.Clone()
	return &store.ExamAttemptConnectResult{Security: security, Attempt: f.attempt, Workspace: f.workspace, Participation: f.participation,
		Connection: f.connection, ClassID: f.f.sitting.ClassID, FirstAdmission: f.firstAdmission,
		Configuration: configuration, RuntimeCapabilities: runtimeCapabilitiesFixture(f.f, f.f.sitting.ExamRevisionID), BrowserPolicy: disabledCandidateBrowserPolicyFixture(f.f.sitting.ExamRevisionID),
		ConnectionOpened: f.connectionOpened, Replayed: f.replayed}, nil
}
func (f *attemptStoreFake) RenewParticipation(_ context.Context, input *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	f.f.order = append(f.f.order, "renew")
	f.renew = input
	return f.renewResult, f.renewErr
}
func (f *attemptStoreFake) ResolveFocusLossTarget(_ context.Context, access store.ExamAttemptFocusLossAccess) (*store.ExamAttemptFocusLossTarget, error) {
	f.f.order = append(f.f.order, "focus.resolve")
	f.focusAccess = access
	return f.focusTarget, f.focusErr
}
func (f *attemptStoreFake) RecordFocusLoss(_ context.Context, input *store.ExamAttemptFocusLossSignal) (*store.ExamAttemptFocusLossResult, error) {
	f.f.order = append(f.f.order, "focus.record")
	f.focusSignal = input
	return f.focusResult, f.focusErr
}
func (f *attemptStoreFake) ResolveEndedFocusLossTarget(_ context.Context, access store.ExamAttemptFocusLossAccess) (*store.ExamAttemptFocusLossDiscrepancyTarget, error) {
	f.f.order = append(f.f.order, "focus.late.resolve")
	f.focusAccess = access
	return f.lateFocusTarget, f.lateFocusErr
}
func (f *attemptStoreFake) RecordEndedFocusLoss(_ context.Context, input *store.ExamAttemptFocusLossDiscrepancy) (*store.ExamAttemptFocusLossDiscrepancyResult, error) {
	f.f.order = append(f.f.order, "focus.late.record")
	f.lateFocusInput = input
	return f.lateFocusResult, f.lateFocusErr
}
func (f *attemptStoreFake) ResolveParticipationExpiry(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64) (*store.ExamAttemptParticipationExpiryDue, error) {
	f.f.order = append(f.f.order, "expiry.resolve")
	return f.resolvedExpiry, f.resolveExpiryErr
}
func (f *attemptStoreFake) ListExpiredParticipations(_ context.Context, _ int) ([]store.ExamAttemptParticipationExpiryDue, error) {
	f.f.order = append(f.f.order, "expiry.list")
	return f.expiryDue, nil
}
func (f *attemptStoreFake) ExpireParticipation(_ context.Context, input *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	f.f.order = append(f.f.order, "expiry.commit")
	f.expireInput = input
	return f.expireResult, f.expireErr
}
func (f *attemptStoreFake) ReallowAttempt(_ context.Context, input *store.ExamAttemptReallow, command *store.CommandIdempotency) (*store.ExamAttemptReallowResult, error) {
	f.f.order = append(f.f.order, "reallow")
	f.reallow, f.idempotency = input, command
	return f.reallowResult, f.reallowErr
}
func (f *attemptStoreFake) SuspendAttempt(_ context.Context, input *store.ExamAttemptManualSuspension, command *store.CommandIdempotency) (*store.ExamAttemptManualSuspensionResult, error) {
	f.f.order = append(f.f.order, "suspend")
	f.manualSuspension, f.idempotency = input, command
	return f.manualSuspensionResult, f.manualSuspensionErr
}
func (f *attemptStoreFake) CloseConnection(_ context.Context, input *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	f.f.order = append(f.f.order, "close")
	if input.CandidateUserID != f.connect.CandidateUserID || input.SessionID != f.connect.SessionID {
		return nil, store.NewErrNotFound("attempt_connection", input.ConnectionID.String())
	}
	connection := *f.connection
	if err := connection.Close(input.Reason, f.f.at.Add(time.Second)); err != nil {
		return nil, err
	}
	return &store.ExamAttemptConnectionCloseResult{AttemptID: f.attempt.ID, ExamID: f.attempt.ExamID, SittingID: f.connect.SittingID,
		CandidateUserID: f.connect.CandidateUserID, Connection: &connection, Changed: f.closeChanged}, nil
}
func (f *attemptStoreFake) Get(_ context.Context, examID model.ExamID, attemptID model.ExamAttemptID) (*store.ExamAttemptManagerSnapshot, error) {
	f.f.order = append(f.f.order, "manager.get")
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.attempt == nil || f.attempt.ExamID != examID || f.attempt.ID != attemptID {
		return nil, errors.New("not configured")
	}
	return &store.ExamAttemptManagerSnapshot{Attempt: f.attempt, ActiveSuspension: f.managerSuspension}, nil
}
func (f *attemptStoreFake) List(_ context.Context, options store.ExamAttemptManagerListOptions) ([]store.ExamAttemptManagerSnapshot, error) {
	f.f.order = append(f.f.order, "manager.list")
	f.managerOptions = options
	return f.managerRows, nil
}

func (f *attemptStoreFake) ListCandidateActivity(context.Context, store.CandidateExamActivityListOptions) (*store.CandidateExamActivityPage, error) {
	return &store.CandidateExamActivityPage{ServerTime: model.NowUTC(), Items: []store.CandidateExamActivityItem{}}, nil
}

func (f *attemptStoreFake) ListSittingCandidateStatuses(context.Context, store.SittingCandidateStatusListOptions) (*store.SittingCandidateStatusPage, error) {
	return &store.SittingCandidateStatusPage{Items: []store.SittingCandidateStatusItem{}}, nil
}
func (*attemptStoreFake) ListSessionRevocationInvalidationTargets(
	context.Context,
	model.UserID,
	[]model.SessionID,
) ([]store.ExamAttemptInvalidationTarget, error) {
	return nil, nil
}
func (f *attemptStoreFake) GetCandidatePresentation(_ context.Context, access store.CandidateAttemptAccess) (*store.CandidateExamPresentation, error) {
	f.candidateAccess = access
	if f.authorizedSession.IsValid() && access.SessionID != f.authorizedSession {
		return nil, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	return f.presentation, nil
}

func (f *attemptStoreFake) StartBrowserActivity(context.Context, *store.BrowserActivitySourceStart) (*model.BrowserSourceStatus, error) {
	return nil, nil
}
func (f *attemptStoreFake) AppendBrowserActivity(context.Context, *store.BrowserActivityAppend) (*model.BrowserActivityAcknowledgement, error) {
	return nil, nil
}
func (f *attemptStoreFake) ListBrowserActivity(context.Context, store.BrowserActivityListOptions) ([]store.BrowserActivityRecord, error) {
	return nil, nil
}
func (f *attemptStoreFake) ResolveCorrectionAcknowledgementTarget(_ context.Context, input store.ExamAttemptCorrectionAcknowledgement) (*store.ExamAttemptCorrectionAcknowledgementTarget, error) {
	f.f.order = append(f.f.order, "correction.acknowledgement.resolve")
	f.correctionAcknowledgement = &input
	return f.correctionAcknowledgementTarget, f.correctionAcknowledgementErr
}
func (f *attemptStoreFake) AcknowledgeCorrection(_ context.Context, input *store.ExamAttemptCorrectionAcknowledgement, command *store.CommandIdempotency) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
	f.f.order = append(f.f.order, "correction.acknowledgement.commit")
	f.correctionAcknowledgementCalls++
	f.correctionAcknowledgement, f.idempotency = input, command
	return f.correctionAcknowledgementResult, f.correctionAcknowledgementErr
}
func (*attemptStoreFake) ResolveCandidateResource(context.Context, store.CandidateAttemptAccess, model.ExamResourceID) (*store.CandidateResourceContent, error) {
	return nil, errors.New("not configured")
}

func (f *attemptStoreFake) PrepareSecurityPreflight(context.Context, *store.SecurityPreflightPrepare, *store.CommandIdempotency) (*store.SecurityPreflightPrepared, error) {
	return nil, errors.New("unexpected preflight preparation")
}
func (f *attemptStoreFake) ReportSecurityPreflight(context.Context, *store.SecurityPreflightReport, *store.CommandIdempotency) (*model.SecurityPreflightResult, error) {
	return nil, errors.New("unexpected preflight report")
}

func (f *attemptStoreFake) ResolveSecurityPreflightSitting(context.Context, string, model.UserID, model.SessionID) (model.ExamSittingID, error) {
	return "", errors.New("unexpected preflight lookup")
}

func (f *attemptStoreFake) RecoverSecurityPolicy(context.Context, store.SecurityPreflightAccess, model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	return nil, errors.New("unexpected security recovery")
}

func syntheticNativeAgreement(t *testing.T, build model.DesktopBuildTuple) *model.DesktopNativeAgreement {
	t.Helper()
	digest := model.SHA256Fingerprint([]byte("synthetic-source-schema"))
	definitions := []model.NativeCoverageDefinition{}
	matrix := model.NativeCapabilityMatrix{RegistryDigest: model.NativeRegistryDigest, MatrixID: build.CapabilityMatrixIdentity, ReleaseID: "synthetic-release", TargetTuple: build.DesktopTarget}
	for _, item := range []struct {
		key    string
		source model.NativeSourceID
	}{{"baseline.capture", model.NativeSourceCapture}, {"baseline.display", model.NativeSourceDisplay}, {"baseline.window", model.NativeSourceWindow}} {
		definitions = append(definitions, model.NativeCoverageDefinition{CoverageKey: item.key, CapabilityID: "baseline", SourceID: item.source, SourceSchemaDigest: digest, PermittedClaims: []model.NativeCapabilityClaim{model.NativeClaimEnforce}, RequiredPermissions: []string{}, Baseline: true})
		matrix.Entries = append(matrix.Entries, model.NativeCapabilityMatrixEntry{CoverageKey: item.key, SourceSchemaDigest: digest, Claim: model.NativeClaimEnforce, ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: "passed"})
	}
	agreement, err := model.NewDesktopNativeAgreement(model.NativeRegistryDigest, model.SHA256Fingerprint([]byte("synthetic-manifest")), model.SHA256Fingerprint([]byte("synthetic-matrix")), model.SHA256Fingerprint([]byte("synthetic-detectors")), definitions, matrix, []model.NativeDetectorDefinition{})
	if err != nil {
		t.Fatal(err)
	}
	return agreement
}

func (f *attemptStoreFake) UpdateSecurityCoverage(context.Context, *store.ExamAttemptSecurityCoverageUpdate) (model.SecurityCoverageResult, error) {
	return model.SecurityCoverageResult{}, errors.New("unexpected security update")
}

func renewalCoverageFixture() model.SecurityCoverageRenewal {
	return model.SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: model.SHA256Fingerprint([]byte("renewal-policy")), SecuritySessionID: model.NewId(), StreamID: model.NewId(), Posture: "compliant", Sources: []model.NativeSourceCoverage{}, Coverage: []model.NativeCoverageClaim{}, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
}
func renewalCoverageResultFixture() model.SecurityCoverageResult {
	digest := model.SHA256Fingerprint([]byte("renewal-control"))
	return model.SecurityCoverageResult{ProcessedControlSequence: 1, ProcessedControlDigest: &digest, CoverageResult: "accepted", SourceResetReceipts: []model.SourceResetReceipt{}, SecurityInteractionAllowed: true, DeliveryWatermarkRejections: []model.DeliveryWatermarkRejection{}}
}

func (f *attemptStoreFake) NativeDeliveryStatus(context.Context, store.NativeDeliveryAccess) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected NativeDeliveryStatus")
}

func (f *attemptStoreFake) NativeDeliveryReceipt(context.Context, store.NativeDeliveryAccess, int64) (*model.NativeBatchReceipt, error) {
	return nil, errors.New("unexpected NativeDeliveryReceipt")
}

func (f *attemptStoreFake) DeclareNativeDeliveryGaps(context.Context, *store.NativeDeliveryGapDeclaration, *store.CommandIdempotency) (*model.DeliveryGapReceipt, error) {
	return nil, errors.New("unexpected DeclareNativeDeliveryGaps")
}

func (f *attemptStoreFake) SealNativeDelivery(context.Context, *store.NativeDeliveryFinalDeclaration, *store.CommandIdempotency) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected SealNativeDelivery")
}

func (f *attemptStoreFake) UpdateNativeDeliverySummary(context.Context, *store.NativeDeliverySummaryUpdate, *store.CommandIdempotency) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected UpdateNativeDeliverySummary")
}

func (f *attemptStoreFake) ResolveNativeDeliveryTarget(context.Context, store.NativeDeliveryAccess) (*store.NativeDeliveryTarget, error) {
	return nil, errors.New("unexpected native delivery target")
}

func (f *desktopBuildResolverFake) ResolveNativeDeliveryBuild(context.Context, NativeDeliveryBuildIdentity) (model.DesktopBuildTuple, error) {
	return f.f.desktopBuild, nil
}

func (f *attemptStoreFake) AppendNativeDelivery(context.Context, *store.NativeDeliveryAppend, *store.CommandIdempotency) (*model.NativeSecurityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendNativeDelivery")
}

func disabledCandidateBrowserPolicyFixture(revisionID model.ExamRevisionID) *store.CandidateBrowserPolicy {
	policy := model.DisabledBrowserPolicy()
	digest, _ := model.BrowserPolicyDigest(policy)
	return &store.CandidateBrowserPolicy{Policy: policy, PolicyRevisionID: revisionID, PolicyRevisionNumber: 1, PolicyDigest: digest,
		BrowserActivityDisclosure: model.BrowserActivityDisclosure{NoticeID: "integrated_browser_activity", Audience: "exam_managers_with_browser_activity_permission", RetentionPolicyRevision: 1, RetentionAnchor: "eligible_closed_sitting_records_completion"}}
}

func (f *attemptStoreFake) BrowserSourceStatus(context.Context, store.BrowserDeliveryAccess) (*model.BrowserSourceStatus, error) {
	return nil, errors.New("unexpected browser source query")
}
func (f *attemptStoreFake) BrowserSourceList(context.Context, store.BrowserDeliveryAccess) ([]model.BrowserSourceStatus, error) {
	return nil, errors.New("unexpected browser source query")
}

func (f *attemptStoreFake) ResolveLiveDeliveryTarget(context.Context, store.CandidateAttemptAccess) (*store.DeliveryOwnerTarget, error) {
	return nil, errors.New("unexpected browser source target")
}

func (f *attemptStoreFake) AppendHistoricalBrowserDelivery(context.Context, *store.BrowserActivityAppend, *store.CommandIdempotency) (*model.BrowserActivityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendHistoricalBrowserDelivery")
}
func (f *attemptStoreFake) ResolveBrowserDeliveryTarget(context.Context, store.BrowserDeliveryAccess) (*store.DeliveryOwnerTarget, error) {
	return nil, errors.New("unexpected ResolveBrowserDeliveryTarget")
}
func (f *attemptStoreFake) BrowserDeliveryReceipts(context.Context, store.BrowserDeliveryAccess, int64, int) (*model.BrowserReceiptPage, error) {
	return nil, errors.New("unexpected BrowserDeliveryReceipts")
}
func (f *attemptStoreFake) DeclareBrowserDeliveryGaps(context.Context, *store.BrowserDeliveryGapDeclaration, *store.CommandIdempotency) (*model.BrowserDeliveryGapResult, error) {
	return nil, errors.New("unexpected DeclareBrowserDeliveryGaps")
}
func (f *attemptStoreFake) SealBrowserDelivery(context.Context, *store.BrowserDeliveryFinalDeclaration, *store.CommandIdempotency) (*model.BrowserSourceStatus, error) {
	return nil, errors.New("unexpected SealBrowserDelivery")
}
func (f *attemptStoreFake) UpdateBrowserDeliverySummary(context.Context, *store.BrowserDeliverySummaryUpdate, *store.CommandIdempotency) (*model.BrowserDeliverySummaryResult, error) {
	return nil, errors.New("unexpected UpdateBrowserDeliverySummary")
}

func (f *attemptStoreFake) ListExpiredDeliveries(context.Context, int) ([]store.DeliveryExpiryDue, error) {
	return nil, nil
}
func (f *attemptStoreFake) ExpireDelivery(context.Context, *store.DeliveryExpiry) (bool, error) {
	return false, errors.New("unexpected ExpireDelivery")
}

func (f *attemptStoreFake) DeliveryBudget(context.Context, store.DeliveryBudgetAccess) (*model.DeliveryBudgetSnapshot, error) {
	return nil, errors.New("unexpected DeliveryBudget")
}
func (f *attemptStoreFake) StopDeliveryDetails(context.Context, *store.DeliveryDetailsStop, *store.CommandIdempotency) (*model.StopDeliveryDetailsResult, error) {
	return nil, errors.New("unexpected StopDeliveryDetails")
}

// chainHasCode reports whether any application error in err's chain carries
// code, so an assertion can see the cause a public concealment wraps.
func chainHasCode(err error, code string) bool {
	for err != nil {
		var failure *apperror.Error
		if !errors.As(err, &failure) {
			return false
		}
		if failure.Code() == code {
			return true
		}
		err = failure.Unwrap()
	}
	return false
}
