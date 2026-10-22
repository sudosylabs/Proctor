// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testExamCatalogListAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-catalog-unit")
	creator := saveUser(t, ctx, ss)
	firstAt := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	membershipBase := model.NowUTC()
	membership, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: creator.ID, StartsAt: membershipBase.Add(-time.Minute),
	})
	require.NoError(t, err)
	first := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt, "catalog-first_100%")
	second := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt.Add(time.Minute), "catalog!second")

	visibility := store.ExamListVisibility{ActorUserID: creator.ID, OrdinaryMembershipAt: membershipBase.Add(10 * time.Minute), OrdinaryInstitutionWide: true}
	page, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveActive, Limit: 1, Visibility: visibility})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, second.Value.Exam.ID, page[0].ID)
	require.Equal(t, second.Value.Draft.Title, page[0].Title)
	require.Equal(t, 1, page[0].ManagerCount)
	require.False(t, page[0].ArchivedAt.Valid)
	next, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{
		ArchiveFilter: store.ExamArchiveActive, Limit: 1, Visibility: visibility,
		BeforeUpdatedAt: page[0].UpdatedAt, BeforeExamID: page[0].ID,
	})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, first.Value.Exam.ID, next[0].ID)
	for _, search := range []struct {
		query string
		want  model.ExamID
	}{
		{query: "SECOND", want: second.Value.Exam.ID},
		{query: "_", want: first.Value.Exam.ID},
		{query: "%", want: first.Value.Exam.ID},
		{query: "!", want: second.Value.Exam.ID},
	} {
		found, searchErr := ss.ExamAuthoring().List(ctx, store.ExamListOptions{
			Query: search.query, ArchiveFilter: store.ExamArchiveActive, Limit: 50, Visibility: visibility,
		})
		require.NoError(t, searchErr)
		require.Len(t, found, 1, "search %q", search.query)
		require.Equal(t, search.want, found[0].ID, "search %q", search.query)
	}

	outsider := saveUser(t, ctx, ss)
	hidden, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveAll, Limit: 50,
		Visibility: store.ExamListVisibility{ActorUserID: outsider.ID, OrdinaryMembershipAt: membershipBase.Add(10 * time.Minute), OrdinaryInstitutionWide: true}})
	require.NoError(t, err)
	require.Empty(t, hidden)
	overridden, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50,
		Visibility: store.ExamListVisibility{ActorUserID: outsider.ID, OverrideInstitutionWide: true}})
	require.NoError(t, err)
	require.Len(t, overridden, 2)
	otherUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-catalog-other-unit")
	_ = createCatalogExam(t, ctx, ss, otherUnit.ID, creator.ID, firstAt.Add(2*time.Minute), "catalog-other-unit")
	exact, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50, Visibility: visibility})
	require.NoError(t, err)
	require.Len(t, exact, 2)
	require.Equal(t, unit.ID, exact[0].AcademicUnitID)
	require.Equal(t, unit.ID, exact[1].AcademicUnitID)
	_, err = EndAcademicUnitMember(ctx, ss, membership.ID, membership.Revision, membershipBase.Add(5*time.Minute))
	require.NoError(t, err)
	revokedMembership, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50, Visibility: visibility})
	require.NoError(t, err)
	require.Empty(t, revokedMembership)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: creator.ID, StartsAt: membershipBase.Add(6 * time.Minute),
	})
	require.NoError(t, err)

	archiveAt := firstAt.Add(2 * time.Hour)
	archive := newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 1, archiveAt)
	command := examCommand(creator.ID, "exam.archive", "archive-key", "archive-command")
	archived, err := ss.ExamAuthoring().Archive(ctx, archive, command)
	require.NoError(t, err)
	require.False(t, archived.Replayed)
	require.True(t, archived.Value.IsArchived())
	require.Equal(t, int64(2), archived.Value.Revision)
	require.True(t, archived.Value.ArchivedAt.Time.Equal(archiveAt))
	replay := newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 1, archiveAt.Add(time.Minute))
	replayed, err := ss.ExamAuthoring().Archive(ctx, replay, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(2), replayed.Value.Revision)
	archivedPage, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveArchived, Limit: 50, Visibility: visibility})
	require.NoError(t, err)
	require.Len(t, archivedPage, 1)
	require.Equal(t, first.Value.Exam.ID, archivedPage[0].ID)
	_, err = ss.ExamAuthoring().Archive(ctx, newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 2, archiveAt.Add(time.Minute)), examCommand(creator.ID, "exam.archive", "archive-again", "archive-again-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "exam_archived", conflict.Constraint)

	concurrent := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt.Add(3*time.Minute), "catalog-concurrent")
	left := newExamArchive(t, ctx, ss, concurrent.Value.Exam.ID, creator.ID, 1, archiveAt.Add(2*time.Minute))
	right := newExamArchive(t, ctx, ss, concurrent.Value.Exam.ID, creator.ID, 1, archiveAt.Add(3*time.Minute))
	commands := []*store.CommandIdempotency{
		examCommand(creator.ID, "exam.archive", "archive-concurrent-left", "archive-concurrent-left-command"),
		examCommand(creator.ID, "exam.archive", "archive-concurrent-right", "archive-concurrent-right-command"),
	}
	inputs := []*store.ExamArchive{left, right}
	type archiveResult struct {
		result *store.ExamArchiveCommandResult
		err    error
	}
	results := make(chan archiveResult, 2)
	var workers sync.WaitGroup
	for index := range inputs {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			result, archiveErr := ss.ExamAuthoring().Archive(ctx, inputs[index], commands[index])
			results <- archiveResult{result: result, err: archiveErr}
		}(index)
	}
	workers.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		if result.err == nil && result.result != nil && result.result.Value.IsArchived() {
			succeeded++
			continue
		}
		var archiveConflict *store.ErrConflict
		if errors.As(result.err, &archiveConflict) && archiveConflict.Constraint == "exam_archived" {
			conflicted++
			continue
		}
		require.FailNow(t, fmt.Sprintf("concurrent archive result = %#v, err = %v", result.result, result.err))
	}
	require.Equal(t, 1, succeeded, "concurrent archives: successes")
	require.Equal(t, 1, conflicted, "concurrent archives: successes")
}

func createCatalogExam(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time, key string) *store.ExamAuthoringCommandResult {
	t.Helper()
	creation := newExamAuthoringCreation(t, ctx, ss, unitID, creatorID, at)
	creation.Draft.Title = key
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creatorID, "exam.create", key, key+"-command"))
	require.NoError(t, err)
	return created
}

func newExamArchive(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, at time.Time) *store.ExamArchive {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage),
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return &store.ExamArchive{ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision,
		ArchivedAt: at, AuditEventID: audit.ID, AuditAt: at}
}
