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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
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
	require.NoError(t, err)
	require.Equal(t, f.revision.ID, result.Attempt.AdmissionRevisionID)
	require.Equal(t, f.sitting.ClassID, result.ClassID)
	require.True(t, result.FirstAdmission)
	require.True(t, result.ConnectionOpened)
	require.Equal(t, 1, f.effects.opened)
	input := f.persistence.connect
	require.NotNil(t, input)
	require.Equal(t, model.HashToken(credential), input.ContinuityCredentialHash)
	require.NotContains(t, input.ContinuityCredentialHash, credential)
	require.NotNil(t, input.InitialConfiguration)
	require.Equal(t, f.configuration.UserSettingsRevision, input.InitialConfiguration.UserSettingsRevision)
	require.Equal(t, f.desktopBuild, input.DesktopBuild)
	require.Equal(t, int64(1), input.DesktopCompatibilityPolicyRevision)
	require.Equal(t, model.CurrentAttemptConfigurationManifestFingerprint(), input.ConfigurationManifestFingerprint)
	require.Equal(t, f.configuration.Digest, result.Configuration.Digest)
	require.Equal(t, f.configuration.Digest, result.RuntimeCapabilities.AttemptConfiguration.Digest)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"sitting", "audit", "connect", "effect.open"}, f.order)
	require.NotContains(t, strings.ToLower(fmt.Sprintf("%#v", result.Participation)), "credential")
	require.NotContains(t, fmt.Sprintf("%#v", result.Participation), credential)
	require.NotContains(t, fmt.Sprintf("%#v", result.Participation), model.HashToken(credential))
	require.Nil(t, f.audit.values["continuity_credential"])
	require.Nil(t, f.audit.values["credential_hash"])
	wantIdempotency, prepareErr := prepareConnectIdempotency(f.invocation, command)
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	require.NotContains(t, auditCapture, credential)
	require.NotContains(t, auditCapture, model.HashToken(credential))
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
	require.NoError(t, err)
	input := f.persistence.correctionAcknowledgement
	principal := f.invocation.Principal()
	require.NotNil(t, input)
	require.Equal(t, f.attemptID, input.Access.AttemptID)
	require.Equal(t, principal.UserID, input.Access.CandidateUserID)
	require.Equal(t, principal.SessionID, input.Access.SessionID)
	require.Equal(t, f.connectionID, input.Access.ConnectionID)
	require.Equal(t, principal.DesktopRegistrationID, input.Access.DesktopRegistrationID)
	require.Equal(t, principal.DPoPKeyThumbprint, input.Access.DPoPKeyThumbprint)
	require.Equal(t, model.HashToken(credential), input.Access.ContinuityCredentialHash)
	require.Equal(t, participationID, input.ParticipationID)
	require.Equal(t, int64(4), input.Generation)
	require.Equal(t, correctionRevisionID, input.CorrectionRevisionID)
	require.Equal(t, currentRevisionID, input.ExpectedCurrentRevisionID)
	require.NotNil(t, input.AuditEvent)
	require.True(t, input.AuditEvent.ID.IsZero())
	require.Equal(t, model.AuditStatusAttempt, input.AuditEvent.Status)
	require.Equal(t, correctionRevisionID, result.CorrectionRevisionID)
	require.Equal(t, currentRevisionID, result.CurrentRevisionID)
	require.Equal(t, f.at, result.AcknowledgedAt.Time)
	wantIdempotency, prepareErr := idempotency.Require(f.invocation, store.ExamAttemptCorrectionAcknowledgementOperation,
		command.IdempotencyKey, struct {
			AttemptID                 string `json:"exam_attempt_id"`
			ParticipationID           string `json:"participation_id"`
			Generation                int64  `json:"generation"`
			CorrectionRevisionID      string `json:"correction_revision_id"`
			ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
		}{f.attemptID.String(), participationID.String(), 4, correctionRevisionID.String(), currentRevisionID.String()})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
	// Contract (guardrail 8): the authorization decision and the prepared audit
	// precede the durable acknowledgement.
	require.Equal(t, []string{"correction.acknowledgement.resolve", "audit.authorization", "audit.prepare", "correction.acknowledgement.commit"}, f.order)
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	require.NotContains(t, auditCapture, credential)
	require.NotContains(t, auditCapture, model.HashToken(credential))
	require.NotContains(t, auditCapture, participationID.String())
	require.NotContains(t, auditCapture, principal.SessionID.String())
	require.NotContains(t, auditCapture, f.connectionID.String())
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
	require.NoError(t, err)
	require.Equal(t, f.at.Add(-time.Minute), result.AcknowledgedAt.Time, "result=%#v calls=%d order=%v", result, f.persistence.correctionAcknowledgementCalls, f.order)
	require.Equal(t, 1, f.persistence.correctionAcknowledgementCalls, "result=%#v calls=%d order=%v", result, f.persistence.correctionAcknowledgementCalls, f.order)
	// Contract (guardrail 8): the authorization decision and the prepared audit
	// precede the durable acknowledgement.
	require.Equal(t, []string{"correction.acknowledgement.resolve", "audit.authorization", "audit.prepare", "correction.acknowledgement.commit"}, f.order)
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
	require.NoError(t, err, "stale acknowledgement reopened capability: %#v", result)
	require.Equal(t, laterRevisionID, result.CurrentRevisionID, "stale acknowledgement reopened capability: %#v", result)
	require.Equal(t, f.at.Add(-time.Minute), result.AcknowledgedAt.Time, "stale acknowledgement reopened capability: %#v", result)
	require.Equal(t, store.CandidateBrowserAcknowledgementRequired, result.RuntimeCapabilities.Browser.State, "stale acknowledgement reopened capability: %#v", result)
	require.True(t, result.RuntimeCapabilities.WorkspaceMutationAllowed, "stale acknowledgement reopened capability: %#v", result)
	require.True(t, result.RuntimeCapabilities.SubmissionAllowed, "stale acknowledgement reopened capability: %#v", result)

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
	require.NoError(t, err)
	principal := f.invocation.Principal()
	access := f.workspace.resolvedAccess
	require.Equal(t, f.attemptID, access.AttemptID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, participationID, access.ParticipationID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, int64(4), access.Generation, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, principal.UserID, access.CandidateUserID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, principal.SessionID, access.SessionID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, f.connectionID, access.ConnectionID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, model.HashToken(credential), access.ContinuityCredentialHash, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.NotNil(t, f.workspace.mutation, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, entryID, f.workspace.mutation.EntryID, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, "src", f.workspace.mutation.DestinationPath, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, model.AttemptWorkspaceMutationCreateDirectory, f.workspace.mutation.Operation, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.True(t, f.workspace.mutation.AuditEventID.IsValid(), "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, int64(9), result.Change.Cursor, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	require.Equal(t, 1, f.effects.workspaceChanged, "access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	require.Len(t, f.audit.values, 2)
	require.Equal(t, f.attemptID.String(), f.audit.values["exam_attempt_id"])
	require.Equal(t, string(model.AttemptWorkspaceMutationCreateDirectory), f.audit.values["operation"])
	require.NotContains(t, auditCapture, credential)
	require.NotContains(t, auditCapture, model.HashToken(credential))
	require.NotContains(t, auditCapture, participationID.String())
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"workspace.resolve", "audit", "workspace.apply", "effect.workspace"}, f.order)
	wantIdempotency, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID,
		model.AttemptWorkspaceMutationCreateDirectory, struct {
			Path string `json:"path"`
		}{"src"})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.workspace.idempotency)
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
	require.NoError(t, err)
	require.NotNil(t, f.workspace.reservation, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.NotNil(t, f.workspace.ready, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.NotNil(t, f.workspace.mutation, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, f.content.attemptObjectID, f.workspace.reservation.ObjectID, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, f.content.attemptObjectID, f.workspace.ready.ObjectID, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, version, f.workspace.ready.ContentVersion, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, f.content.attemptObjectID, f.workspace.mutation.ObjectID, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, "main.go", f.workspace.mutation.DestinationPath, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.NotNil(t, result.Entry, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, version, result.Entry.ContentVersion, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	require.Equal(t, 1, f.effects.workspaceChanged, "reservation=%#v ready=%#v mutation=%#v result=%#v", f.workspace.reservation, f.workspace.ready, f.workspace.mutation, result)
	capture := fmt.Sprintf("%#v", f.audit.values)
	require.NotContains(t, capture, "main.go")
	require.NotContains(t, capture, checksum)
	require.NotContains(t, capture, credential)
	require.NotContains(t, capture, model.HashToken(credential))
	// Contract: bytes are staged only inside a reserved upload, the audit begins
	// before the durable apply (guardrail 8), and the apply commits before its
	// effect (guardrail 14).
	require.Equal(t, []string{"workspace.resolve", "workspace.reserve", "content.stage", "workspace.ready", "audit", "workspace.apply", "effect.workspace"}, f.order)
	wantIdempotency, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID,
		model.AttemptWorkspaceMutationCreateFile, struct {
			Path, MediaType, SHA256 string
			Size                    int64
		}{"main.go", "text/plain", checksum, 4})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.workspace.idempotency)
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
	_, err := f.service.ReplaceCandidateExamWorkspaceFile(context.Background(), f.invocation, command)
	require.NoError(t, err)
	want, err := prepareWorkspaceMutationIdempotency(f.invocation, "replace-key", f.attemptID,
		model.AttemptWorkspaceMutationReplaceFile, struct {
			EntryID, Path, Version, MediaType, SHA256 string
			Size                                      int64
		}{entryID.String(), "main.go", currentVersion.String(), "text/plain", checksum, 4})
	require.NoError(t, err)
	require.Equal(t, want, f.workspace.idempotency)
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
	require.NoError(t, err, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.Len(t, f.workspace.reclaimable, 1, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.Equal(t, 0, f.effects.workspaceChanged, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.Equal(t, f.workspace.proposedEntryID, f.workspace.mutation.EntryID, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.NotEqual(t, entryID, f.workspace.mutation.EntryID, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.Equal(t, f.workspace.proposedVersion, f.workspace.ready.ContentVersion, "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
	require.Equal(t, version, resultVersion(f.workspace.mutationResult), "error=%v reclaimable=%v effects=%#v", err, f.workspace.reclaimable, f.effects)
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
	require.NoError(t, err, "result=%#v mutation=%#v error", result, f.workspace.mutation)
	require.NotNil(t, result.Entry, "result=%#v mutation=%#v error", result, f.workspace.mutation)
	require.Equal(t, retainedID, result.Entry.EntryID, "result=%#v mutation=%#v error", result, f.workspace.mutation)
	require.Equal(t, f.workspace.proposedEntryID, f.workspace.mutation.EntryID, "result=%#v mutation=%#v error", result, f.workspace.mutation)
	require.NotEqual(t, retainedID, f.workspace.mutation.EntryID, "result=%#v mutation=%#v error", result, f.workspace.mutation)
	require.Equal(t, 0, f.effects.workspaceChanged, "result=%#v mutation=%#v error", result, f.workspace.mutation)
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
			require.ErrorAs(t, err, &fault)
			require.Equal(t, test.wantCode, fault.Code())
			require.Len(t, f.workspace.reclaimable, test.wantReclaim)
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
			require.Equal(t, "old", mutation.ExpectedPath)
			require.Equal(t, "new", mutation.DestinationPath)
		}},
		{name: "delete_file", operation: model.AttemptWorkspaceMutationDeleteEntry, invoke: func(f *fixture, id model.AttemptWorkspaceEntryID, version model.WorkspaceContentVersion) error {
			_, err := f.service.DeleteCandidateExamWorkspaceEntry(context.Background(), f.invocation, DeleteWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: id, ExpectedPath: "old", ExpectedContentVersion: version, IdempotencyKey: "test-key"})
			return err
		}, check: func(t *testing.T, mutation *store.ExamAttemptWorkspaceMutation) {
			require.Equal(t, "old", mutation.ExpectedPath)
			require.False(t, mutation.ExpectedContentVersion.IsZero())
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
			require.NoError(t, test.invoke(f, entryID, version))
			test.check(t, f.workspace.mutation)
			var semantic any
			switch test.operation {
			case model.AttemptWorkspaceMutationMoveEntry:
				semantic = struct{ EntryID, ExpectedPath, DestinationPath string }{entryID.String(), "old", "new"}
			case model.AttemptWorkspaceMutationDeleteEntry:
				semantic = struct{ EntryID, ExpectedPath, ExpectedContentVersion string }{entryID.String(), "old", version.String()}
			}
			want, prepareErr := prepareWorkspaceMutationIdempotency(f.invocation, "test-key", f.attemptID, test.operation, semantic)
			require.NoError(t, prepareErr)
			require.Equal(t, want, f.workspace.idempotency)
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
		require.Error(t, err)
		require.Empty(t, f.order)
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
	require.NoError(t, err)
	input := f.persistence.renew
	principal := f.invocation.Principal()
	require.NotNil(t, input, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, f.attemptID, input.AttemptID, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, participationID, input.ParticipationID, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, f.connectionID, input.ConnectionID, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, principal.UserID, input.CandidateUserID, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, principal.SessionID, input.SessionID, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, int64(3), input.Generation, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, int64(7), input.Sequence, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, model.HashToken(credential), input.ContinuityCredentialHash, "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, int64(7), result.AcceptedSequence, "renewal input/result = %#v / %#v", input, result)
	require.True(t, result.DatabaseTime.Equal(databaseNow), "renewal input/result = %#v / %#v", input, result)
	require.Equal(t, 1, f.effects.renewed, "renewal input/result = %#v / %#v", input, result)
	require.NotContains(t, fmt.Sprintf("%#v", result), credential, "renewal input/result = %#v / %#v", input, result)
	require.NotContains(t, fmt.Sprintf("%#v", result), model.HashToken(credential), "renewal input/result = %#v / %#v", input, result)
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
	require.NoError(t, err)
	require.Equal(t, 0, f.effects.renewed)
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
		_, err := f.service.RenewExamAttemptParticipation(context.Background(), f.invocation, command)
		require.Error(t, err, "RenewParticipation(%#v) error=%v input", command, err)
		require.Nil(t, f.persistence.renew, "RenewParticipation(%#v) error=%v input", command, err)
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
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.attempt.unavailable", fault.Code())
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
			require.True(t, chainHasCode(err, test.code), "error = %v, want %s", err, test.code)
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
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v order=%v", err, f.effects, f.order)
	require.Equal(t, "exam.attempt.connection_lost", fault.Code(), "error=%v effects=%#v order=%v", err, f.effects, f.order)
	require.Equal(t, 1, f.effects.expired, "error=%v effects=%#v order=%v", err, f.effects, f.order)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"renew", "expiry.resolve", "system.audit", "expiry.commit", "effect.expire"}, f.order)
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
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v order=%v", err, f.effects, f.order)
	require.Equal(t, "exam.attempt.connection_lost", fault.Code(), "error=%v effects=%#v order=%v", err, f.effects, f.order)
	require.Equal(t, 0, f.effects.expired, "error=%v effects=%#v order=%v", err, f.effects, f.order)
	// An already ended Participation is neither audited nor expired again.
	require.Nil(t, f.systemAudit.values, "an ended Participation was audited")
	require.Nil(t, f.persistence.expireInput, "an ended Participation was expired again")
}

func TestScanExpiredParticipationsCommitsAuditedAggregateBeforeSafeEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{due}
	f.persistence.expireResult = expiryResultFixture(t, f, due, false)
	result, err := f.service.ScanExpiredParticipations(context.Background(), 200)
	require.NoError(t, err)
	input := f.persistence.expireInput
	require.Equal(t, 1, result.Due, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, 1, result.Completed, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, 0, result.Replayed, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.NotNil(t, input, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, due.AttemptID, input.AttemptID, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, due.ParticipationID, input.ParticipationID, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, due.Generation, input.Generation, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.True(t, input.EvidenceID.IsValid(), "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.True(t, input.FlagID.IsValid(), "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.True(t, input.SuspensionID.IsValid(), "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.True(t, input.AuditEventID.IsValid(), "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.GreaterOrEqual(t, input.AuditAt, int64(1), "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	require.Equal(t, 1, f.effects.expired, "scan=%#v input=%#v order=%v effects=%#v", result, input, f.order, f.effects)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"expiry.list", "system.audit", "expiry.commit", "effect.expire"}, f.order)
	require.Nil(t, f.systemAudit.values["continuity_credential"])
	require.Nil(t, f.systemAudit.values["credential_hash"])
	require.Nil(t, f.systemAudit.values["private_reason"])
	for _, forbidden := range []string{"continuity_credential", "credential_hash", "private_reason"} {
		require.NotContains(t, strings.ToLower(fmt.Sprintf("%#v", f.systemAudit.values)), forbidden, "expiry audit capture contains %q: %#v", forbidden, f.systemAudit.values)
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
	require.NoError(t, err, "scan=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 1, result.Completed, "scan=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 1, f.effects.expired, "scan=%#v error=%v effects=%#v", result, err, f.effects)
}

func TestScanExpiredParticipationsSuppressesReplayEffectsAndRejectsUnboundedLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	due := expiryDueFixture(f)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{due}
	f.persistence.expireResult = expiryResultFixture(t, f, due, true)
	result, err := f.service.ScanExpiredParticipations(context.Background(), 1)
	require.NoError(t, err, "scan=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 1, result.Replayed, "scan=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 0, f.effects.expired, "scan=%#v error=%v effects=%#v", result, err, f.effects)
	for _, limit := range []int{0, 201} {
		other := newFixture(t)
		_, err = other.service.ScanExpiredParticipations(context.Background(), limit)
		require.Error(t, err, "limit=%d error=%v order", limit, err)
		require.Empty(t, other.order, "limit=%d error=%v order", limit, err)
	}
}

func TestScanExpiredParticipationsFailsAuditAndPublishesNothingOnCommitFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.expiryDue = []store.ExamAttemptParticipationExpiryDue{expiryDueFixture(f)}
	f.persistence.expireErr = store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	_, err := f.service.ScanExpiredParticipations(context.Background(), 10)
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	require.Equal(t, "exam.attempt.connection_closed", fault.Code(), "error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	require.Equal(t, fault.Code(), f.systemAudit.failedCode, "error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	require.Equal(t, 0, f.effects.expired, "error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	// Contract (guardrail 8): the audit begins before the durable write and
	// records the failed commit; no effect follows.
	require.Equal(t, []string{"expiry.list", "system.audit", "expiry.commit", "system.audit.fail"}, f.order)
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
	require.NoError(t, err)
	input := f.persistence.reallow
	require.NotNil(t, input, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, suspensionID, input.SuspensionID, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, int64(2), input.ExpectedAttemptRevision, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, reason, input.PrivateReason, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, managerID, input.ActorUserID, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.False(t, input.ManagerOverride, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, model.ExamAttemptReady, result.Attempt.State, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, model.AttemptSuspensionClosed, result.Suspension.State, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	require.Equal(t, 1, f.effects.reallowed, "result=%#v input=%#v effects=%#v order=%v", result, input, f.effects, f.order)
	// Contract: the Manager authorization precedes the audit, the audit begins
	// before the durable reallow (guardrail 8), and the reallow commits before
	// its effect (guardrail 14).
	require.Equal(t, []string{"manager.manage", "sitting", "audit", "manager.get", "reallow", "effect.reallow"}, f.order)
	require.Nil(t, f.audit.values["private_reason"], "private reason escaped persistence input: audit=%#v result=%#v", f.audit.values, result)
	require.NotContains(t, fmt.Sprintf("%#v", result), reason, "private reason escaped persistence input: audit=%#v result=%#v", f.audit.values, result)
	for _, capture := range []any{f.audit.values, result} {
		require.NotContains(t, fmt.Sprintf("%#v", capture), reason, "private reason escaped persistence input: %#v", capture)
	}
	wantIdempotency, prepareErr := prepareReallowIdempotency(f.invocation, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: reason, IdempotencyKey: "test-key",
	})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
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
	require.NoError(t, err, "Reallow() error")
	require.Equal(t, model.AttemptSuspensionCandidateReasonFocusLossPolicy, result.Suspension.CandidateReason)
	require.Equal(t, 1, f.effects.reallowed, "Reallow() effects")
}

func TestManagerAttemptOperationsAuthorizeBeforeReadingAttempts(t *testing.T) {
	t.Parallel()
	denied := apperror.New("authorization.denied")
	// Each operation records only its authorization decision before refusing.
	operations := map[string]struct {
		authorization string
		run           func(*fixture, model.ExamAttemptID) error
	}{
		"get": {authorization: "manager.authorize", run: func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.GetExamAttempt(context.Background(), f.invocation, GetManagedAttemptQuery{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID})
			return err
		}},
		"reallow": {authorization: "manager.manage", run: func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID, SuspensionID: model.NewAttemptSuspensionID(),
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		}},
		"end": {authorization: "manager.manage", run: func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.EndExamAttemptByManager(context.Background(), f.invocation, ManagerEndCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID,
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		}},
		"suspend": {authorization: "manager.manage", run: func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.SuspendExamAttempt(context.Background(), f.invocation, ManualSuspensionCommand{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID,
				ExpectedAttemptRevision: 2, PrivateReason: "probe", IdempotencyKey: "probe-key"})
			return err
		}},
		"browser_activity": {authorization: "browser.authorize", run: func(f *fixture, attemptID model.ExamAttemptID) error {
			_, err := f.service.ListExamAttemptBrowserActivity(context.Background(), f.invocation, BrowserActivityPageQuery{
				ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: attemptID, Limit: 10})
			return err
		}},
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
				err := operation.run(f, attemptID)
				var fault *apperror.Error
				require.ErrorAs(t, err, &fault, "existing=%t error", existing)
				codes = append(codes, fault.Code())
				require.Equal(t, []string{operation.authorization}, f.order, "existing=%t denied caller reached Attempt state", existing)
				require.Nil(t, f.audit.values, "existing=%t denied caller reached Attempt state: order=%v audit", existing, f.order)
			}
			require.Equal(t, codes[1], codes[0])
			require.Equal(t, "resource.not_found", codes[0])
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
	require.True(t, apperror.Is(err, "resource.not_found"), "error=%v audit=%#v order=%v", err, f.audit, f.order)
	require.True(t, chainHasCode(err, "exam.attempt.not_found"), "error=%v audit=%#v order=%v", err, f.audit, f.order)
	require.Equal(t, "exam.attempt.not_found", f.audit.failedCode, "error=%v audit=%#v order=%v", err, f.audit, f.order)
	require.Nil(t, f.persistence.reallow, "error=%v audit=%#v order=%v", err, f.audit, f.order)
}

func TestReallowValidatesReasonBeforeAuthorization(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"", " padded ", "\xff", strings.Repeat("é", model.AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		f := newFixture(t)
		_, err := f.service.ReallowExamAttempt(context.Background(), f.invocation, ReallowCommand{
			ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: model.NewAttemptSuspensionID(),
			ExpectedAttemptRevision: 1, PrivateReason: reason, IdempotencyKey: "test-key",
		})
		require.Error(t, err, "reason=%q error=%v order", reason, err)
		require.Empty(t, f.order, "reason=%q error=%v order", reason, err)
		require.Nil(t, f.persistence.reallow, "reason=%q error=%v order", reason, err)
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
	require.NoError(t, err, "result=%#v input=%#v error=%v effects=%#v", result, f.persistence.reallow, err, f.effects)
	require.True(t, result.Replayed, "result=%#v input=%#v error=%v effects=%#v", result, f.persistence.reallow, err, f.effects)
	require.True(t, f.persistence.reallow.ManagerOverride, "result=%#v input=%#v error=%v effects=%#v", result, f.persistence.reallow, err, f.effects)
	require.Equal(t, 0, f.effects.reallowed, "result=%#v input=%#v error=%v effects=%#v", result, f.persistence.reallow, err, f.effects)
}

func expiryDueFixture(f *fixture) store.ExamAttemptParticipationExpiryDue {
	return store.ExamAttemptParticipationExpiryDue{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		CandidateUserID: f.userID, AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, LeaseExpiresAt: f.at.Add(-time.Second)}
}

func expiryResultFixture(t *testing.T, f *fixture, due store.ExamAttemptParticipationExpiryDue, replayed bool) *store.ExamAttemptParticipationExpiryResult {
	t.Helper()
	attempt, err := model.NewExamAttempt(due.AttemptID, due.ExamID, due.SittingID, due.CandidateUserID, f.revision.ID, f.at.Add(-time.Hour))
	require.NoError(t, err)
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, f.at, 2
	participation := &store.ExamAttemptParticipationView{ID: due.ParticipationID, AttemptID: due.AttemptID,
		State: model.AttemptParticipationEnded, Generation: due.Generation, RenewalSequence: 4,
		StartedAt: f.at.Add(-time.Minute), UpdatedAt: f.at, LeaseExpiresAt: due.LeaseExpiresAt,
		EndedAt: model.OptionalTimeFrom(f.at), EndReason: model.AttemptParticipationEndLeaseExpired}
	connection := &store.ExamAttemptManagerConnection{ID: model.NewAttemptConnectionID(), State: model.AttemptConnectionClosed,
		OpenedAt: f.at.Add(-time.Minute), ClosedAt: model.OptionalTimeFrom(f.at), CloseReason: model.AttemptConnectionCloseLeaseExpired}
	flag, err := model.NewIntegrityFlag(model.NewIntegrityFlagID(), due.AttemptID, due.Generation, model.IntegrityPolicyConnectionLoss, f.at)
	require.NoError(t, err)
	evidence, err := model.NewConnectionLossEvidence(model.NewIntegrityEvidenceID(), due.AttemptID, due.ParticipationID,
		flag.ID, due.Generation, due.LeaseExpiresAt, f.at)
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	require.NoError(t, err, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.True(t, result.Replayed, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 0, f.effects.opened, "result=%#v error=%v effects=%#v", result, err, f.effects)
	// Contract (guardrail 8): the audit begins before the durable write.
	require.Equal(t, []string{"sitting", "audit", "connect"}, f.order)
}

func TestReconnectThatOpensANewConnectionPublishesOneOpenEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	require.NoError(t, err, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.False(t, result.FirstAdmission, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.True(t, result.ConnectionOpened, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.False(t, result.Replayed, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 1, f.effects.opened, "result=%#v error=%v effects=%#v", result, err, f.effects)
}

func TestDifferentKeyConvergenceOnExistingOpenConnectionPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	f.persistence.connectionOpened = false
	result, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	require.NoError(t, err, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.False(t, result.FirstAdmission, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.False(t, result.ConnectionOpened, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.False(t, result.Replayed, "result=%#v error=%v effects=%#v", result, err, f.effects)
	require.Equal(t, 0, f.effects.opened, "result=%#v error=%v effects=%#v", result, err, f.effects)
}

func TestConnectReplayOfClosedConnectionRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectClosed = true
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v", err, f.effects)
	require.Equal(t, "exam.attempt.connection_closed", fault.Code(), "error=%v effects=%#v", err, f.effects)
	require.Equal(t, 0, f.effects.opened, "error=%v effects=%#v", err, f.effects)
}

func TestConnectReplayOfEndedParticipationRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.participationEnded = true
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v", err, f.effects)
	require.Equal(t, "exam.attempt.connection_closed", fault.Code(), "error=%v effects=%#v", err, f.effects)
	require.Equal(t, 0, f.effects.opened, "error=%v effects=%#v", err, f.effects)
}

func TestConnectRejectsAConnectionOutcomeBoundToAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectionSession = model.NewSessionID()
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v", err, f.effects)
	require.Equal(t, "exam.attempt.unavailable", fault.Code(), "error=%v effects=%#v", err, f.effects)
	require.Equal(t, 0, f.effects.opened, "error=%v effects=%#v", err, f.effects)
}

func TestConnectFailureCompletesAuditAndPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	require.Equal(t, "exam.attempt.sitting_not_open", fault.Code(), "error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	require.Equal(t, fault.Code(), f.audit.failedCode, "error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	require.Equal(t, 0, f.effects.opened, "error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	// Contract (guardrail 8): the audit begins before the durable write and
	// records the rejected commit.
	require.Equal(t, []string{"sitting", "audit", "connect", "audit.fail"}, f.order)
}

func TestExpiredReplayReturnsCandidateSafeConnectionLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	_, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "error=%v effects=%#v", err, f.effects)
	require.Equal(t, "exam.attempt.connection_lost", fault.Code(), "error=%v effects=%#v", err, f.effects)
	require.Equal(t, 0, f.effects.opened, "error=%v effects=%#v", err, f.effects)
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
	require.NoError(t, err)
	require.Equal(t, f.persistence.presentation.Capacity, result.Capacity)
	require.Equal(t, f.revision.ID, result.RuntimeCapabilities.ExamRevision.AdmissionRevisionID)
	require.Equal(t, currentID, result.RuntimeCapabilities.ExamRevision.CurrentRevisionID)
	require.True(t, result.RuntimeCapabilities.FocusLossCollectionEnabled)
	require.Contains(t, result.InstructionsMarkdown, "# Rules")
	require.Contains(t, result.InstructionsMarkdown, "**Go**")
	require.Contains(t, result.InstructionsMarkdown, "[handbook](https://example.test/handbook)")
	require.Contains(t, result.Resources[0].DescriptionMarkdown, "_carefully_")
	for _, forbidden := range []string{"<script", "alert('x')", "javascript:", "![tracker]", "pixel.png", "<iframe", "evil.test", "data:text/html"} {
		require.NotContains(t, result.InstructionsMarkdown+result.Resources[0].DescriptionMarkdown, forbidden, "unsafe Markdown survived %q: %#v", forbidden, result)
	}
	require.Equal(t, model.HashToken(credential), f.persistence.candidateAccess.ContinuityCredentialHash)
	require.Equal(t, f.userID, f.persistence.candidateAccess.CandidateUserID)
	require.Equal(t, f.invocation.Principal().SessionID, f.persistence.candidateAccess.SessionID)
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
			require.ErrorAs(t, err, &fault)
			require.Equal(t, "exam.attempt.unavailable", fault.Code())
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
	require.NoError(t, err)
	defer opened.Body.Close()
	require.Equal(t, starterID, f.content.starterID, "opened=%#v starter", opened)
	require.Equal(t, version, opened.ContentVersion, "opened=%#v starter", opened)
	require.Equal(t, "text/x-go", opened.MediaType, "opened=%#v starter", opened)
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
	require.True(t, apperror.Is(err, "resource.not_found"), "error=%v access=%#v", err, f.persistence.candidateAccess)
	require.True(t, chainHasCode(err, "exam.attempt.not_found"), "error=%v access=%#v", err, f.persistence.candidateAccess)
	require.Equal(t, principal.SessionID, f.persistence.candidateAccess.SessionID, "error=%v access=%#v", err, f.persistence.candidateAccess)
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
		require.ErrorAs(t, open(), &fault)
		require.Equal(t, "authentication.token_invalid", fault.Code())
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
	require.NoError(t, err)
	require.Equal(t, workspaceID, page.WorkspaceID, "page=%#v options=%#v", page, f.workspace.options)
	require.Equal(t, int64(7), page.Cursor, "page=%#v options=%#v", page, f.workspace.options)
	require.Len(t, page.Items, 1, "page=%#v options=%#v", page, f.workspace.options)
	require.True(t, page.HasMore, "page=%#v options=%#v", page, f.workspace.options)
	require.False(t, page.RefreshRequired, "page=%#v options=%#v", page, f.workspace.options)
	require.Equal(t, int64(-1), f.workspace.options.ExpectedCursor, "page=%#v options=%#v", page, f.workspace.options)
	require.True(t, f.workspace.options.AfterEntryID.IsZero(), "page=%#v options=%#v", page, f.workspace.options)
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
		require.ErrorAs(t, err, &fault, "query=%#v error=%v calls", query, err)
		require.Equal(t, "exam.attempt.invalid", fault.Code(), "query=%#v error=%v calls", query, err)
		require.Equal(t, 0, f.workspace.lists, "query=%#v error=%v calls", query, err)
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
	require.NoError(t, err, "page=%#v options=%#v error", page, f.workspace.journalOptions)
	require.Equal(t, int64(5), page.CurrentCursor, "page=%#v options=%#v error", page, f.workspace.journalOptions)
	require.Len(t, page.Entries, 1, "page=%#v options=%#v error", page, f.workspace.journalOptions)
	require.Equal(t, int64(4), f.workspace.journalOptions.AfterCursor, "page=%#v options=%#v error", page, f.workspace.journalOptions)
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
	require.NoError(t, err)
	defer opened.Body.Close()
	require.Equal(t, objectID, f.content.openAttemptID, "opened=%#v object", opened)
	require.Equal(t, version, opened.ContentVersion, "opened=%#v object", opened)
}

func TestManagerListAuthorizesBeforeBoundedStoreRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rows := make([]store.ExamAttemptManagerSnapshot, 3)
	for index := range rows {
		attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), f.sitting.ExamID, f.sitting.ID, model.NewUserID(), f.revision.ID, f.at.Add(time.Duration(index)*time.Second))
		require.NoError(t, err)
		rows[index].Attempt = attempt
	}
	f.persistence.managerRows = rows
	page, err := f.service.ListExamAttempts(context.Background(), f.invocation, ListManagedAttemptsQuery{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, Limit: 2,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "page=%#v options=%#v order=%v", page, f.persistence.managerOptions, f.order)
	require.True(t, page.HasMore, "page=%#v options=%#v order=%v", page, f.persistence.managerOptions, f.order)
	require.Equal(t, 3, f.persistence.managerOptions.Limit, "page=%#v options=%#v order=%v", page, f.persistence.managerOptions, f.order)
	// Contract: authorization precedes reading the managed Attempts.
	require.Equal(t, []string{"manager.authorize", "manager.list"}, f.order)
}

func TestManagerListValidatesAndDeepClonesActiveSuspension(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	require.NoError(t, err)
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, f.at, 2
	suspension := &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: attempt.ID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 2,
		State: model.AttemptSuspensionActive, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: f.at}
	f.persistence.managerRows = []store.ExamAttemptManagerSnapshot{{Attempt: attempt, ActiveSuspension: suspension}}
	page, err := f.service.ListExamAttempts(context.Background(), f.invocation, ListManagedAttemptsQuery{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, Limit: 1,
	})
	require.NoError(t, err, "page=%#v error", page)
	require.Len(t, page.Items, 1, "page=%#v error", page)
	require.NotNil(t, page.Items[0].ActiveSuspension, "page=%#v error", page)
	require.Equal(t, suspension.ID, page.Items[0].ActiveSuspension.ID, "page=%#v error", page)
	suspension.ID = model.NewAttemptSuspensionID()
	require.NotEqual(t, suspension.ID, page.Items[0].ActiveSuspension.ID, "manager page retained Store-owned Suspension pointer")
}

func TestTrustedCloseCommitsBeforeManagerEffectAndSuppressesNoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	connected, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, credential, "test-key"))
	require.NoError(t, err)
	f.order = nil
	f.persistence.closeChanged = true
	closed, err := f.service.CloseExamAttemptConnection(context.Background(), f.invocation, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	})
	require.NoError(t, err, "closed=%#v error=%v order=%v", closed, err, f.order)
	require.True(t, closed.Changed, "closed=%#v error=%v order=%v", closed, err, f.order)
	// Contract (guardrails 8 and 14): the audit begins before the durable write,
	// and the write commits before its effect.
	require.Equal(t, []string{"audit", "close", "effect.close"}, f.order)
	f.order = nil
	f.persistence.closeChanged = false
	_, err = f.service.CloseExamAttemptConnection(context.Background(), f.invocation, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	})
	require.NoError(t, err)
	require.Equal(t, 1, f.effects.closed, "close effects")
}

func TestTrustedCloseStillFencesCandidateAndSessionOwnership(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*model.Principal){
		func(principal *model.Principal) { principal.UserID = model.NewUserID() },
		func(principal *model.Principal) { principal.SessionID = model.NewSessionID() },
	} {
		f := newFixture(t)
		connected, err := f.service.ConnectExamAttempt(context.Background(), f.invocation, validConnectCommand(f, model.NewCredentialToken(), "test-key"))
		require.NoError(t, err)
		principal := f.invocation.Principal()
		mutate(&principal)
		_, err = f.service.CloseExamAttemptConnection(context.Background(), invocation.New(principal, f.invocation.RequestMetadata()), CloseConnectionCommand{
			AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
			ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
		})
		require.True(t, apperror.Is(err, "resource.not_found"), "error=%v effects=%#v", err, f.effects)
		require.True(t, chainHasCode(err, "exam.attempt.not_found"), "error=%v effects=%#v", err, f.effects)
		require.Equal(t, 0, f.effects.closed, "error=%v effects=%#v", err, f.effects)
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
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.attempt.invalid", fault.Code())
	require.Empty(t, f.order)
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
	f.configuration, configurationErr = (model.AttemptConfigurationCandidate{ManifestFingerprint: f.desktopBuild.AttemptConfigurationManifestFingerprint, RegistryFingerprint: f.desktopBuild.DesktopSettingsRegistryFingerprint, UserSettingsRevision: model.NewUserSettingsRevision(), DesktopBuild: f.desktopBuild.DesktopBuildID, DesktopTarget: f.desktopBuild.TargetTuple(), Presentation: model.AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}).Freeze(model.NewID())
	require.NoError(t, configurationErr)
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
	require.NoError(t, err)
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
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
	require.NoError(t, err)
	attempt.State, attempt.UpdatedAt, attempt.Revision = state, f.at, revision
	err = attempt.Validate()
	require.NoError(t, err)
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
	latestParticipation             *store.ExamAttemptParticipationView
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
	return &store.ExamAttemptManagerSnapshot{Attempt: f.attempt, ActiveSuspension: f.managerSuspension,
		LatestParticipation: f.latestParticipation}, nil
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
	require.NoError(t, err)
	return agreement
}

func (f *attemptStoreFake) UpdateSecurityCoverage(context.Context, *store.ExamAttemptSecurityCoverageUpdate) (model.SecurityCoverageResult, error) {
	return model.SecurityCoverageResult{}, errors.New("unexpected security update")
}

func renewalCoverageFixture() model.SecurityCoverageRenewal {
	return model.SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: model.SHA256Fingerprint([]byte("renewal-policy")), SecuritySessionID: model.NewID(), StreamID: model.NewID(), Posture: "compliant", Sources: []model.NativeSourceCoverage{}, Coverage: []model.NativeCoverageClaim{}, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
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
