// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptWorkspaceMutationContractUsesSelectiveFencesAndSafeResults(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(),
		ParticipationID: model.NewAttemptParticipationID(), Generation: 2, CandidateUserID: model.NewUserID(),
		SessionID: model.NewSessionID(), DesktopRegistrationID: model.NewDesktopRegistrationID(),
		DPoPKeyThumbprint: strings.Repeat("A", 43), ConnectionID: model.NewAttemptConnectionID(),
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
	require.Equal(t, int64(2), mutation.Access.Generation, "Workspace mutation contract = %#v / %#v", mutation, result)
	require.Empty(t, mutation.DestinationPath, "Workspace mutation contract = %#v / %#v", mutation, result)
	require.True(t, result.Replayed, "Workspace mutation contract = %#v / %#v", mutation, result)
	require.Equal(t, int64(7), result.Change.Cursor, "Workspace mutation contract = %#v / %#v", mutation, result)
	assertNoWorkspaceSecrets(t, reflect.TypeOf(result))
	_, exists := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem().MethodByName("ApplyMutation")
	require.True(t, exists, "ExamAttemptWorkspaceStore is missing ApplyMutation")
}

func TestCandidateWorkspaceRecoveryCursorsNeverCarryPaths(t *testing.T) {
	t.Parallel()
	access := CandidateAttemptAccess{AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(),
		SessionID: model.NewSessionID(), DesktopRegistrationID: model.NewDesktopRegistrationID(),
		DPoPKeyThumbprint: strings.Repeat("A", 43), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	initial := CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: model.AttemptWorkspaceJournalReadMaximum}
	manifest := CandidateWorkspaceListOptions{Access: access, ExpectedCursor: 41,
		AfterEntryID: model.NewAttemptWorkspaceEntryID(), Limit: model.AttemptWorkspaceJournalReadMaximum}
	page := CandidateAttemptWorkspacePage{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 41, RefreshRequired: true}
	journal := CandidateWorkspaceJournalOptions{Access: access, AfterCursor: 40, Limit: model.AttemptWorkspaceJournalReadMaximum}
	recovery := CandidateWorkspaceJournalPage{WorkspaceID: page.WorkspaceID, CurrentCursor: 41, RefreshRequired: true}
	require.Equal(t, int64(-1), initial.ExpectedCursor, "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	require.True(t, initial.AfterEntryID.IsZero(), "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	require.Equal(t, int64(41), manifest.ExpectedCursor, "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	require.Equal(t, int64(41), page.Cursor, "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	require.Equal(t, int64(40), journal.AfterCursor, "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	require.True(t, recovery.RefreshRequired, "manifest=%#v page=%#v journal=%#v recovery=%#v", manifest, page, journal, recovery)
	_, exists := reflect.TypeOf(manifest).FieldByName("AfterPath")
	require.False(t, exists, "candidate manifest cursor exposes a Workspace Path")
}

func TestExamAttemptWorkspaceMutationResolvesSafeAuditAndEffectScope(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 2, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		DesktopRegistrationID: model.NewDesktopRegistrationID(), DPoPKeyThumbprint: strings.Repeat("A", 43),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	target := ExamAttemptWorkspaceMutationTarget{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		ClassID: model.NewClassID(), CandidateUserID: access.CandidateUserID, WorkspaceID: model.NewExamAttemptWorkspaceID()}
	input := ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory,
		EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "cmd", AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	result := ExamAttemptWorkspaceMutationResult{SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: target.CandidateUserID, WorkspaceID: target.WorkspaceID}
	require.NotEmpty(t, input.AuditEventID, "target=%#v input=%#v result=%#v", target, input, result)
	require.False(t, input.AuditAt.IsZero(), "target=%#v input=%#v result=%#v", target, input, result)
	require.Equal(t, target.SittingID, result.SittingID, "target=%#v input=%#v result=%#v", target, input, result)
	require.Equal(t, target.ClassID, result.ClassID, "target=%#v input=%#v result=%#v", target, input, result)
	require.Equal(t, access.CandidateUserID, result.CandidateUserID, "target=%#v input=%#v result=%#v", target, input, result)
	_, exists := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem().MethodByName("ResolveMutationTarget")
	require.True(t, exists, "ExamAttemptWorkspaceStore is missing ResolveMutationTarget")
	assertNoWorkspaceSecrets(t, reflect.TypeOf(target))
}

func TestExamAttemptWorkspaceObjectContractSeparatesStagingFromCleanup(t *testing.T) {
	t.Parallel()
	access := ExamAttemptWorkspaceMutationAccess{AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		Generation: 3, CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		DesktopRegistrationID: model.NewDesktopRegistrationID(), DPoPKeyThumbprint: strings.Repeat("A", 43),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	objectID := model.NewAttemptWorkspaceObjectID()
	reservation := ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: objectID}
	ready := ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: objectID, ContentVersion: model.NewWorkspaceContentVersion(),
		Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 7, SHA256: strings.Repeat("a", 64)}}
	require.Equal(t, ready.ObjectID, reservation.ObjectID, "reservation=%#v ready=%#v", reservation, ready)
	require.Equal(t, int64(7), ready.Content.SizeBytes, "reservation=%#v ready=%#v", reservation, ready)
	methods := reflect.TypeOf((*ExamAttemptWorkspaceStore)(nil)).Elem()
	for _, name := range []string{"ReserveObject", "MarkObjectReady", "MarkObjectReclaimable", "ClaimObjectsForCleanup", "CompleteObjectCleanup", "ReleaseObjectCleanup"} {
		_, exists := methods.MethodByName(name)
		require.True(t, exists, "ExamAttemptWorkspaceStore is missing %s", name)
	}
}

func assertNoWorkspaceSecrets(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "object", "backend", "storage"} {
			require.NotContains(t, name, forbidden, "outbound")
		}
	}
}
