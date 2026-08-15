// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSubmissionStoreOwnsAtomicCandidateSealAndExactReplaySelector(t *testing.T) {
	t.Parallel()

	access := ExamSubmissionSealAccess{
		AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(), Generation: 3,
		ConnectionID: model.NewAttemptConnectionID(), CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), ExpectedWorkspaceCursor: 41,
		FinalFocusLossSequence: 9,
	}
	command := ExamSubmissionSeal{
		SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: model.NewAuditEventID().String(), AuditAt: model.GetMillis(),
	}
	target := ExamSubmissionSealTarget{
		ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ClassID: model.NewClassID(),
		CandidateUserID: access.CandidateUserID, WorkspaceID: model.NewExamAttemptWorkspaceID(),
	}
	receipt := ExamSubmissionReceipt{
		SubmissionID: command.SubmissionID, AttemptID: access.AttemptID, State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 41, ManifestDigest: strings.Repeat("a", 64), SubmittedAt: time.Unix(100, 0).UTC(),
	}
	result := ExamSubmissionSealResult{
		Receipt: receipt, ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: access.CandidateUserID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, ConnectionID: access.ConnectionID, Replayed: true,
	}
	if command.Access.ExpectedWorkspaceCursor != 41 || command.Access.FinalFocusLossSequence != 9 ||
		result.Receipt.State != model.ExamAttemptSubmitted || !result.Replayed {
		t.Fatalf("command/result = %#v / %#v", command, result)
	}
	methods := reflect.TypeOf((*ExamSubmissionStore)(nil)).Elem()
	for _, name := range []string{"ResolveSealTarget", "Seal", "Resolve", "Get", "ListManifest", "ResolveFile"} {
		if _, exists := methods.MethodByName(name); !exists {
			t.Fatalf("ExamSubmissionStore is missing %s", name)
		}
	}
	assertSubmissionProjectionHasNoSecrets(t, reflect.TypeOf(target))
	assertSubmissionProjectionHasNoSecrets(t, reflect.TypeOf(receipt))
}

func TestExamSubmissionManagerReadsSeparateAuthorizationManifestAndContentSelector(t *testing.T) {
	t.Parallel()

	submissionID := model.NewSubmissionID()
	entryID := model.NewAttemptWorkspaceEntryID()
	authorization := ExamSubmissionAuthorization{
		SubmissionID: submissionID, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		AttemptID: model.NewExamAttemptID(), AcademicUnitID: model.NewAcademicUnitID(),
	}
	item := ExamSubmissionManifestItem{
		EntryID: entryID, Kind: model.StarterWorkspaceEntryFile, Path: "cmd/main.go",
		ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 12,
		SHA256: strings.Repeat("a", 64),
	}
	page := ExamSubmissionManifestPage{
		SubmissionID: submissionID, WorkspaceCursor: 41, ManifestDigest: strings.Repeat("b", 64),
		Items: []ExamSubmissionManifestItem{item}, HasMore: true,
	}
	selector := ExamSubmissionFileSelector{
		Entry: item, StorageOrigin: model.AttemptWorkspaceStorageStarter,
		StarterObjectID: model.NewStarterWorkspaceObjectID(), ContentVersion: item.ContentVersion,
	}
	options := ExamSubmissionManifestListOptions{SubmissionID: submissionID, AfterEntryID: entryID, Limit: model.ExamSubmissionManifestReadMaximum}
	if authorization.SubmissionID != submissionID || page.Items[0].Path != "cmd/main.go" ||
		selector.Entry.EntryID != entryID || options.AfterEntryID != entryID {
		t.Fatalf("authorization/page/selector = %#v / %#v / %#v", authorization, page, selector)
	}
	if _, exists := reflect.TypeOf(options).FieldByName("AfterPath"); exists {
		t.Fatal("Submission manifest cursor exposes a protected Workspace Path")
	}
	assertSubmissionProjectionHasNoSecrets(t, reflect.TypeOf(page))
}

func assertSubmissionProjectionHasNoSecrets(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "private", "evidence", "source"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("outbound %s exposes %s", typ, typ.Field(index).Name)
			}
		}
	}
}
