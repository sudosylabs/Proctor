//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

func TestLocalCacheLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	cache, err := localcachelayer.NewMemoryCache(128)
	if err != nil {
		t.Fatal(err)
	}
	cachedStore, err := localcachelayer.New(
		sqlStore,
		cache,
		localcachelayer.DefaultPolicy(),
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	if err != nil {
		t.Fatal(err)
	}
	runLayerConformance(t, sqlStore, cachedStore)
}

func TestRetryLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	retriedStore, err := retrylayer.New(sqlStore, retrylayer.DefaultPolicy(IsTransientError))
	if err != nil {
		t.Fatal(err)
	}
	runLayerConformance(t, sqlStore, retriedStore)
}

func TestTimerLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	timedStore, err := timerlayer.New(sqlStore, timerlayer.NopRecorder{})
	if err != nil {
		t.Fatal(err)
	}

	runLayerConformance(t, sqlStore, timedStore)
}

func runLayerConformance(t *testing.T, sqlStore *SQLStore, decorated store.Store) {
	t.Helper()
	tests := []struct {
		name string
		run  func(*testing.T, store.Store)
	}{
		{"Institution", storetest.TestInstitutionStore},
		{"AcademicUnit", storetest.TestAcademicUnitStore},
		{"Programme", storetest.TestProgrammeStore},
		{"ProgrammeLevel", storetest.TestProgrammeLevelStore},
		{"AcademicPeriod", storetest.TestAcademicPeriodStore},
		{"ExamAuthoring", storetest.TestExamAuthoringStore},
		{"ExamRevision", storetest.TestExamRevisionStore},
		{"ExamResource", storetest.TestExamResourceStore},
		{"ExamStarterWorkspace", storetest.TestExamStarterWorkspaceStore},
		{"Class", storetest.TestClassStore},
		{"User", storetest.TestUserStore},
		{"File", storetest.TestFileStore},
		{"Job", storetest.TestJobStore},
		{"ExternalIdentity", storetest.TestExternalIdentityStore},
		{"ExternalLoginState", storetest.TestExternalLoginStateStore},
		{"PasswordCredential", storetest.TestPasswordCredentialStore},
		{"UserToken", storetest.TestUserTokenStore},
		{"PersonalAccessToken", storetest.TestPersonalAccessTokenStore},
		{"MFA", storetest.TestMFAStore},
		{"Session", storetest.TestSessionStores},
		{"ClusterDiscovery", storetest.TestClusterDiscoveryStore},
		{"Affiliation", storetest.TestAffiliationStore},
		{"AcademicUnitMember", storetest.TestAcademicUnitMemberStore},
		{"ClassMember", storetest.TestClassMemberStore},
		{"Role", storetest.TestRoleStore},
		{"RoleBinding", storetest.TestRoleBindingStore},
		{"Audit", storetest.TestAuditStore},
		{"Installation", storetest.TestInstallationStore},
		{"CommandOutcome", storetest.TestCommandOutcomeStore},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetTestStore(t, sqlStore)
			test.run(t, decorated)
		})
	}
}

func TestInstitutionStore(t *testing.T) {
	StoreTest(t, storetest.TestInstitutionStore)
}

func TestAcademicUnitStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicUnitStore)
}

func TestProgrammeStore(t *testing.T) {
	StoreTest(t, storetest.TestProgrammeStore)
}

func TestProgrammeLevelStore(t *testing.T) {
	StoreTest(t, storetest.TestProgrammeLevelStore)
}

func TestAcademicPeriodStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicPeriodStore)
}

func TestExamAuthoringStore(t *testing.T) {
	StoreTest(t, storetest.TestExamAuthoringStore)
	t.Run("bounded catalog plan", func(t *testing.T) {
		persistence := openTestStore(t)
		resetTestStore(t, persistence)
		testExamCatalogBoundedPlan(t, persistence)
	})
}

func TestExamRevisionStore(t *testing.T) {
	StoreTest(t, storetest.TestExamRevisionStore)
}

func TestExamRevisionPublishedBytesSurviveDraftReplacementAndCleanup(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	filesystem := memoryvfs.New()
	content, err := filecontent.New(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	resourceBytes := []byte("old resource bytes")
	workspaceBytes := []byte("old workspace")
	storetest.TestExamRevisionStoreWithRetention(t, persistence, storetest.ExamRevisionRetentionProbe{StageOriginal: func(t *testing.T, ctx context.Context, _ store.Store, fixture storetest.ExamRevisionRetentionFixture) {
		resourceSize := int64(len(resourceBytes))
		resourceKey := examRevisionResourceObjectKey(fixture.OldResourceRevisionID, fixture.OldResourceRenditionID)
		if _, writeErr := filesystem.Write(ctx, resourceKey, bytes.NewReader(resourceBytes), vfspkg.WriteOptions{Size: &resourceSize, NoOverwrite: true}); writeErr != nil {
			t.Fatal(writeErr)
		}
		if _, stageErr := content.StageStarterWorkspaceObject(ctx, fixture.OldWorkspaceObjectID, bytes.NewReader(workspaceBytes), int64(len(workspaceBytes)), "text/plain"); stageErr != nil {
			t.Fatal(stageErr)
		}
	}, Verify: func(t *testing.T, ctx context.Context, ss store.Store, fixture storetest.ExamRevisionRetentionFixture) {
		neighborAt := model.NowUTC().Add(-48 * time.Hour)
		neighbor, newErr := model.NewStagedStarterWorkspaceObject(model.NewStarterWorkspaceObjectID(), fixture.ExamID, fixture.ActorUserID, neighborAt, neighborAt.Add(model.StarterWorkspaceUploadLease))
		if newErr != nil {
			t.Fatal(newErr)
		}
		if _, reserveErr := ss.ExamStarterWorkspace().ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: neighbor}); reserveErr != nil {
			t.Fatal(reserveErr)
		}
		neighborBytes := []byte("unpinned")
		if _, stageErr := content.StageStarterWorkspaceObject(ctx, neighbor.ID, bytes.NewReader(neighborBytes), int64(len(neighborBytes)), "text/plain"); stageErr != nil {
			t.Fatal(stageErr)
		}
		if _, advanceErr := persistence.GetMaster().Exec(ctx, `UPDATE exam_starter_workspace_objects SET reclaim_after=CURRENT_TIMESTAMP-INTERVAL '1 hour' WHERE id=?`, fixture.OldWorkspaceObjectID.String()); advanceErr != nil {
			t.Fatal(advanceErr)
		}

		claimed, claimErr := ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 100, "revision-vfs-retention")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		foundNeighbor := false
		for _, object := range claimed {
			if object.ID == fixture.OldWorkspaceObjectID {
				t.Fatalf("cleanup claimed published object %s", object.ID)
			}
			if object.ID != neighbor.ID {
				continue
			}
			foundNeighbor = true
			if removeErr := content.RemoveStarterWorkspaceObject(ctx, object.ID); removeErr != nil {
				t.Fatal(removeErr)
			}
			if completeErr := ss.ExamStarterWorkspace().CompleteObjectCleanup(ctx, object.ID, "revision-vfs-retention"); completeErr != nil {
				t.Fatal(completeErr)
			}
		}
		if !foundNeighbor {
			t.Fatal("cleanup did not claim unpinned neighbor")
		}
		if opened, openErr := content.OpenStarterWorkspaceObject(ctx, neighbor.ID); openErr == nil {
			_ = opened.Close()
			t.Fatal("cleaned neighbor bytes remain")
		}
		assertOpenedBytes(t, func() (io.ReadCloser, error) {
			return content.OpenExamResource(ctx, fixture.OldResourceRevisionID, fixture.OldResourceRenditionID)
		}, resourceBytes)
		assertOpenedBytes(t, func() (io.ReadCloser, error) {
			return content.OpenStarterWorkspaceObject(ctx, fixture.OldWorkspaceObjectID)
		}, workspaceBytes)
		assertExamRevisionDatabaseImmutability(t, ctx, persistence, fixture)
	}})
}

func assertExamRevisionDatabaseImmutability(t *testing.T, ctx context.Context, persistence *SQLStore, fixture storetest.ExamRevisionRetentionFixture) {
	t.Helper()
	resourceID, entryID, revisionID, renditionID := createExamRevisionConstraintResource(t, ctx, persistence, fixture.ExamID)
	workspaceEntryID := model.NewStarterWorkspaceEntryID()
	at := model.NowUTC()
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO exam_starter_workspace_entries (id,exam_id,kind,path,current_object_id,created_at,updated_at,archived_at) VALUES (?,?, 'directory','appendable',NULL,?,?,NULL)`, workspaceEntryID.String(), fixture.ExamID.String(), at, at); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"header update", `UPDATE exam_revisions SET title='changed' WHERE id=?`, []any{fixture.RevisionID.String()}},
		{"resource update", `UPDATE exam_revision_resources SET display_name='changed' WHERE exam_revision_id=?`, []any{fixture.RevisionID.String()}},
		{"resource delete", `DELETE FROM exam_revision_resources WHERE exam_revision_id=?`, []any{fixture.RevisionID.String()}},
		{"workspace update", `UPDATE exam_revision_starter_workspace_entries SET path='changed' WHERE exam_revision_id=?`, []any{fixture.RevisionID.String()}},
		{"workspace delete", `DELETE FROM exam_revision_starter_workspace_entries WHERE exam_revision_id=?`, []any{fixture.RevisionID.String()}},
		{"resource append", `INSERT INTO exam_revision_resources (exam_revision_id,exam_id,resource_id,file_entry_id,file_revision_id,rendition_id,display_name,description_markdown,position,media_type,size_bytes,sha256) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, []any{
			fixture.RevisionID.String(), fixture.ExamID.String(), resourceID.String(), entryID.String(), revisionID.String(), renditionID.String(), "Appended", "", 1, "text/plain", 1, strings.Repeat("b", 64),
		}},
		{"workspace append", `INSERT INTO exam_revision_starter_workspace_entries (exam_revision_id,exam_id,entry_id,kind,path) VALUES (?,?,?,?,?)`, []any{
			fixture.RevisionID.String(), fixture.ExamID.String(), workspaceEntryID.String(), "directory", "appendable",
		}},
	}
	for _, test := range queries {
		_, err := persistence.GetMaster().Exec(ctx, test.query, test.args...)
		assertPostgresCode(t, test.name, err, "55000")
	}
	if _, err := persistence.ExamRevision().GetSnapshot(ctx, fixture.ExamID, fixture.RevisionID); err != nil {
		t.Fatalf("read immutable Revision after rejected mutations: %v", err)
	}
	assertExamRevisionSealCompleteness(t, ctx, persistence, fixture)
	assertExamRevisionResourceOwnershipConstraint(t, ctx, persistence, fixture, resourceID)
}

func createExamRevisionConstraintResource(t *testing.T, ctx context.Context, persistence *SQLStore, examID model.ExamID) (model.ExamResourceID, model.FileEntryID, model.FileRevisionID, model.FileRenditionID) {
	t.Helper()
	entryID, revisionID, renditionID, resourceID := model.NewFileEntryID(), model.NewFileRevisionID(), model.NewFileRenditionID(), model.NewExamResourceID()
	at := model.NowUTC()
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO file_entries (id,created_at,updated_at,revision,indexing_policy,purpose,purge_claimed) VALUES (?,?,?,1,'none','exam_resource',FALSE)`, entryID.String(), at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO file_revisions (id,file_entry_id,created_at,availability,indexing_state) VALUES (?,?,?,'available','not_required')`, revisionID.String(), entryID.String(), at); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO file_renditions (id,file_revision_id,created_at,name,media_type,size_bytes,width,height,sha256) VALUES (?,?,?,'original','text/plain',1,0,0,?)`, renditionID.String(), revisionID.String(), at, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO exam_resources (id,exam_id,file_entry_id,selected_file_revision_id,display_name,description_markdown,position,created_at,updated_at) VALUES (?,?,?,?,'Other','',1,?,?)`, resourceID.String(), examID.String(), entryID.String(), revisionID.String(), at, at); err != nil {
		t.Fatal(err)
	}
	return resourceID, entryID, revisionID, renditionID
}

func assertExamRevisionSealCompleteness(t *testing.T, ctx context.Context, persistence *SQLStore, fixture storetest.ExamRevisionRetentionFixture) {
	t.Helper()
	for _, test := range []struct {
		name   string
		number int
		sealed bool
		count  int
	}{
		{name: "unsealed snapshot", number: 2000, sealed: false, count: 0},
		{name: "incomplete sealed snapshot", number: 3000, sealed: true, count: 1},
	} {
		tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		revisionID := model.NewExamRevisionID()
		_, err = tx.ExecContext(ctx, `INSERT INTO exam_revisions (id,exam_id,number,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,base_revision_id,publication_kind,sealed)
			SELECT $1,exam_id,number+$2,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,starter_workspace_digest,content_digest,$3,0,0,published_by_user_id,published_at,NULL,publication_kind,$4 FROM exam_revisions WHERE id=$5`, revisionID.String(), test.number, test.count, test.sealed, fixture.RevisionID.String())
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		err = tx.Commit()
		assertPostgresCode(t, test.name, err, "23514")
		var exists bool
		if queryErr := persistence.GetMaster().Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM exam_revisions WHERE id=?)`, revisionID.String()); queryErr != nil || exists {
			t.Fatalf("%s persisted after rejected commit: exists=%v err=%v", test.name, exists, queryErr)
		}
	}
}

func assertExamRevisionResourceOwnershipConstraint(t *testing.T, ctx context.Context, persistence *SQLStore, fixture storetest.ExamRevisionRetentionFixture, resourceID model.ExamResourceID) {
	t.Helper()
	tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	unsealedRevisionID := model.NewExamRevisionID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO exam_revisions (id,exam_id,number,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,base_revision_id,publication_kind,sealed)
		SELECT $1,exam_id,number+1000,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,NULL,publication_kind,FALSE FROM exam_revisions WHERE id=$2`, unsealedRevisionID.String(), fixture.RevisionID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO exam_revision_resources (exam_revision_id,exam_id,resource_id,file_entry_id,file_revision_id,rendition_id,display_name,description_markdown,position,media_type,size_bytes,sha256) VALUES ($1,$2,$3,$4,$5,$6,'Mismatched','',0,'text/plain',1,$7)`,
		unsealedRevisionID.String(), fixture.ExamID.String(), resourceID.String(), fixture.OldResourceEntryID.String(), fixture.OldResourceRevisionID.String(), fixture.OldResourceRenditionID.String(), strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("Exam Revision resource accepted a File Entry owned by another Exam Resource")
	}
}

func assertPostgresCode(t *testing.T, name string, err error, want string) {
	t.Helper()
	var postgresErr *pq.Error
	if !errors.As(err, &postgresErr) || string(postgresErr.Code) != want {
		t.Fatalf("%s error=%v, want PostgreSQL code %s", name, err, want)
	}
}

func examRevisionResourceObjectKey(revisionID model.FileRevisionID, renditionID model.FileRenditionID) string {
	id := revisionID.String()
	return fmt.Sprintf("files/%s/%s/revisions/%s/renditions/%s.resource", id[:2], id[2:4], id, renditionID)
}

func assertOpenedBytes(t *testing.T, open func() (io.ReadCloser, error), want []byte) {
	t.Helper()
	reader, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("opened bytes=%q want=%q", got, want)
	}
}

func TestExamResourceStore(t *testing.T) {
	StoreTest(t, storetest.TestExamResourceStore)
}

func TestExamStarterWorkspaceStore(t *testing.T) {
	StoreTest(t, storetest.TestExamStarterWorkspaceStore)
}

func TestClassStore(t *testing.T) {
	StoreTest(t, storetest.TestClassStore)
}

func TestUserStore(t *testing.T) {
	StoreTest(t, storetest.TestUserStore)
}

func TestFileStore(t *testing.T) {
	StoreTest(t, storetest.TestFileStore)
}

func TestJobStore(t *testing.T) {
	StoreTest(t, storetest.TestJobStore)
}

func TestExternalIdentityStore(t *testing.T) {
	StoreTest(t, storetest.TestExternalIdentityStore)
}

func TestExternalLoginStateStore(t *testing.T) {
	StoreTest(t, storetest.TestExternalLoginStateStore)
}

func TestPasswordCredentialStore(t *testing.T) {
	StoreTest(t, storetest.TestPasswordCredentialStore)
}

func TestUserTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestUserTokenStore)
}

func TestPersonalAccessTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestPersonalAccessTokenStore)
}

func TestMFAStore(t *testing.T) {
	StoreTest(t, storetest.TestMFAStore)
}

func TestSessionStores(t *testing.T) {
	StoreTest(t, storetest.TestSessionStores)
}

func TestClusterDiscoveryStore(t *testing.T) {
	StoreTest(t, storetest.TestClusterDiscoveryStore)
}

func TestAffiliationStore(t *testing.T) {
	StoreTest(t, storetest.TestAffiliationStore)
}

func TestAcademicUnitMemberStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicUnitMemberStore)
}

func TestClassMemberStore(t *testing.T) {
	StoreTest(t, storetest.TestClassMemberStore)
}

func TestRoleStore(t *testing.T) {
	StoreTest(t, storetest.TestRoleStore)
}

func TestRoleBindingStore(t *testing.T) {
	StoreTest(t, storetest.TestRoleBindingStore)
}

func TestAuditStore(t *testing.T) {
	StoreTest(t, storetest.TestAuditStore)
}

func TestInstallationStore(t *testing.T) {
	StoreTest(t, storetest.TestInstallationStore)
}

func TestCommandOutcomeStore(t *testing.T) {
	StoreTest(t, storetest.TestCommandOutcomeStore)
}
