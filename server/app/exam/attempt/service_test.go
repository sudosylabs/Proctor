// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestConnectDelegatesAtomicAdmissionAndPassesOnlyCredentialHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: credential, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.AdmissionRevisionID != f.revision.ID || result.ClassID != f.sitting.ClassID ||
		!result.FirstAdmission || !result.ConnectionOpened || f.effects.opened != 1 {
		t.Fatalf("connection result = %#v", result)
	}
	input := f.persistence.connect
	if input == nil || input.ContinuityCredentialHash != model.HashToken(credential) ||
		strings.Contains(input.ContinuityCredentialHash, credential) {
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
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	if strings.Contains(auditCapture, credential) || strings.Contains(auditCapture, model.HashToken(credential)) {
		t.Fatalf("credential material entered audit = %s", auditCapture)
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

	result, err := f.service.CreateWorkspaceDirectory(context.Background(), f.call, CreateWorkspaceDirectoryCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID,
			ContinuityCredential: credential}, ParticipationID: participationID, Generation: 4},
		Path: "src", Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := f.call.Principal()
	access := f.workspace.resolvedAccess
	if access.AttemptID != f.attemptID || access.ParticipationID != participationID || access.Generation != 4 ||
		access.CandidateUserID != principal.UserID || access.SessionID != principal.SessionID || access.ConnectionID != f.connectionID ||
		access.ContinuityCredentialHash != model.HashToken(credential) || f.workspace.mutation == nil ||
		f.workspace.mutation.EntryID != entryID || f.workspace.mutation.DestinationPath != "src" ||
		f.workspace.mutation.Operation != model.AttemptWorkspaceMutationCreateDirectory || !model.IsValidId(f.workspace.mutation.AuditEventID) ||
		result.Change.Cursor != 9 || f.effects.workspaceChanged != 1 {
		t.Fatalf("access=%#v mutation=%#v result=%#v effects=%#v", access, f.workspace.mutation, result, f.effects)
	}
	auditCapture := fmt.Sprintf("%#v", f.audit.values)
	if strings.Contains(auditCapture, "src") || strings.Contains(auditCapture, credential) ||
		strings.Contains(auditCapture, model.HashToken(credential)) || strings.Contains(auditCapture, participationID.String()) {
		t.Fatalf("private mutation material entered audit: %s", auditCapture)
	}
	if got := strings.Join(f.order, ","); got != "workspace.resolve,audit,workspace.apply,effect.workspace" {
		t.Fatalf("order = %s", got)
	}
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

	result, err := f.service.CreateWorkspaceFile(context.Background(), f.call, CreateWorkspaceFileCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID,
			ContinuityCredential: credential}, ParticipationID: participationID, Generation: 2},
		Path: "main.go", MediaType: "text/plain", ExpectedSHA256: checksum, Body: strings.NewReader("main"), Size: 4,
		Idempotency: &store.CommandIdempotency{},
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
	_, err := f.service.CreateWorkspaceFile(context.Background(), f.call, CreateWorkspaceFileCommand{
		Access: validWorkspaceMutationAccess(f), Path: "retry.txt", MediaType: "text/plain", ExpectedSHA256: checksum,
		Body: strings.NewReader("x"), Size: 1, Idempotency: &store.CommandIdempotency{},
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
	result, err := f.service.CreateWorkspaceDirectory(context.Background(), f.call, CreateWorkspaceDirectoryCommand{
		Access: validWorkspaceMutationAccess(f), Path: "src", Idempotency: &store.CommandIdempotency{},
	})
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
			_, err := f.service.CreateWorkspaceFile(context.Background(), f.call, CreateWorkspaceFileCommand{Access: validWorkspaceMutationAccess(f),
				Path: "work.txt", MediaType: "text/plain", ExpectedSHA256: checksum, Body: strings.NewReader("x"), Size: 1,
				Idempotency: &store.CommandIdempotency{}})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.wantCode || len(f.workspace.reclaimable) != test.wantReclaim {
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
			_, err := f.service.MoveWorkspaceEntry(context.Background(), f.call, MoveWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: id, ExpectedPath: "old", DestinationPath: "new", Idempotency: &store.CommandIdempotency{}})
			return err
		}, check: func(t *testing.T, mutation *store.ExamAttemptWorkspaceMutation) {
			if mutation.ExpectedPath != "old" || mutation.DestinationPath != "new" {
				t.Fatalf("mutation=%#v", mutation)
			}
		}},
		{name: "delete_file", operation: model.AttemptWorkspaceMutationDeleteEntry, invoke: func(f *fixture, id model.AttemptWorkspaceEntryID, version model.WorkspaceContentVersion) error {
			_, err := f.service.DeleteWorkspaceEntry(context.Background(), f.call, DeleteWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: id, ExpectedPath: "old", ExpectedContentVersion: version, Idempotency: &store.CommandIdempotency{}})
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
		})
	}
}

func TestConnectRejectsMalformedCredentialAndPATBeforeReads(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*fixture){
		func(f *fixture) { f.call = NewCall(model.Principal{UserID: f.userID}, model.RequestMetadata{}) },
		func(f *fixture) {
			principal := f.call.Principal()
			principal.CredentialType = model.CredentialPersonalAccessToken
			principal.SessionID = ""
			principal.AuthenticationStrength = ""
			principal.AuthenticatedAt = time.Time{}
			principal.ClientType = model.SessionClientCLI
			principal.CredentialScopes = []string{string(model.ActionExamSittingView)}
			f.call = NewCall(principal, model.RequestMetadata{})
		},
	} {
		f := newFixture(t)
		mutate(f)
		_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
			SittingID: f.sitting.ID, ContinuityCredential: "not-canonical", Idempotency: &store.CommandIdempotency{},
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
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{
		AttemptID: f.attemptID, ParticipationID: participationID, Generation: 3, AcceptedSequence: 7,
		DatabaseTime: databaseNow, LeaseExpiresAt: databaseNow.Add(model.AttemptParticipationInitialLease),
	}
	result, err := f.service.RenewParticipation(context.Background(), f.call, RenewParticipationCommand{
		AttemptID: f.attemptID, ParticipationID: participationID, ConnectionID: f.connectionID,
		Generation: 3, Sequence: 7, ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.renew
	principal := f.call.Principal()
	if input == nil || input.AttemptID != f.attemptID || input.ParticipationID != participationID ||
		input.ConnectionID != f.connectionID || input.CandidateUserID != principal.UserID || input.SessionID != principal.SessionID ||
		input.Generation != 3 || input.Sequence != 7 || input.ContinuityCredentialHash != model.HashToken(credential) ||
		result.AcceptedSequence != 7 || !result.DatabaseTime.Equal(databaseNow) ||
		strings.Contains(fmt.Sprintf("%#v", result), credential) || strings.Contains(fmt.Sprintf("%#v", result), model.HashToken(credential)) {
		t.Fatalf("renewal input/result = %#v / %#v", input, result)
	}
}

func TestRenewParticipationRejectsInvalidPrincipalAndFieldsBeforeStore(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*fixture, *RenewParticipationCommand){
		func(f *fixture, command *RenewParticipationCommand) {
			f.call = NewCall(model.Principal{}, model.RequestMetadata{})
		},
		func(_ *fixture, command *RenewParticipationCommand) { command.AttemptID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.ParticipationID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.ConnectionID = "" },
		func(_ *fixture, command *RenewParticipationCommand) { command.Generation = 0 },
		func(_ *fixture, command *RenewParticipationCommand) { command.Sequence = 0 },
		func(_ *fixture, command *RenewParticipationCommand) { command.ContinuityCredential = "not-canonical" },
	} {
		f := newFixture(t)
		command := RenewParticipationCommand{AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(),
			ConnectionID: f.connectionID, Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken()}
		mutate(f, &command)
		if _, err := f.service.RenewParticipation(context.Background(), f.call, command); err == nil || f.persistence.renew != nil {
			t.Fatalf("RenewParticipation(%#v) error=%v input=%#v", command, err, f.persistence.renew)
		}
	}
}

func TestRenewParticipationRejectsInconsistentStoreProjection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.renewResult = &store.ExamAttemptParticipationRenewalResult{
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), Generation: 1,
		AcceptedSequence: 1, DatabaseTime: f.at, LeaseExpiresAt: f.at.Add(model.AttemptParticipationInitialLease),
	}
	_, err := f.service.RenewParticipation(context.Background(), f.call, RenewParticipationCommand{
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
		Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.unavailable" {
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
			_, err := f.service.RenewParticipation(context.Background(), f.call, RenewParticipationCommand{
				AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
				Generation: 1, Sequence: 1, ContinuityCredential: model.NewCredentialToken(),
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.code {
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
	_, err := f.service.RenewParticipation(context.Background(), f.call, RenewParticipationCommand{
		AttemptID: due.AttemptID, ParticipationID: due.ParticipationID, ConnectionID: f.connectionID,
		Generation: due.Generation, Sequence: 5, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_lost" || f.effects.expired != 1 ||
		strings.Join(f.order, ",") != "renew,expiry.resolve,system.audit,expiry.commit,effect.expire" {
		t.Fatalf("error=%v effects=%#v order=%v", err, f.effects, f.order)
	}
}

func TestLateRenewalConcealsScannerWinningBeforeTargetResolution(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.renewErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	f.persistence.resolveExpiryErr = store.NewErrNotFound("attempt_participation", "ended")
	_, err := f.service.RenewParticipation(context.Background(), f.call, RenewParticipationCommand{
		AttemptID: f.attemptID, ParticipationID: model.NewAttemptParticipationID(), ConnectionID: f.connectionID,
		Generation: 2, Sequence: 5, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_lost" || f.effects.expired != 0 ||
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
		!model.IsValidId(input.AuditEventID) || input.AuditAt < 1 || f.effects.expired != 1 ||
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
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" ||
		f.systemAudit.failedCode != fault.Code || f.effects.expired != 0 ||
		strings.Join(f.order, ",") != "expiry.list,system.audit,expiry.commit,system.audit.fail" {
		t.Fatalf("error=%v audit=%#v effects=%#v order=%v", err, f.systemAudit, f.effects, f.order)
	}
}

func TestReallowRequiresExactSuspensionAndKeepsPrivateReasonOutOfEffects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	suspensionID := model.NewAttemptSuspensionID()
	f.persistence.reallowResult = reallowResultFixture(t, f, suspensionID, false)
	reason := "manager verified secure connectivity"
	result, err := f.service.Reallow(context.Background(), f.call, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: reason, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := f.persistence.reallow
	if input == nil || input.SuspensionID != suspensionID || input.ExpectedAttemptRevision != 2 || input.PrivateReason != reason ||
		input.ActorUserID != f.userID || input.ManagerOverride || result.Attempt.State != model.ExamAttemptActive ||
		result.Suspension.State != model.AttemptSuspensionClosed || f.effects.reallowed != 1 ||
		strings.Join(f.order, ",") != "manager.manage,sitting,audit,reallow,effect.reallow" {
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
}

func TestReallowValidatesReasonBeforeAuthorization(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"", " padded ", "\xff", strings.Repeat("é", model.AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		f := newFixture(t)
		_, err := f.service.Reallow(context.Background(), f.call, ReallowCommand{
			ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: model.NewAttemptSuspensionID(),
			ExpectedAttemptRevision: 1, PrivateReason: reason, Idempotency: &store.CommandIdempotency{},
		})
		if err == nil || len(f.order) != 0 || f.persistence.reallow != nil {
			t.Fatalf("reason=%q error=%v order=%v", reason, err, f.order)
		}
	}
}

func TestReallowReplaySuppressesEffectAndPropagatesAuthorizedOverride(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.managerOverride = true
	suspensionID := model.NewAttemptSuspensionID()
	f.persistence.reallowResult = reallowResultFixture(t, f, suspensionID, true)
	result, err := f.service.Reallow(context.Background(), f.call, ReallowCommand{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: 2, PrivateReason: "authorized override review", Idempotency: &store.CommandIdempotency{},
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
	attempt, err := model.NewExamAttempt(f.attemptID, f.sitting.ExamID, f.sitting.ID, f.userID, f.revision.ID, f.at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	attempt.UpdatedAt, attempt.Revision = f.at, 3
	suspension := &store.ExamAttemptSuspensionView{ID: suspensionID, AttemptID: f.attemptID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 2,
		State: model.AttemptSuspensionClosed, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: f.at.Add(-time.Minute),
		EndedAt: model.OptionalTimeFrom(f.at), ReallowedByUserID: f.userID}
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
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || !result.Replayed || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect" {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestReconnectThatOpensANewConnectionPublishesOneOpenEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || result.FirstAdmission || !result.ConnectionOpened || result.Replayed || f.effects.opened != 1 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestDifferentKeyConvergenceOnExistingOpenConnectionPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	f.persistence.connectionOpened = false
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || result.FirstAdmission || result.ConnectionOpened || result.Replayed || f.effects.opened != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestConnectReplayOfClosedConnectionRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectClosed = true
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectReplayOfEndedParticipationRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.participationEnded = true
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectRejectsAConnectionOutcomeBoundToAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectionSession = model.NewSessionID()
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.unavailable" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectFailureCompletesAuditAndPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.sitting_unavailable" ||
		f.audit.failedCode != fault.Code || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect,audit.fail" {
		t.Fatalf("error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	}
}

func TestExpiredReplayReturnsCandidateSafeConnectionLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_lost" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestProtectedPresentationUsesCurrentRevisionAndSanitizesCandidateMarkdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	currentID := model.NewExamRevisionID()
	f.persistence.presentation = &store.CandidateExamPresentation{
		AttemptID: f.attemptID, SittingID: f.sitting.ID, AdmissionRevisionID: f.revision.ID, CurrentRevisionID: currentID,
		FocusLossCollectionEnabled: true,
		Title:                      "Algorithms", InstructionsMarkdown: "# Rules\nUse **Go**.\n<script>alert('x')</script>\n[bad](javascript:alert(1))\n![tracker](https://example.test/pixel.png)\n[handbook](https://example.test/handbook)",
		Resources: []store.CandidateExamResource{{ResourceID: model.NewExamResourceID(), DisplayName: "Reference",
			DescriptionMarkdown: "Read _carefully_. <iframe src=https://evil.test></iframe> [data](data:text/html,bad)", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 4, SHA256: strings.Repeat("a", 64)}},
	}
	result, err := f.service.GetPresentation(context.Background(), f.call, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AdmissionRevisionID != f.revision.ID || result.CurrentRevisionID != currentID || !result.FocusLossCollectionEnabled ||
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
		f.persistence.candidateAccess.CandidateUserID != f.userID || f.persistence.candidateAccess.SessionID != f.call.Principal().SessionID {
		t.Fatalf("candidate selector = %#v", f.persistence.candidateAccess)
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
	opened, err := f.service.OpenWorkspaceFile(context.Background(), f.call, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	}, entryID)
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
	f.persistence.authorizedSession = f.call.Principal().SessionID
	principal := f.call.Principal()
	principal.SessionID = model.NewSessionID()
	otherSession := NewCall(principal, f.call.RequestMetadata())
	_, err := f.service.GetPresentation(context.Background(), otherSession, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" ||
		f.persistence.candidateAccess.SessionID != principal.SessionID {
		t.Fatalf("error=%v access=%#v", err, f.persistence.candidateAccess)
	}
}

func TestContentSelectorsPreserveCandidateAccessErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	invalidCall := NewCall(model.Principal{}, model.RequestMetadata{})
	access := CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()}
	for _, open := range []func() error{
		func() error {
			_, err := f.service.OpenResource(context.Background(), invalidCall, access, model.NewExamResourceID())
			return err
		},
		func() error {
			_, err := f.service.OpenWorkspaceFile(context.Background(), invalidCall, access, model.NewAttemptWorkspaceEntryID())
			return err
		},
	} {
		var fault *Fault
		if err := open(); !errors.As(err, &fault) || fault.Code != "authentication.invalid_token" {
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
	page, err := f.service.ListWorkspace(context.Background(), f.call, WorkspaceQuery{Access: CandidateAccess{
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
		_, err := f.service.ListWorkspace(context.Background(), f.call, query)
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "exam.attempt.invalid" || f.workspace.lists != 0 {
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
	page, err := f.service.ListWorkspaceJournal(context.Background(), f.call, WorkspaceJournalQuery{Access: CandidateAccess{
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
	opened, err := f.service.OpenWorkspaceFile(context.Background(), f.call, CandidateAccess{AttemptID: f.attemptID,
		ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()}, entryID)
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
	page, err := f.service.ListManaged(context.Background(), f.call, ListManagedAttemptsQuery{
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
	page, err := f.service.ListManaged(context.Background(), f.call, ListManagedAttemptsQuery{
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
	connected, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: credential, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.order = nil
	f.persistence.closeChanged = true
	closed, err := f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	})
	if err != nil || !closed.Changed || strings.Join(f.order, ",") != "audit,close,effect.close" {
		t.Fatalf("closed=%#v error=%v order=%v", closed, err, f.order)
	}
	f.order = nil
	f.persistence.closeChanged = false
	if _, err = f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
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
		connected, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
			SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
		})
		if err != nil {
			t.Fatal(err)
		}
		principal := f.call.Principal()
		mutate(&principal)
		_, err = f.service.CloseConnection(context.Background(), NewCall(principal, f.call.RequestMetadata()), CloseConnectionCommand{
			AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
			ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
		})
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" || f.effects.closed != 0 {
			t.Fatalf("error=%v effects=%#v", err, f.effects)
		}
	}
}

func TestTrustedCloseRejectsUnknownReasonBeforeAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
		AttemptID: model.NewExamAttemptID(), SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: model.NewAttemptConnectionID(), Reason: "unknown",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.invalid" || len(f.order) != 0 {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
}

type fixture struct {
	service                   *Service
	call                      Call
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
	audit                     *auditFake
	systemAudit               *systemAuditFake
	effects                   *effectsFake
	content                   *contentFake
	managerOverride           bool
	focusSignalID             model.FocusLossSignalID
	focusFlagID               model.IntegrityFlagID
	submissionID              model.SubmissionID
	submissionAuthorizationID model.SubmissionID
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
	f.audit = &auditFake{f: f}
	f.systemAudit = &systemAuditFake{f: f}
	f.effects = &effectsFake{f: f}
	f.content = &contentFake{f: f}
	service, err := New(Dependencies{
		Persistence: f.persistence, Workspace: f.workspace, Submissions: f.submissions, Sittings: &sittingFake{f: f}, Managers: &managerFake{f: f},
		Auditor: f.audit, SystemAuditor: f.systemAudit, Effects: f.effects, EffectFailures: f.effects, Content: f.content,
		Now: func() time.Time { return f.at }, NewAttemptID: model.NewExamAttemptID, NewWorkspaceID: model.NewExamAttemptWorkspaceID,
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
	f.call = NewCall(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: f.at}, model.RequestMetadata{RequestID: "attempt-test"})
	return f
}

type sittingFake struct{ f *fixture }

func (fake *sittingFake) Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	fake.f.order = append(fake.f.order, "sitting")
	return &store.ExamSittingSnapshot{Sitting: fake.f.sitting}, nil
}

type managerFake struct{ f *fixture }

func (fake *managerFake) AuthorizeSittingView(context.Context, Call, model.ExamSittingID) error {
	fake.f.order = append(fake.f.order, "manager.authorize")
	return nil
}
func (fake *managerFake) AuthorizeSittingManage(context.Context, Call, model.ExamSittingID) (bool, error) {
	fake.f.order = append(fake.f.order, "manager.manage")
	return fake.f.managerOverride, nil
}
func (fake *managerFake) AuthorizeSubmissionView(_ context.Context, _ Call, submissionID model.SubmissionID) error {
	fake.f.order = append(fake.f.order, "submission.authorize")
	fake.f.submissionAuthorizationID = submissionID
	return nil
}

type auditFake struct {
	f          *fixture
	values     map[string]any
	failedCode string
}

type systemAuditFake struct {
	f          *fixture
	values     map[string]any
	failedCode string
}

func (fake *systemAuditFake) Begin(_ context.Context, _ model.Action, _ model.Resource, _ model.RoleScopeType, _ string, _ string, values map[string]any) (string, error) {
	fake.f.order = append(fake.f.order, "system.audit")
	fake.values = values
	return model.NewId(), nil
}
func (fake *systemAuditFake) Fail(_ context.Context, _ string, code string) error {
	fake.f.order = append(fake.f.order, "system.audit.fail")
	fake.failedCode = code
	return nil
}

func (fake *auditFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.RoleScopeType, _ string, _ string, values map[string]any) (string, error) {
	fake.f.order = append(fake.f.order, "audit")
	fake.values = values
	return model.NewId(), nil
}
func (fake *auditFake) Fail(_ context.Context, _ string, code string) error {
	fake.f.order = append(fake.f.order, "audit.fail")
	fake.failedCode = code
	return nil
}

type effectsFake struct {
	f                *fixture
	opened, closed   int
	expired          int
	reallowed        int
	workspaceChanged int
	focusLoss        int
	submitted        int
}

func (fake *effectsFake) ConnectionOpened(context.Context, ConnectionResult) error {
	fake.f.order = append(fake.f.order, "effect.open")
	fake.opened++
	return nil
}
func (fake *effectsFake) ConnectionClosed(context.Context, ConnectionClosedResult) error {
	fake.f.order = append(fake.f.order, "effect.close")
	fake.closed++
	return nil
}
func (fake *effectsFake) ParticipationExpired(context.Context, ParticipationExpiry) error {
	fake.f.order = append(fake.f.order, "effect.expire")
	fake.expired++
	return nil
}
func (fake *effectsFake) AttemptReallowed(context.Context, ReallowResult) error {
	fake.f.order = append(fake.f.order, "effect.reallow")
	fake.reallowed++
	return nil
}
func (fake *effectsFake) WorkspaceChanged(context.Context, WorkspaceMutationResult) error {
	fake.f.order = append(fake.f.order, "effect.workspace")
	fake.workspaceChanged++
	return nil
}
func (fake *effectsFake) FocusLossEvaluated(context.Context, FocusLossEvaluation) error {
	fake.f.order = append(fake.f.order, "effect.focus_loss")
	fake.focusLoss++
	return nil
}
func (fake *effectsFake) AttemptSubmitted(context.Context, SubmissionResult) error {
	fake.f.order = append(fake.f.order, "effect.submit")
	fake.submitted++
	return nil
}
func (*effectsFake) Report(context.Context, string, error) {}

type contentFake struct {
	f               *fixture
	starterID       model.StarterWorkspaceObjectID
	attemptObjectID model.AttemptWorkspaceObjectID
	attemptContent  *model.AttemptWorkspaceContent
	openAttemptID   model.AttemptWorkspaceObjectID
	openAttemptBody string
}

func (fake *contentFake) StageAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID, _ io.Reader, _ int64, _ string) (*model.AttemptWorkspaceContent, error) {
	fake.f.order = append(fake.f.order, "content.stage")
	fake.attemptObjectID = id
	return fake.attemptContent, nil
}
func (fake *contentFake) OpenAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	fake.openAttemptID = id
	return io.NopCloser(strings.NewReader(fake.openAttemptBody)), nil
}

func (*contentFake) OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("resource")), nil
}
func (fake *contentFake) OpenStarterWorkspaceObject(_ context.Context, id model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	fake.starterID = id
	return io.NopCloser(strings.NewReader("workspace")), nil
}

type attemptStoreFake struct {
	f                  *fixture
	connect            *store.ExamAttemptConnect
	connectErr         error
	replayed           bool
	firstAdmission     bool
	connectionOpened   bool
	connectClosed      bool
	participationEnded bool
	connectionSession  model.SessionID
	closeChanged       bool
	attempt            *model.ExamAttempt
	workspace          *model.ExamAttemptWorkspace
	participation      *store.ExamAttemptParticipationView
	connection         *model.AttemptConnection
	presentation       *store.CandidateExamPresentation
	candidateAccess    store.CandidateAttemptAccess
	authorizedSession  model.SessionID
	managerOptions     store.ExamAttemptManagerListOptions
	managerRows        []store.ExamAttemptManagerSnapshot
	renew              *store.ExamAttemptParticipationRenewal
	renewResult        *store.ExamAttemptParticipationRenewalResult
	renewErr           error
	focusAccess        store.ExamAttemptFocusLossAccess
	focusTarget        *store.ExamAttemptFocusLossTarget
	focusSignal        *store.ExamAttemptFocusLossSignal
	focusResult        *store.ExamAttemptFocusLossResult
	focusErr           error
	expiryDue          []store.ExamAttemptParticipationExpiryDue
	resolvedExpiry     *store.ExamAttemptParticipationExpiryDue
	resolveExpiryErr   error
	expireInput        *store.ExamAttemptParticipationExpiry
	expireResult       *store.ExamAttemptParticipationExpiryResult
	expireErr          error
	reallow            *store.ExamAttemptReallow
	reallowResult      *store.ExamAttemptReallowResult
	reallowErr         error
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

func (fake *attemptWorkspaceStoreFake) List(_ context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	fake.lists++
	fake.options = options
	return fake.page, nil
}
func (fake *attemptWorkspaceStoreFake) ResolveFile(context.Context, store.CandidateAttemptAccess, model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	return fake.file, nil
}
func (fake *attemptWorkspaceStoreFake) ListJournal(_ context.Context, options store.CandidateWorkspaceJournalOptions) (*store.CandidateWorkspaceJournalPage, error) {
	fake.journalOptions = options
	return fake.journalPage, nil
}
func (fake *attemptWorkspaceStoreFake) ResolveMutationTarget(_ context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	fake.f.order = append(fake.f.order, "workspace.resolve")
	fake.resolvedAccess = access
	return fake.target, nil
}
func (fake *attemptWorkspaceStoreFake) ReserveObject(_ context.Context, input *store.ExamAttemptWorkspaceObjectReservation) (*model.AttemptWorkspaceObject, error) {
	fake.f.order = append(fake.f.order, "workspace.reserve")
	fake.reservation = input
	return model.NewStagedAttemptWorkspaceObject(input.ObjectID, fake.target.WorkspaceID, fake.f.at, fake.f.at.Add(time.Hour))
}
func (fake *attemptWorkspaceStoreFake) MarkObjectReady(_ context.Context, input *store.ExamAttemptWorkspaceObjectReady) (*model.AttemptWorkspaceObject, error) {
	fake.f.order = append(fake.f.order, "workspace.ready")
	fake.ready = input
	object, err := model.NewStagedAttemptWorkspaceObject(input.ObjectID, fake.target.WorkspaceID, fake.f.at, fake.f.at.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	if err = object.MarkContentReady(input.ContentVersion, input.Content.MediaType, input.Content.SizeBytes, input.Content.SHA256, fake.f.at.Add(time.Second)); err != nil {
		return nil, err
	}
	return object, nil
}
func (fake *attemptWorkspaceStoreFake) ApplyMutation(_ context.Context, mutation *store.ExamAttemptWorkspaceMutation, _ *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutationResult, error) {
	fake.f.order = append(fake.f.order, "workspace.apply")
	fake.mutation = mutation
	return fake.mutationResult, fake.mutationErr
}

func (fake *attemptWorkspaceStoreFake) MarkObjectReclaimable(_ context.Context, id model.AttemptWorkspaceObjectID) error {
	fake.reclaimable = append(fake.reclaimable, id)
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

func (fake *attemptStoreFake) Connect(_ context.Context, input *store.ExamAttemptConnect, _ *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	fake.f.order = append(fake.f.order, "connect")
	fake.connect = input
	if fake.connectErr != nil {
		return nil, fake.connectErr
	}
	var err error
	fake.attempt, err = model.NewExamAttempt(input.AttemptID, fake.f.sitting.ExamID, input.SittingID, input.CandidateUserID, fake.f.revision.ID, fake.f.at)
	if err != nil {
		return nil, err
	}
	fake.workspace, err = model.NewExamAttemptWorkspace(input.WorkspaceID, input.AttemptID, fake.f.at)
	if err != nil {
		return nil, err
	}
	fake.participation = &store.ExamAttemptParticipationView{ID: input.ParticipationID, AttemptID: input.AttemptID,
		State: model.AttemptParticipationActive, Generation: 1, StartedAt: fake.f.at, UpdatedAt: fake.f.at,
		LeaseExpiresAt: fake.f.at.Add(model.AttemptParticipationInitialLease)}
	if fake.participationEnded {
		fake.participation.State = model.AttemptParticipationEnded
		fake.participation.UpdatedAt = fake.f.at.Add(time.Second)
		fake.participation.EndedAt = model.OptionalTimeFrom(fake.f.at.Add(time.Second))
		fake.participation.EndReason = model.AttemptParticipationEndInterrupted
	}
	connectionSession := input.SessionID
	if fake.connectionSession.IsValid() {
		connectionSession = fake.connectionSession
	}
	fake.connection, err = model.NewAttemptConnection(input.ConnectionID, input.AttemptID, input.ParticipationID, connectionSession, fake.f.at)
	if err != nil {
		return nil, err
	}
	if fake.connectClosed {
		if err := fake.connection.Close(model.AttemptConnectionCloseTransport, fake.f.at.Add(time.Second)); err != nil {
			return nil, err
		}
	}
	return &store.ExamAttemptConnectResult{Attempt: fake.attempt, Workspace: fake.workspace, Participation: fake.participation,
		Connection: fake.connection, ClassID: fake.f.sitting.ClassID, FirstAdmission: fake.firstAdmission,
		ConnectionOpened: fake.connectionOpened, Replayed: fake.replayed}, nil
}
func (fake *attemptStoreFake) RenewParticipation(_ context.Context, input *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	fake.f.order = append(fake.f.order, "renew")
	fake.renew = input
	return fake.renewResult, fake.renewErr
}
func (fake *attemptStoreFake) ResolveFocusLossTarget(_ context.Context, access store.ExamAttemptFocusLossAccess) (*store.ExamAttemptFocusLossTarget, error) {
	fake.f.order = append(fake.f.order, "focus.resolve")
	fake.focusAccess = access
	return fake.focusTarget, fake.focusErr
}
func (fake *attemptStoreFake) RecordFocusLoss(_ context.Context, input *store.ExamAttemptFocusLossSignal) (*store.ExamAttemptFocusLossResult, error) {
	fake.f.order = append(fake.f.order, "focus.record")
	fake.focusSignal = input
	return fake.focusResult, fake.focusErr
}
func (fake *attemptStoreFake) ResolveParticipationExpiry(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64) (*store.ExamAttemptParticipationExpiryDue, error) {
	fake.f.order = append(fake.f.order, "expiry.resolve")
	return fake.resolvedExpiry, fake.resolveExpiryErr
}
func (fake *attemptStoreFake) ListExpiredParticipations(_ context.Context, _ int) ([]store.ExamAttemptParticipationExpiryDue, error) {
	fake.f.order = append(fake.f.order, "expiry.list")
	return fake.expiryDue, nil
}
func (fake *attemptStoreFake) ExpireParticipation(_ context.Context, input *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	fake.f.order = append(fake.f.order, "expiry.commit")
	fake.expireInput = input
	return fake.expireResult, fake.expireErr
}
func (fake *attemptStoreFake) ReallowAttempt(_ context.Context, input *store.ExamAttemptReallow, _ *store.CommandIdempotency) (*store.ExamAttemptReallowResult, error) {
	fake.f.order = append(fake.f.order, "reallow")
	fake.reallow = input
	return fake.reallowResult, fake.reallowErr
}
func (fake *attemptStoreFake) CloseConnection(_ context.Context, input *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	fake.f.order = append(fake.f.order, "close")
	if input.CandidateUserID != fake.connect.CandidateUserID || input.SessionID != fake.connect.SessionID {
		return nil, store.NewErrNotFound("attempt_connection", input.ConnectionID.String())
	}
	connection := *fake.connection
	if err := connection.Close(input.Reason, fake.f.at.Add(time.Second)); err != nil {
		return nil, err
	}
	return &store.ExamAttemptConnectionCloseResult{AttemptID: fake.attempt.ID, SittingID: fake.connect.SittingID,
		CandidateUserID: fake.connect.CandidateUserID, Connection: &connection, Changed: fake.closeChanged}, nil
}
func (*attemptStoreFake) Get(context.Context, model.ExamID, model.ExamAttemptID) (*store.ExamAttemptManagerSnapshot, error) {
	return nil, errors.New("not configured")
}
func (fake *attemptStoreFake) List(_ context.Context, options store.ExamAttemptManagerListOptions) ([]store.ExamAttemptManagerSnapshot, error) {
	fake.f.order = append(fake.f.order, "manager.list")
	fake.managerOptions = options
	return fake.managerRows, nil
}
func (fake *attemptStoreFake) GetCandidatePresentation(_ context.Context, access store.CandidateAttemptAccess) (*store.CandidateExamPresentation, error) {
	fake.candidateAccess = access
	if fake.authorizedSession.IsValid() && access.SessionID != fake.authorizedSession {
		return nil, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	return fake.presentation, nil
}
func (*attemptStoreFake) ResolveCandidateResource(context.Context, store.CandidateAttemptAccess, model.ExamResourceID) (*store.CandidateResourceContent, error) {
	return nil, errors.New("not configured")
}
