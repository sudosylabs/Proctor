// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptWorkspaceMutationContractUsesSelectiveFencesAndSafeResults(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(),
		ParticipationID: model.NewAttemptParticipationID(), Generation: 2, CandidateUserID: model.NewUserID(),
		SessionID: model.NewSessionID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	entryID := model.NewAttemptWorkspaceEntryID()
	workspaceID := model.NewExamAttemptWorkspaceID()
	mutation := ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationReplaceFile,
		EntryID: entryID, ExpectedPath: "cmd/main.go", ExpectedContentVersion: model.NewWorkspaceContentVersion(),
		ObjectID: model.NewAttemptWorkspaceObjectID()}
	result := ExamAttemptWorkspaceMutationResult{WorkspaceID: workspaceID,
		Change: model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 7,
			EntryID: entryID, EntryKind: model.StarterWorkspaceEntryFile, Operation: mutation.Operation,
			OldPath: mutation.ExpectedPath, NewPath: mutation.ExpectedPath,
			ContentVersion: model.NewWorkspaceContentVersion(), ChangedAt: model.NowUTC()}, Replayed: true}
	if mutation.Access.Generation != 2 || mutation.DestinationPath != "" || !result.Replayed || result.Change.Cursor != 7 {
		t.Fatalf("Workspace mutation contract = %#v / %#v", mutation, result)
	}
	assertNoWorkspaceSecrets(t, reflect.TypeOf(result))
	if _, exists := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem().MethodByName("ApplyMutation"); !exists {
		t.Fatal("ExamAttemptWorkspaceStore is missing ApplyMutation")
	}
}

func TestCandidateWorkspaceRecoveryCursorsNeverCarryPaths(t *testing.T) {
	t.Parallel()
	access := CandidateAttemptAccess{AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(),
		SessionID: model.NewSessionID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	initial := CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: model.AttemptWorkspaceJournalReadMaximum}
	manifest := CandidateWorkspaceListOptions{Access: access, ExpectedCursor: 41,
		AfterEntryID: model.NewAttemptWorkspaceEntryID(), Limit: model.AttemptWorkspaceJournalReadMaximum}
	page := CandidateAttemptWorkspacePage{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 41, RefreshRequired: true}
	journal := CandidateWorkspaceJournalOptions{Access: access, AfterCursor: 40, Limit: model.AttemptWorkspaceJournalReadMaximum}
	recovery := CandidateWorkspaceJournalPage{WorkspaceID: page.WorkspaceID, CurrentCursor: 41, RefreshRequired: true}
	if initial.ExpectedCursor != -1 || !initial.AfterEntryID.IsZero() || manifest.ExpectedCursor != 41 || page.Cursor != 41 ||
		journal.AfterCursor != 40 || !recovery.RefreshRequired {
		t.Fatalf("manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	}
	if _, exists := reflect.TypeOf(manifest).FieldByName("AfterPath"); exists {
		t.Fatal("candidate manifest cursor exposes a Workspace Path")
	}
}

func TestExamAttemptWorkspaceMutationResolvesSafeAuditAndEffectScope(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	target := ExamAttemptWorkspaceMutationTarget{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		ClassID: model.NewClassID(), CandidateUserID: access.CandidateUserID, WorkspaceID: model.NewExamAttemptWorkspaceID()}
	input := ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory,
		EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "cmd", AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	result := ExamAttemptWorkspaceMutationResult{SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: target.CandidateUserID, WorkspaceID: target.WorkspaceID}
	if input.AuditEventID == "" || input.AuditAt < 1 || result.SittingID != target.SittingID || result.ClassID != target.ClassID ||
		result.CandidateUserID != access.CandidateUserID {
		t.Fatalf("target=%#v input=%#v result=%#v", target, input, result)
	}
	if _, exists := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem().MethodByName("ResolveMutationTarget"); !exists {
		t.Fatal("ExamAttemptWorkspaceStore is missing ResolveMutationTarget")
	}
	assertNoWorkspaceSecrets(t, reflect.TypeOf(target))
}

func TestExamAttemptWorkspaceObjectContractSeparatesStagingFromCleanup(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 3, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	objectID := model.NewAttemptWorkspaceObjectID()
	reservation := ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: objectID}
	ready := ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: objectID, ContentVersion: model.NewWorkspaceContentVersion(),
		Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 7, SHA256: strings.Repeat("a", 64)}}
	if reservation.ObjectID != ready.ObjectID || ready.Content.SizeBytes != 7 {
		t.Fatalf("reservation=%#v ready=%#v", reservation, ready)
	}
	methods := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem()
	for _, name := range []string{"ReserveObject", "MarkObjectReady", "MarkObjectReclaimable", "ClaimObjectsForCleanup", "CompleteObjectCleanup", "ReleaseObjectCleanup"} {
		if _, exists := methods.MethodByName(name); !exists {
			t.Fatalf("ExamAttemptWorkspaceStore is missing %s", name)
		}
	}
}

func assertNoWorkspaceSecrets(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "object", "backend", "storage"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("outbound %s exposes %s", typ, typ.Field(index).Name)
			}
		}
	}
}
