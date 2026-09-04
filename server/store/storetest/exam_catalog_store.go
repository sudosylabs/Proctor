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
	"sync"
	"testing"
	"time"

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
	requireNoError(t, err)
	first := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt, "catalog-first_100%")
	second := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt.Add(time.Minute), "catalog!second")

	visibility := store.ExamListVisibility{ActorUserID: creator.ID, OrdinaryMembershipAt: membershipBase.Add(10 * time.Minute), OrdinaryInstitutionWide: true}
	page, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveActive, Limit: 1, Visibility: visibility})
	requireNoError(t, err)
	if len(page) != 1 || page[0].ID != second.Value.Exam.ID || page[0].Title != second.Value.Draft.Title || page[0].ManagerCount != 1 || page[0].ArchivedAt.Valid {
		t.Fatalf("first page = %#v", page)
	}
	next, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{
		ArchiveFilter: store.ExamArchiveActive, Limit: 1, Visibility: visibility,
		BeforeUpdatedAt: page[0].UpdatedAt, BeforeExamID: page[0].ID,
	})
	requireNoError(t, err)
	if len(next) != 1 || next[0].ID != first.Value.Exam.ID {
		t.Fatalf("next page = %#v", next)
	}
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
		requireNoError(t, searchErr)
		if len(found) != 1 || found[0].ID != search.want {
			t.Fatalf("search %q = %#v, want Exam %s", search.query, found, search.want)
		}
	}

	outsider := saveUser(t, ctx, ss)
	hidden, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveAll, Limit: 50,
		Visibility: store.ExamListVisibility{ActorUserID: outsider.ID, OrdinaryMembershipAt: membershipBase.Add(10 * time.Minute), OrdinaryInstitutionWide: true}})
	requireNoError(t, err)
	if len(hidden) != 0 {
		t.Fatalf("ordinary non-manager saw Exams: %#v", hidden)
	}
	overridden, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50,
		Visibility: store.ExamListVisibility{ActorUserID: outsider.ID, OverrideInstitutionWide: true}})
	requireNoError(t, err)
	if len(overridden) != 2 {
		t.Fatalf("override list = %#v", overridden)
	}
	otherUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "exam-catalog-other-unit")
	_ = createCatalogExam(t, ctx, ss, otherUnit.ID, creator.ID, firstAt.Add(2*time.Minute), "catalog-other-unit")
	exact, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50, Visibility: visibility})
	requireNoError(t, err)
	if len(exact) != 2 || exact[0].AcademicUnitID != unit.ID || exact[1].AcademicUnitID != unit.ID {
		t.Fatalf("exact-unit list = %#v", exact)
	}
	_, err = ss.AcademicUnitMember().End(ctx, membership.ID.String(), membership.Revision, model.MillisFromTime(membershipBase.Add(5*time.Minute)))
	requireNoError(t, err)
	revokedMembership, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 50, Visibility: visibility})
	requireNoError(t, err)
	if len(revokedMembership) != 0 {
		t.Fatalf("ended exact membership still exposed Exams: %#v", revokedMembership)
	}
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: creator.ID, StartsAt: membershipBase.Add(6 * time.Minute),
	})
	requireNoError(t, err)

	archiveAt := firstAt.Add(2 * time.Hour)
	archive := newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 1, archiveAt)
	command := examCommand(creator.ID, "exam.archive.v1", "archive-key", "archive-command")
	archived, err := ss.ExamAuthoring().Archive(ctx, archive, command)
	requireNoError(t, err)
	if archived.Replayed || !archived.Value.IsArchived() || archived.Value.Revision != 2 || !archived.Value.ArchivedAt.Time.Equal(archiveAt) {
		t.Fatalf("archived = %#v", archived)
	}
	replay := newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 1, archiveAt.Add(time.Minute))
	replayed, err := ss.ExamAuthoring().Archive(ctx, replay, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Revision != 2 {
		t.Fatalf("archive replay = %#v", replayed)
	}
	archivedPage, err := ss.ExamAuthoring().List(ctx, store.ExamListOptions{ArchiveFilter: store.ExamArchiveArchived, Limit: 50, Visibility: visibility})
	requireNoError(t, err)
	if len(archivedPage) != 1 || archivedPage[0].ID != first.Value.Exam.ID {
		t.Fatalf("archived page = %#v", archivedPage)
	}
	_, err = ss.ExamAuthoring().Archive(ctx, newExamArchive(t, ctx, ss, first.Value.Exam.ID, creator.ID, 2, archiveAt.Add(time.Minute)), examCommand(creator.ID, "exam.archive.v1", "archive-again", "archive-again-command"))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "exam_archived" {
		t.Fatalf("second archive error = %v", err)
	}

	concurrent := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, firstAt.Add(3*time.Minute), "catalog-concurrent")
	left := newExamArchive(t, ctx, ss, concurrent.Value.Exam.ID, creator.ID, 1, archiveAt.Add(2*time.Minute))
	right := newExamArchive(t, ctx, ss, concurrent.Value.Exam.ID, creator.ID, 1, archiveAt.Add(3*time.Minute))
	commands := []*store.CommandIdempotency{
		examCommand(creator.ID, "exam.archive.v1", "archive-concurrent-left", "archive-concurrent-left-command"),
		examCommand(creator.ID, "exam.archive.v1", "archive-concurrent-right", "archive-concurrent-right-command"),
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
		t.Fatalf("concurrent archive result = %#v, err = %v", result.result, result.err)
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent archives: successes=%d conflicts=%d", succeeded, conflicted)
	}
}

func createCatalogExam(t *testing.T, ctx context.Context, ss store.Store, unitID model.AcademicUnitID, creatorID model.UserID, at time.Time, key string) *store.ExamAuthoringCommandResult {
	t.Helper()
	creation := newExamAuthoringCreation(t, ctx, ss, unitID, creatorID, at)
	creation.Draft.Title = key
	created, err := ss.ExamAuthoring().Create(ctx, creation, examCommand(creatorID, "exam.create.v1", key, key+"-command"))
	requireNoError(t, err)
	return created
}

func newExamArchive(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, expectedRevision int64, at time.Time) *store.ExamArchive {
	t.Helper()
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	requireNoError(t, err)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage),
		Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: exam.AcademicUnitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return &store.ExamArchive{ExamID: examID, ActorUserID: actorID, ExpectedRevision: expectedRevision,
		ArchivedAt: model.MillisFromTime(at), AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at)}
}
