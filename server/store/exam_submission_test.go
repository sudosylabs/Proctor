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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSubmissionStoreOwnsAtomicCandidateSealAndExactReplaySelector(t *testing.T) {
	t.Parallel()

	access := ExamSubmissionSealAccess{
		AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(), Generation: 3,
		ConnectionID: model.NewAttemptConnectionID(), CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), ExpectedCurrentRevisionID: model.NewExamRevisionID(),
		ExpectedWorkspaceCursor: 41, FinalFocusLossSequence: 9,
	}
	command := ExamSubmissionSeal{
		SubmissionID: model.NewSubmissionID(), Access: access,
		AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
	}
	target := ExamSubmissionSealTarget{
		ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ClassID: model.NewClassID(),
		CandidateUserID: access.CandidateUserID, WorkspaceID: model.NewExamAttemptWorkspaceID(),
		CurrentRevisionID: access.ExpectedCurrentRevisionID,
	}
	receipt := ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		SubmissionID: command.SubmissionID, AttemptID: access.AttemptID, ExamRevisionID: access.ExpectedCurrentRevisionID,
		State:           model.ExamAttemptSubmitted,
		WorkspaceCursor: 41, ManifestDigest: strings.Repeat("a", 64), SubmittedAt: time.Unix(100, 0).UTC(),
	}
	result := ExamSubmissionSealResult{
		Receipt: receipt, ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: access.CandidateUserID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, ConnectionID: access.ConnectionID, Replayed: true,
	}
	require.Equal(t, int64(41), command.Access.ExpectedWorkspaceCursor, "command/result = %#v / %#v", command, result)
	require.Equal(t, int64(9), command.Access.FinalFocusLossSequence, "command/result = %#v / %#v", command, result)
	require.Equal(t, model.ExamAttemptSubmitted, result.Receipt.State, "command/result = %#v / %#v", command, result)
	require.True(t, result.Replayed, "command/result = %#v / %#v", command, result)
	methods := reflect.TypeOf((*ExamSubmissionStore)(nil)).Elem()
	for _, name := range []string{"ResolveSealTarget", "Seal", "Resolve", "Get", "ListManifest", "ResolveFile"} {
		_, exists := methods.MethodByName(name)
		require.True(t, exists, "ExamSubmissionStore is missing %s", name)
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
		AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(), AcademicUnitID: model.NewAcademicUnitID(),
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
	require.Equal(t, submissionID, authorization.SubmissionID, "authorization/page/selector = %#v / %#v / %#v", authorization, page, selector)
	require.Equal(t, "cmd/main.go", page.Items[0].Path, "authorization/page/selector = %#v / %#v / %#v", authorization, page, selector)
	require.Equal(t, entryID, selector.Entry.EntryID, "authorization/page/selector = %#v / %#v / %#v", authorization, page, selector)
	require.Equal(t, entryID, options.AfterEntryID, "authorization/page/selector = %#v / %#v / %#v", authorization, page, selector)
	_, exists := reflect.TypeOf(options).FieldByName("AfterPath")
	require.False(t, exists, "Submission manifest cursor exposes a protected Workspace Path")
	assertSubmissionProjectionHasNoSecrets(t, reflect.TypeOf(page))
}

func assertSubmissionProjectionHasNoSecrets(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		for _, forbidden := range []string{"credential", "session", "private", "evidence", "source"} {
			require.NotContains(t, name, forbidden, "outbound")
		}
	}
}
