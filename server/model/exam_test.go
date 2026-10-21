// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewExamAuthoringState(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	examID := NewExamID()
	unitID := NewAcademicUnitID()
	creatorID := NewUserID()

	exam, err := NewExam(examID, unitID, creatorID, at)
	require.NoError(t, err)
	draft, err := NewExamDraft(examID, "  Systems Programming  ", "", DefaultExamPolicySet(), at)
	require.NoError(t, err)
	manager, err := NewExamManager(examID, creatorID, creatorID, at)
	require.NoError(t, err)

	require.Equal(t, creatorID, exam.CreatorUserID, "creator/owner")
	require.Equal(t, creatorID, exam.OwnerUserID, "creator/owner")
	require.True(t, exam.DefaultRevisionID.IsZero(), "default revision/revision = %q", exam.DefaultRevisionID)
	require.Equal(t, int64(1), exam.Revision, "default revision/revision = %q", exam.DefaultRevisionID)
	require.Equal(t, examID, draft.ExamID)
	require.Equal(t, "Systems Programming", draft.Title)
	require.Empty(t, draft.InstructionsMarkdown)
	require.Equal(t, int64(1), draft.Revision)
	require.Equal(t, examID, manager.ExamID)
	require.Equal(t, creatorID, manager.UserID)
	require.Equal(t, creatorID, manager.GrantedByUserID)
	wantUTC := at.UTC()
	require.True(t, exam.CreatedAt.Equal(wantUTC), "authoring timestamps were not normalized to UTC")
	require.True(t, exam.UpdatedAt.Equal(wantUTC), "authoring timestamps were not normalized to UTC")
	require.True(t, draft.UpdatedAt.Equal(wantUTC), "authoring timestamps were not normalized to UTC")
	require.True(t, manager.GrantedAt.Equal(wantUTC), "authoring timestamps were not normalized to UTC")
}

func TestExamArchiveRecordsImmutableTimeAndAdvancesRevision(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	exam, err := NewExam(NewExamID(), NewAcademicUnitID(), NewUserID(), createdAt)
	require.NoError(t, err)
	archivedAt := createdAt.Add(time.Hour)
	require.NoError(t, exam.Archive(archivedAt))
	require.True(t, exam.ArchivedAt.Valid)
	require.True(t, exam.ArchivedAt.Time.Equal(archivedAt))
	require.True(t, exam.UpdatedAt.Equal(archivedAt))
	require.Equal(t, int64(2), exam.Revision)
	first := *exam
	require.Error(t, exam.Archive(archivedAt.Add(time.Hour)), "second Archive succeeded")
	require.Equal(t, first, *exam, "second Archive mutated Exam: %#v", exam)
}

func TestExamAndDraftValidateIndependently(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	exam, err := NewExam(NewExamID(), NewAcademicUnitID(), NewUserID(), at)
	require.NoError(t, err)
	draft, err := NewExamDraft(exam.ID, "Compiler Construction", "Read **carefully**.", DefaultExamPolicySet(), at)
	require.NoError(t, err)

	draft.Revision = 7
	require.NoError(t, draft.Validate())
	require.Equal(t, int64(1), exam.Revision, "draft revision changed exam revision to")

	exam.OwnerUserID = UserID("")
	require.Error(t, exam.Validate(), "expected missing owner to fail")
	exam.OwnerUserID = exam.CreatorUserID
	draft.Title = strings.Repeat("x", ExamTitleMaxRunes+1)
	require.Error(t, draft.Validate(), "expected oversized title to fail")
	draft.Title = " Compiler Construction "
	require.Error(t, draft.Validate(), "expected persisted untrimmed title to fail")
}

func TestExamDraftApplyTextPatchPreservesPresenceAndRevision(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	draft, err := NewExamDraft(NewExamID(), "Systems", "Use **Go**.", DefaultExamPolicySet(), at)
	require.NoError(t, err)
	title := "  Distributed Systems  "
	clearInstructions := ""
	changed, err := draft.ApplyTextPatch(&title, &clearInstructions, at.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "Distributed Systems", draft.Title)
	require.Empty(t, draft.InstructionsMarkdown)
	require.Equal(t, int64(2), draft.Revision)
	require.True(t, draft.UpdatedAt.Equal(at.Add(time.Minute)))

	unchangedTitle := draft.Title
	changed, err = draft.ApplyTextPatch(&unchangedTitle, nil, at.Add(2*time.Minute))
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, int64(2), draft.Revision)
	require.True(t, draft.UpdatedAt.Equal(at.Add(time.Minute)))
}

func TestExamDraftApplyTextPatchRejectsMissingOrInvalidFieldsAtomically(t *testing.T) {
	t.Parallel()
	draft, err := NewExamDraft(NewExamID(), "Systems", "Instructions", DefaultExamPolicySet(), time.Now().UTC())
	require.NoError(t, err)
	original := *draft
	_, err = draft.ApplyTextPatch(nil, nil, time.Now().UTC())
	require.Error(t, err, "ApplyTextPatch accepted no authored field")
	invalidTitle := "   "
	_, err = draft.ApplyTextPatch(&invalidTitle, nil, time.Now().UTC())
	require.Error(t, err, "ApplyTextPatch accepted an empty title")
	invalidUTF8Title := string([]byte{0xff})
	_, err = draft.ApplyTextPatch(&invalidUTF8Title, nil, time.Now().UTC())
	require.Error(t, err, "ApplyTextPatch accepted an invalid UTF-8 title")
	tooLarge := strings.Repeat("x", ExamInstructionsMarkdownMaxBytes+1)
	_, err = draft.ApplyTextPatch(nil, &tooLarge, time.Now().UTC())
	require.Error(t, err, "ApplyTextPatch accepted oversized instructions")
	require.Equal(t, original, *draft, "failed patch mutated draft: got %#v", draft)
}

func TestNewExamDraftRejectsInvalidUTF8Title(t *testing.T) {
	t.Parallel()
	_, err := NewExamDraft(NewExamID(), string([]byte{0xff}), "", DefaultExamPolicySet(), time.Now().UTC())
	require.Error(t, err, "NewExamDraft accepted an invalid UTF-8 title")
}
