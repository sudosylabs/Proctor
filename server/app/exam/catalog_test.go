// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
)

func TestListCatalogUsesAuthorizedPersistenceVisibilityAndBoundedSummary(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	visibility := store.ExamListVisibility{ActorUserID: fixture.userID, OrdinaryMembershipAt: time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC), OrdinaryAcademicUnitRootIDs: []model.AcademicUnitID{fixture.unitID}}
	fixture.authorizer.listVisibility = visibility
	updatedAt := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	fixture.persistence.summaries = []store.ExamSummary{{ID: fixture.examID, AcademicUnitID: fixture.unitID, CreatorUserID: fixture.userID,
		OwnerUserID: fixture.userID, Title: "Systems", UpdatedAt: updatedAt, Revision: 3, ManagerCount: 2}}
	page, err := fixture.service.ListExams(context.Background(), fixture.invocation, ListQuery{AcademicUnitID: fixture.unitID, Query: "  Systems  ",
		ArchiveFilter: store.ExamArchiveActive, Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "Systems", page.Items[0].Title)
	require.Equal(t, 2, page.Items[0].ManagerCount)
	require.Equal(t, fixture.userID, fixture.persistence.listOptions.Visibility.ActorUserID)
	require.Equal(t, visibility, fixture.persistence.listOptions.Visibility)
	require.Equal(t, fixture.unitID, fixture.persistence.listOptions.AcademicUnitID)
	require.Equal(t, "Systems", fixture.persistence.listOptions.Query)
	// Contract: list visibility is authorized before the Store lists Exams.
	ordertest.RequireInOrder(t, *fixture.order, "authorize.list", "store.list")
}

func TestArchiveOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	result, err := fixture.service.ArchiveExam(context.Background(), fixture.invocation, ArchiveCommand{ExamID: fixture.examID,
		ExpectedExamRevision: 1, IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.True(t, result.IsArchived(), "archive = %#v store=%#v effect", result, fixture.persistence.archive)
	require.Equal(t, int64(2), result.Revision, "archive = %#v store=%#v effect", result, fixture.persistence.archive)
	require.NotNil(t, fixture.persistence.archive, "archive = %#v store=%#v effect", result, fixture.persistence.archive)
	require.Equal(t, int64(2), fixture.effects.archivedRevision, "archive = %#v store=%#v effect", result, fixture.persistence.archive)
	require.Equal(t, model.ActionExamManage, fixture.authorizer.action, "authorization/audit")
	require.Len(t, fixture.auditor.value, 3, "authorization/audit")
	require.Equal(t, fixture.examID.String(), fixture.auditor.value["exam_id"], "authorization/audit")
	require.Equal(t, int64(1), fixture.auditor.value["expected_exam_revision"], "authorization/audit")
	require.Equal(t, int64(2), fixture.auditor.value["exam_revision"], "authorization/audit")
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationArchive, "test-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_exam_revision":1}`, fixture.examID))
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	ordertest.RequireInOrder(t, *fixture.order, "authorize", "audit.begin", "store.archive", "effect.archived")
}

func TestArchiveReplayAfterCurrentArchiveDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.archived = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	_, err := fixture.service.ArchiveExam(context.Background(), fixture.invocation, ArchiveCommand{ExamID: fixture.examID,
		ExpectedExamRevision: 1, IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.NotNil(t, fixture.persistence.archive, "replay store/effect")
	require.Equal(t, int64(0), fixture.effects.archivedRevision, "replay store/effect")
}

func TestArchiveNewAttemptRejectsArchivedAndStaleExamThroughAuditedStoreGuard(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		archived bool
		revision int64
		want     string
	}{{"archived", true, 1, "exam.archived"}, {"stale", false, 2, "exam.revision_conflict"}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.persistence.archived = test.archived
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			_, err := fixture.service.ArchiveExam(context.Background(), fixture.invocation, ArchiveCommand{ExamID: fixture.examID,
				ExpectedExamRevision: test.revision, IdempotencyKey: "test-key"})
			var fault *apperror.Error
			require.ErrorAs(t, err, &fault, "archive error/audit")
			require.Equal(t, test.want, fault.Code(), "archive error/audit")
			require.Equal(t, test.want, fixture.auditor.failedCode, "archive error/audit")
		})
	}
}

func TestListExamsDefaultsLimitAndActiveArchiveFilter(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.authorizer.listVisibility = store.ExamListVisibility{ActorUserID: fixture.userID}
	_, err := fixture.service.ListExams(context.Background(), fixture.invocation, ListQuery{})
	require.NoError(t, err)
	options := fixture.persistence.listOptions
	require.Equal(t, 50, options.Limit, "list options = %#v, want limit 50 and active archive filter", options)
	require.Equal(t, store.ExamArchiveActive, options.ArchiveFilter, "list options = %#v, want limit 50 and active archive filter", options)
}
