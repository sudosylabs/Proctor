// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestListCatalogUsesAuthorizedPersistenceVisibilityAndBoundedSummary(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	visibility := store.ExamListVisibility{ActorUserID: fixture.userID, OrdinaryAcademicUnitRootIDs: []string{fixture.unitID.String()}}
	fixture.authorizer.listVisibility = visibility
	updatedAt := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	fixture.persistence.summaries = []store.ExamSummary{{ID: fixture.examID, AcademicUnitID: fixture.unitID, CreatorUserID: fixture.userID,
		OwnerUserID: fixture.userID, Title: "Systems", UpdatedAt: updatedAt, Revision: 3, ManagerCount: 2}}
	page, err := fixture.service.List(context.Background(), fixture.call, ListQuery{AcademicUnitID: fixture.unitID,
		ArchiveFilter: store.ExamArchiveActive, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "Systems" || page.Items[0].ManagerCount != 2 {
		t.Fatalf("page = %#v", page)
	}
	if fixture.persistence.listOptions.Visibility.ActorUserID != fixture.userID || !reflect.DeepEqual(fixture.persistence.listOptions.Visibility, visibility) || fixture.persistence.listOptions.AcademicUnitID != fixture.unitID {
		t.Fatalf("list options = %#v", fixture.persistence.listOptions)
	}
	if want := []string{"authorize.list", "store.list"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestArchiveOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	result, err := fixture.service.Archive(context.Background(), fixture.call, ArchiveCommand{ExamID: fixture.examID,
		ExpectedExamRevision: 1, Idempotency: &store.CommandIdempotency{Operation: "exam.archive.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsArchived() || result.Revision != 2 || fixture.persistence.archive == nil || fixture.effects.archivedRevision != 2 {
		t.Fatalf("archive = %#v store=%#v effect=%d", result, fixture.persistence.archive, fixture.effects.archivedRevision)
	}
	if fixture.authorizer.action != model.ActionExamManage || len(fixture.auditor.value) != 3 || fixture.auditor.value["exam_id"] != fixture.examID.String() || fixture.auditor.value["expected_exam_revision"] != int64(1) || fixture.auditor.value["exam_revision"] != int64(2) {
		t.Fatalf("authorization/audit = %s / %#v", fixture.authorizer.action, fixture.auditor.value)
	}
	want := []string{"store.access", "membership", "authorize", "audit.begin", "store.archive", "effect.archived"}
	if !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestArchiveReplayAfterCurrentArchiveDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.archived = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	if _, err := fixture.service.Archive(context.Background(), fixture.call, ArchiveCommand{ExamID: fixture.examID,
		ExpectedExamRevision: 1, Idempotency: &store.CommandIdempotency{Operation: "exam.archive.v1"}}); err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.archive == nil || fixture.effects.archivedRevision != 0 {
		t.Fatalf("replay store/effect = %#v/%d", fixture.persistence.archive, fixture.effects.archivedRevision)
	}
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
			_, err := fixture.service.Archive(context.Background(), fixture.call, ArchiveCommand{ExamID: fixture.examID,
				ExpectedExamRevision: test.revision, Idempotency: &store.CommandIdempotency{Operation: "exam.archive.v1"}})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.want || fixture.auditor.failedCode != test.want {
				t.Fatalf("archive error/audit = %v/%s, want %s", err, fixture.auditor.failedCode, test.want)
			}
		})
	}
}
