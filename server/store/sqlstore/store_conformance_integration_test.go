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
	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
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
	runLayerConformance(t, sqlStore, newLocalCacheConformanceStore)
}

func newLocalCacheConformanceStore(t *testing.T, sqlStore *SQLStore) store.Store {
	t.Helper()
	storeCache, err := memorycache.New(cachepkg.BytesCodec(), memorycache.Config{
		MaxEntries: 128,
		MaxBytes:   64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := localcachelayer.NewCacheAdapter(storeCache)
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
	return cachedStore
}

func TestRetryLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	runLayerConformance(t, sqlStore, newRetryConformanceStore)
}

func newRetryConformanceStore(t *testing.T, sqlStore *SQLStore) store.Store {
	t.Helper()
	retriedStore, err := retrylayer.New(sqlStore, retrylayer.DefaultPolicy(IsTransientError))
	if err != nil {
		t.Fatal(err)
	}
	return retriedStore
}

func TestTimerLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	runLayerConformance(t, sqlStore, newTimerConformanceStore)
}

func newTimerConformanceStore(t *testing.T, sqlStore *SQLStore) store.Store {
	t.Helper()
	timedStore, err := timerlayer.New(sqlStore, timerlayer.NopRecorder{})
	if err != nil {
		t.Fatal(err)
	}
	return timedStore
}

// TestAccessAndOnboardingDecoratedLayerConformance keeps the phase gate
// focused on the Stores that own access and onboarding state. The repository's
// broader layer-conformance tests remain the authority for every other model.
func TestAccessAndOnboardingDecoratedLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	type decorator struct {
		name string
		new  func(*testing.T, *SQLStore) store.Store
	}
	decorators := []decorator{
		{"local-cache", newLocalCacheConformanceStore},
		{"retry", newRetryConformanceStore},
		{"timer", newTimerConformanceStore},
	}

	tests := []struct {
		name      string
		pristine  bool
		providers map[string]model.ProviderAdmissionMode
		run       func(*testing.T, store.Store)
	}{
		{"Institution", false, nil, storetest.TestInstitutionStore},
		{"AcademicUnit", false, nil, storetest.TestAcademicUnitStore},
		{"Programme", false, nil, storetest.TestProgrammeStore},
		{"ProgrammeLevel", false, nil, storetest.TestProgrammeLevelStore},
		{"AcademicPeriod", false, nil, storetest.TestAcademicPeriodStore},
		{"Class", false, nil, storetest.TestClassStore},
		{"User", false, nil, func(t *testing.T, decorated store.Store) {
			storetest.TestUserStore(t, decorated, userRegistrationSQLProbe(sqlStore))
		}},
		{"Job", false, nil, storetest.TestJobStore},
		{"Mail", false, nil, storetest.TestMailStore},
		{"ExternalIdentity", true, map[string]model.ProviderAdmissionMode{
			"campus-cas": model.ProviderAdmissionAutoProvision,
		}, storetest.TestExternalIdentityStore},
		{"ExternalLoginState", false, nil, func(t *testing.T, decorated store.Store) {
			storetest.TestExternalLoginStateStore(t, decorated, externalLoginStateSQLProbe(sqlStore))
		}},
		{"DesktopAuthorization", false, nil, func(t *testing.T, decorated store.Store) {
			storetest.TestBrowserAuthenticationStore(t, decorated, browserAuthenticationSQLProbe(sqlStore))
		}},
		{"InvitationAndBatch", false, nil, func(t *testing.T, decorated store.Store) {
			storetest.TestInvitationStore(t, decorated, invitationAuthoritySQLProbe(t, sqlStore))
		}},
		{"PasswordCredential", true, map[string]model.ProviderAdmissionMode{
			"campus-cas": model.ProviderAdmissionLinkedOnly,
		}, storetest.TestPasswordCredentialStore},
		{"UserToken", false, nil, storetest.TestUserTokenStore},
		{"PersonalAccessToken", false, nil, storetest.TestPersonalAccessTokenStore},
		{"MFA", false, nil, storetest.TestMFAStore},
		{"Session", false, nil, storetest.TestSessionStores},
		{"Affiliation", false, nil, storetest.TestAffiliationStore},
		{"AcademicUnitMember", false, nil, storetest.TestAcademicUnitMemberStore},
		{"ClassMember", false, nil, storetest.TestClassMemberStore},
		{"Role", false, nil, storetest.TestRoleStore},
		{"RoleBinding", false, nil, storetest.TestRoleBindingStore},
		{"Audit", false, nil, storetest.TestAuditStore},
		{"Installation", true, nil, storetest.TestInstallationStore},
		{"AccessPolicy", true, nil, func(t *testing.T, decorated store.Store) {
			storetest.TestAccessPolicyStore(t, decorated)
		}},
		{"DesktopCompatibilityPolicy", true, nil, storetest.TestDesktopCompatibilityPolicyStore},
		{"CommandOutcome", false, nil, storetest.TestCommandOutcomeStore},
	}

	for _, decorator := range decorators {
		for _, test := range tests {
			t.Run(decorator.name+"/"+test.name, func(t *testing.T) {
				resetPristineTestStore(t, sqlStore)
				if !test.pristine || test.providers != nil {
					seedTestAuthenticationPolicy(t, sqlStore, test.providers)
				}
				test.run(t, decorator.new(t, sqlStore))
			})
		}
	}
}

func runLayerConformance(
	t *testing.T,
	sqlStore *SQLStore,
	decorate func(*testing.T, *SQLStore) store.Store,
) {
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
		{"ExamSitting", storetest.TestExamSittingStore},
		{"ExamAttempt", func(t *testing.T, decorated store.Store) {
			storetest.TestExamAttemptStore(t, decorated)
		}},
		{"BrowserActivity", storetest.TestBrowserActivityStore},
		{"ExecutionGrant", storetest.TestExecutionGrantStore},
		{"ExamAttemptWorkspace", func(t *testing.T, decorated store.Store) {
			storetest.TestExamAttemptWorkspaceStore(t, decorated, decorated.ExamAttemptWorkspace(),
				examAttemptWorkspaceSQLProbe(t, sqlStore))
		}},
		{"ExamSubmission", func(t *testing.T, decorated store.Store) {
			probe := examSubmissionSQLProbe(t, sqlStore)
			probe.ConcurrentPeer = NewSQLExamSubmissionStore(sqlStore)
			storetest.TestExamSubmissionStore(t, decorated, decorated.ExamSubmission(), probe)
		}},
		{"ExamIntegrityReview", func(t *testing.T, decorated store.Store) {
			storetest.TestExamIntegrityReviewStore(t, decorated, decorated.ExamIntegrityReview(),
				storetest.ExamIntegrityReviewSQLProbe{ConcurrentPeer: NewSQLExamIntegrityReviewStore(sqlStore)})
		}},
		{"ExamResource", storetest.TestExamResourceStore},
		{"ExamCorrection", func(t *testing.T, decorated store.Store) {
			storetest.TestExamCorrectionStore(t, decorated, examCorrectionSQLProbe(t, sqlStore))
		}},
		{"ExamStarterWorkspace", storetest.TestExamStarterWorkspaceStore},
		{"Class", storetest.TestClassStore},
		{"User", func(t *testing.T, decorated store.Store) {
			storetest.TestUserStore(t, decorated, userRegistrationSQLProbe(sqlStore))
		}},
		{"UserSettings", storetest.TestUserSettingsStore},
		{"File", storetest.TestFileStore},
		{"Job", storetest.TestJobStore},
		{"Mail", storetest.TestMailStore},
		{"ExternalIdentity", storetest.TestExternalIdentityStore},
		{"ExternalLoginState", func(t *testing.T, decorated store.Store) {
			storetest.TestExternalLoginStateStore(t, decorated, externalLoginStateSQLProbe(sqlStore))
		}},
		{"DesktopAuthorization", func(t *testing.T, decorated store.Store) {
			storetest.TestBrowserAuthenticationStore(t, decorated, browserAuthenticationSQLProbe(sqlStore))
		}},
		{"PasswordCredential", storetest.TestPasswordCredentialStore},
		{"UserToken", storetest.TestUserTokenStore},
		{"Invitation", func(t *testing.T, decorated store.Store) {
			storetest.TestInvitationStore(t, decorated)
		}},
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
		{"AccessPolicy", func(t *testing.T, decorated store.Store) {
			storetest.TestAccessPolicyStore(t, decorated)
		}},
		{"DesktopCompatibilityPolicy", storetest.TestDesktopCompatibilityPolicyStore},
		{"CommandOutcome", storetest.TestCommandOutcomeStore},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch test.name {
			case "Installation", "AccessPolicy", "DesktopCompatibilityPolicy":
				resetPristineTestStore(t, sqlStore)
			case "PasswordCredential":
				resetPristineTestStore(t, sqlStore)
				seedTestAuthenticationPolicy(t, sqlStore, map[string]model.ProviderAdmissionMode{
					"campus-cas": model.ProviderAdmissionLinkedOnly,
				})
			case "ExternalIdentity":
				resetPristineTestStore(t, sqlStore)
				seedTestAuthenticationPolicy(t, sqlStore, map[string]model.ProviderAdmissionMode{
					"campus-cas": model.ProviderAdmissionAutoProvision,
				})
			default:
				resetTestStore(t, sqlStore)
			}
			test.run(t, decorate(t, sqlStore))
		})
	}
}

func TestBrowserAuthenticationStore(t *testing.T) {
	sqlStore := openTestStore(t)
	resetTestStore(t, sqlStore)
	peer := openTestStore(t)
	storetest.TestBrowserAuthenticationStore(t, sqlStore, browserAuthenticationSQLProbe(sqlStore), peer.BrowserAuthentication())
}

func browserAuthenticationSQLProbe(sqlStore *SQLStore) storetest.BrowserAuthenticationSQLProbe {
	return storetest.BrowserAuthenticationSQLProbe{Backdate: func(t *testing.T, id model.BrowserAuthenticationTransactionID, createdAt, expiresAt time.Time) {
		t.Helper()
		if _, err := sqlStore.GetMaster().Exec(context.Background(), `UPDATE browser_authentication_transactions
			SET created_at=?,updated_at=?,expires_at=? WHERE id=?`, createdAt, createdAt, expiresAt, id.String()); err != nil {
			t.Fatal(err)
		}
	}}
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

func TestMailStore(t *testing.T) {
	StoreTest(t, storetest.TestMailStore)
}

func TestMailMaintenanceRetentionAndSharedRateLimit(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestMailStore(t, persistence)
	ctx := context.Background()

	var suppressedID, failedID string
	if err := persistence.GetMaster().Get(ctx, &suppressedID, `SELECT id FROM mail_deliveries WHERE state='suppressed' ORDER BY id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if err := persistence.GetMaster().Get(ctx, &failedID, `SELECT id FROM mail_deliveries WHERE state='accepted' ORDER BY id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE mail_deliveries SET
		created_at=statement_timestamp()-INTERVAL '101 days',message_date=statement_timestamp()-INTERVAL '101 days',
		deadline=statement_timestamp()-INTERVAL '100 days',updated_at=statement_timestamp()-INTERVAL '100 days'
		WHERE id=?`, suppressedID); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE mail_deliveries SET state='failed',accepted_at=NULL,
		created_at=statement_timestamp()-INTERVAL '182 days',message_date=statement_timestamp()-INTERVAL '182 days',
		deadline=statement_timestamp()-INTERVAL '181 days',updated_at=statement_timestamp()-INTERVAL '181 days',
		failed_at=statement_timestamp()-INTERVAL '181 days',public_failure_code='mail.transport.permanent',
		payload_key_id='11111111111111111111111111111111',
		encrypted_payload='{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"retained"}'::jsonb WHERE id=?`, failedID); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO mail_payload_keys(key_id,active_references)
		VALUES('11111111111111111111111111111111',1)
		ON CONFLICT(key_id) DO UPDATE SET active_references=mail_payload_keys.active_references+1`); err != nil {
		t.Fatal(err)
	}
	var auditsBefore int64
	if err := persistence.GetMaster().Get(ctx, &auditsBefore, `SELECT COUNT(*) FROM audit_events`); err != nil {
		t.Fatal(err)
	}
	first, err := persistence.Mail().CleanupTerminal(ctx, 1)
	if err != nil || first.Affected != 1 || !first.More {
		t.Fatalf("first cleanup = %#v, %v", first, err)
	}
	second, err := persistence.Mail().CleanupTerminal(ctx, 1)
	if err != nil || second.Affected != 1 {
		t.Fatalf("second cleanup = %#v, %v", second, err)
	}
	for _, id := range []string{suppressedID, failedID} {
		if _, err = persistence.Mail().GetDelivery(ctx, model.MailDeliveryID(id)); !store.IsNotFound(err) {
			t.Fatalf("retained delivery %s error = %v", id, err)
		}
	}
	var auditsAfter int64
	if err = persistence.GetMaster().Get(ctx, &auditsAfter, `SELECT COUNT(*) FROM audit_events`); err != nil || auditsAfter != auditsBefore {
		t.Fatalf("audit retention before=%d after=%d err=%v", auditsBefore, auditsAfter, err)
	}

	if _, err = persistence.GetMaster().Exec(ctx, `DELETE FROM mail_send_rate_limit`); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `INSERT INTO mail_send_rate_limit(singleton,tokens,updated_at) VALUES(TRUE,5,statement_timestamp()+INTERVAL '1 hour')`); err != nil {
		t.Fatal(err)
	}
	ordinary, err := persistence.Mail().AcquireSendPermit(ctx, store.MailSendOrdinary)
	if err != nil || !ordinary.Allowed {
		t.Fatalf("ordinary reserved permit = %#v, %v", ordinary, err)
	}
	ordinary, err = persistence.Mail().AcquireSendPermit(ctx, store.MailSendOrdinary)
	if err != nil || ordinary.Allowed || ordinary.RetryAfter <= 0 {
		t.Fatalf("ordinary reserve fence = %#v, %v", ordinary, err)
	}
	credential, err := persistence.Mail().AcquireSendPermit(ctx, store.MailSendCredential)
	if err != nil || !credential.Allowed {
		t.Fatalf("credential reserved permit = %#v, %v", credential, err)
	}
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

func TestExamAttemptStore(t *testing.T) {
	persistence := openTestStore(t)
	peerPersistence := openTestStore(t)
	resetTestStore(t, persistence)
	probe := examAttemptSQLProbe(t, persistence)
	probe.ConcurrentExamAttempt = peerPersistence.ExamAttempt()
	storetest.TestExamAttemptStore(t, persistence, probe)
}

func TestBrowserActivityStore(t *testing.T) {
	StoreTest(t, storetest.TestBrowserActivityStore)
}

func TestExecutionGrantStore(t *testing.T) {
	StoreTest(t, storetest.TestExecutionGrantStore)
}

func TestExamAttemptWorkspaceStore(t *testing.T) {
	persistence := openTestStore(t)
	peerPersistence := openTestStore(t)
	resetTestStore(t, persistence)
	probe := examAttemptWorkspaceSQLProbe(t, persistence)
	probe.ConcurrentPeer = NewSQLExamAttemptWorkspaceStore(peerPersistence)
	storetest.TestExamAttemptWorkspaceStore(t, persistence, NewSQLExamAttemptWorkspaceStore(persistence),
		probe)
}

func TestExamSubmissionStore(t *testing.T) {
	persistence := openTestStore(t)
	peerPersistence := openTestStore(t)
	resetTestStore(t, persistence)
	probe := examSubmissionSQLProbe(t, persistence)
	probe.ConcurrentPeer = NewSQLExamSubmissionStore(peerPersistence)
	storetest.TestExamSubmissionStore(t, persistence, NewSQLExamSubmissionStore(persistence), probe)
}

func TestExamIntegrityReviewStore(t *testing.T) {
	persistence := openTestStore(t)
	peerPersistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamIntegrityReviewStore(t, persistence, NewSQLExamIntegrityReviewStore(persistence),
		storetest.ExamIntegrityReviewSQLProbe{ConcurrentPeer: NewSQLExamIntegrityReviewStore(peerPersistence)})
}

func examSubmissionSQLProbe(t *testing.T, persistence *SQLStore) storetest.ExamSubmissionSQLProbe {
	t.Helper()
	return storetest.ExamSubmissionSQLProbe{
		IntegrityPersistence: func(t *testing.T, ctx context.Context, attemptID model.ExamAttemptID, generation int64) storetest.SubmissionIntegrityPersistence {
			t.Helper()
			var row struct {
				PendingQualifiers int64 `db:"pending_qualifiers"`
				UnresolvedMissing int64 `db:"unresolved_missing"`
			}
			if err := persistence.GetMaster().Get(ctx, &row, `SELECT
				(SELECT count(*) FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=?) AS pending_qualifiers,
				COALESCE((SELECT unresolved_missing_count FROM exam_attempt_focus_loss_evaluations
				 WHERE exam_attempt_id=? AND generation=?),0) AS unresolved_missing`,
				attemptID.String(), generation, attemptID.String(), generation); err != nil {
				t.Fatal(err)
			}
			return storetest.SubmissionIntegrityPersistence{PendingQualifiers: row.PendingQualifiers, UnresolvedMissing: row.UnresolvedMissing}
		},
		AssertSchema: func(t *testing.T, ctx context.Context) {
			t.Helper()
			var constraints []string
			if err := persistence.GetMaster().Select(ctx, &constraints, `SELECT conname FROM pg_constraint WHERE conname IN (
				'exam_attempt_workspaces_submission_owner_key',
				'exam_attempt_workspace_entries_submission_owner_key','exam_submissions_workspace_fkey',
				'exam_submissions_participation_fkey','exam_submissions_connection_fkey',
				'exam_submission_manifest_entries_submission_fkey',
				'exam_submission_manifest_entries_workspace_entry_fkey',
				'exam_submission_manifest_entries_workspace_object_fkey',
				'exam_submission_manifest_entries_starter_object_fkey',
				'exam_submission_manifest_entries_attempt_object_fkey',
				'exam_submissions_id_canonical_check','exam_submissions_exam_attempt_id_canonical_check',
				'exam_submissions_workspace_id_canonical_check','exam_submissions_participation_id_canonical_check',
				'exam_submissions_connection_id_canonical_check',
				'exam_submission_manifest_entries_submission_id_canonical_check',
				'exam_submission_manifest_entries_workspace_id_canonical_check',
				'exam_submission_manifest_entries_entry_id_canonical_check',
				'exam_submission_manifest_entries_content_version_canonical_check',
				'exam_submission_manifest_entries_starter_object_id_canonical_check',
				'exam_submission_manifest_entries_attempt_object_id_canonical_check',
				'exam_submission_manifest_entries_workspace_object_id_canonical_check'
			) ORDER BY conname`); err != nil {
				t.Fatal(err)
			}
			if len(constraints) != 22 {
				t.Fatalf("Submission named schema constraints = %#v", constraints)
			}
			for name, fragment := range map[string]string{
				"exam_submissions_participation_fkey":                   "(participation_id, exam_attempt_id, generation)",
				"exam_submissions_connection_fkey":                      "(connection_id, exam_attempt_id, participation_id)",
				"exam_submissions_workspace_fkey":                       "(exam_attempt_id, workspace_id)",
				"exam_submission_manifest_entries_workspace_entry_fkey": "(workspace_id, entry_id, workspace_object_id)",
			} {
				var definition string
				if err := persistence.GetMaster().Get(ctx, &definition,
					`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=?`, name); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(definition, fragment) {
					t.Fatalf("%s definition = %q", name, definition)
				}
			}
			var triggers []string
			if err := persistence.GetMaster().Select(ctx, &triggers, `SELECT tgname FROM pg_trigger
				WHERE NOT tgisinternal AND tgname IN ('exam_submissions_immutable',
				'exam_submission_manifest_entries_immutable','exam_submission_manifest_entries_insert_guard',
				'exam_submission_manifest_entries_object_provenance','exam_submissions_sealed_check') ORDER BY tgname`); err != nil {
				t.Fatal(err)
			}
			if len(triggers) != 5 {
				t.Fatalf("Submission immutable/seal triggers = %#v", triggers)
			}
			var auditResourceConstraint string
			if err := persistence.GetMaster().Get(ctx, &auditResourceConstraint,
				`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='audit_events_resource_type_check'`); err != nil {
				t.Fatal(err)
			}
			for _, resourceType := range []string{"'programme'", "'programme_level'", "'submission'"} {
				if !strings.Contains(auditResourceConstraint, resourceType) {
					t.Fatalf("audit resource constraint missing %s: %q", resourceType, auditResourceConstraint)
				}
			}
			var indexes []string
			if err := persistence.GetMaster().Select(ctx, &indexes, `SELECT indexname FROM pg_indexes
				WHERE schemaname=current_schema() AND indexname IN (
				'class_members_class_user_history_idx','exam_attempts_sitting_unfinished_id_idx') ORDER BY indexname`); err != nil {
				t.Fatal(err)
			}
			if len(indexes) != 2 {
				t.Fatalf("automatic sealing/no-show indexes = %#v", indexes)
			}
		},
		AssertRetentionFence: func(t *testing.T, ctx context.Context, submissionID model.SubmissionID) {
			t.Helper()
			var selectors struct {
				StarterID        string `db:"starter_object_id"`
				AttemptID        string `db:"attempt_object_id"`
				Workspace        string `db:"workspace_id"`
				EntryID          string `db:"entry_id"`
				DirectoryEntryID string `db:"directory_entry_id"`
			}
			if err := persistence.GetMaster().Get(ctx, &selectors, `SELECT
				(SELECT starter_object_id FROM exam_submission_manifest_entries
				 WHERE submission_id=? AND starter_object_id IS NOT NULL ORDER BY entry_id LIMIT 1) AS starter_object_id,
				(SELECT attempt_object_id FROM exam_submission_manifest_entries
				 WHERE submission_id=? AND attempt_object_id IS NOT NULL ORDER BY entry_id LIMIT 1) AS attempt_object_id,
				(SELECT workspace_id FROM exam_submission_manifest_entries
				 WHERE submission_id=? AND attempt_object_id IS NOT NULL ORDER BY entry_id LIMIT 1) AS workspace_id,
				(SELECT entry_id FROM exam_submission_manifest_entries
				 WHERE submission_id=? AND attempt_object_id IS NOT NULL ORDER BY entry_id LIMIT 1) AS entry_id,
				(SELECT entry_id FROM exam_submission_manifest_entries
				 WHERE submission_id=? AND kind='directory' ORDER BY entry_id LIMIT 1) AS directory_entry_id`,
				submissionID.String(), submissionID.String(), submissionID.String(), submissionID.String(), submissionID.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := persistence.GetMaster().Exec(ctx, `DELETE FROM exam_starter_workspace_objects WHERE id=?`, selectors.StarterID); err == nil {
				t.Fatal("starter object retention fence allowed deletion")
			}
			if _, err := persistence.GetMaster().Exec(ctx, `DELETE FROM exam_attempt_workspace_objects WHERE id=?`, selectors.AttemptID); err == nil {
				t.Fatal("attempt object retention fence allowed deletion")
			}
			if _, err := persistence.GetMaster().Exec(ctx, `DELETE FROM exam_attempt_workspace_entries WHERE workspace_id=? AND id=?`, selectors.Workspace, selectors.EntryID); err == nil {
				t.Fatal("Submission-owned Workspace Entry retention fence allowed deletion")
			}
			if _, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_submission_manifest_entries SET path='changed' WHERE submission_id=?`, submissionID.String()); err == nil {
				t.Fatal("immutable Submission manifest accepted mutation")
			}
			assertExamSubmissionManifestInsertFence(t, ctx, persistence,
				`INSERT INTO exam_submission_manifest_entries (submission_id,workspace_id,entry_id,kind,path)
				 VALUES (?,?,?,'directory','missing')`, submissionID.String(), selectors.Workspace,
				model.NewAttemptWorkspaceEntryID().String())
			assertExamSubmissionManifestInsertFence(t, ctx, persistence,
				`INSERT INTO exam_submission_manifest_entries
				 (submission_id,workspace_id,entry_id,kind,path,content_version,media_type,size_bytes,sha256,storage_origin,
				  starter_object_id,attempt_object_id,workspace_object_id)
				 SELECT submission_id,workspace_id,entry_id,kind,'wrong/'||path,content_version,media_type,size_bytes,sha256,
				  storage_origin,starter_object_id,attempt_object_id,workspace_object_id
				 FROM exam_submission_manifest_entries WHERE submission_id=? AND entry_id=?`,
				submissionID.String(), selectors.DirectoryEntryID)
			assertExamSubmissionManifestInsertFence(t, ctx, persistence,
				`INSERT INTO exam_submission_manifest_entries
				 (submission_id,workspace_id,entry_id,kind,path,content_version,media_type,size_bytes,sha256,storage_origin,
				  starter_object_id,attempt_object_id,workspace_object_id)
				 SELECT submission_id,workspace_id,entry_id,kind,path,content_version,media_type,size_bytes,?,storage_origin,
				  starter_object_id,attempt_object_id,workspace_object_id
				 FROM exam_submission_manifest_entries WHERE submission_id=? AND entry_id=?`,
				strings.Repeat("c", 64), submissionID.String(), selectors.EntryID)

			// Simulate a later record-retention workflow removing the live
			// Workspace Entry and making its object cleanup-eligible. The direct
			// Submission reference must independently fence every cleanup phase
			// before a VFS purge can occur.
			_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "isolate Submission object retention fixture", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_entries DISABLE TRIGGER ALL`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_entries WHERE workspace_id=? AND id=?`, selectors.Workspace, selectors.EntryID); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_entries ENABLE TRIGGER ALL`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects DISABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects SET state='staged',
					updated_at=GREATEST(updated_at,statement_timestamp()),expires_at=statement_timestamp()+INTERVAL '1 hour',
					reclaim_after=NULL,claim_token=NULL,claimed_at=NULL WHERE id=?`, selectors.AttemptID); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects ENABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			workspaceStore := NewSQLExamAttemptWorkspaceStore(persistence)
			attemptObjectID, err := model.ParseAttemptWorkspaceObjectID(selectors.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			if err = workspaceStore.MarkObjectReclaimable(ctx, attemptObjectID); !store.IsConflict(err) {
				t.Fatalf("MarkObjectReclaimable(Submission pinned) error = %v", err)
			}
			_, err = runSQLTransaction(ctx, persistence.GetMaster().Begin, "make Submission object cleanup due", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects DISABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects SET state='reclaimable',
					updated_at=GREATEST(updated_at,statement_timestamp()),expires_at=NULL,
					reclaim_after=statement_timestamp()-INTERVAL '1 microsecond',claim_token=NULL,claimed_at=NULL WHERE id=?`, selectors.AttemptID); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects ENABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := workspaceStore.ClaimObjectsForCleanup(ctx, 200, "submission-retention")
			if err != nil {
				t.Fatal(err)
			}
			for _, object := range claimed {
				if object.ID == attemptObjectID {
					t.Fatalf("ClaimObjectsForCleanup claimed Submission-pinned object %#v", object)
				}
			}
			_, err = runSQLTransaction(ctx, persistence.GetMaster().Begin, "force Submission object cleanup claim", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects DISABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects SET state='claimed',
					updated_at=GREATEST(updated_at,statement_timestamp()),claim_token='submission-retention',
					claimed_at=GREATEST(updated_at,statement_timestamp()) WHERE id=?`, selectors.AttemptID); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects ENABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = workspaceStore.CompleteObjectCleanup(ctx, attemptObjectID, "submission-retention"); !store.IsConflict(err) {
				t.Fatalf("CompleteObjectCleanup(Submission pinned) error = %v", err)
			}
		},
		CorruptManifest: func(t *testing.T, ctx context.Context, submissionID model.SubmissionID) {
			t.Helper()
			if _, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_submissions SET manifest_digest=? WHERE id=?`,
				strings.Repeat("c", 64), submissionID.String()); err == nil {
				t.Fatal("immutable Submission header accepted an update")
			}
			connection, err := persistence.GetMaster().DB().Connx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			transaction, err := connection.BeginTxx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback()
			if _, err = transaction.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
				t.Fatal(err)
			}
			if _, err = transaction.ExecContext(ctx, persistence.GetMaster().DB().Rebind(`UPDATE exam_submissions SET manifest_digest=? WHERE id=?`),
				strings.Repeat("c", 64), submissionID.String()); err != nil {
				t.Fatal(err)
			}
			if err = transaction.Commit(); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func assertExamSubmissionManifestInsertFence(t *testing.T, ctx context.Context, persistence *SQLStore,
	query string, args ...any,
) {
	t.Helper()
	_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "probe Submission manifest state fence", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_submission_manifest_entries
			DISABLE TRIGGER exam_submission_manifest_entries_insert_guard`); execErr != nil {
			return struct{}{}, execErr
		}
		if _, execErr := tx.Exec(ctx, query, args...); execErr != nil {
			return struct{}{}, execErr
		}
		if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_submission_manifest_entries
			ENABLE TRIGGER exam_submission_manifest_entries_insert_guard`); execErr != nil {
			return struct{}{}, execErr
		}
		return struct{}{}, nil
	})
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Constraint != "exam_submission_manifest_entries_object_provenance" {
		t.Fatalf("direct manifest insert state fence error = %v", err)
	}
}

func examAttemptWorkspaceSQLProbe(t *testing.T, persistence *SQLStore) storetest.ExamAttemptWorkspaceSQLProbe {
	t.Helper()
	return storetest.ExamAttemptWorkspaceSQLProbe{
		SetParticipationLeaseExpired: examAttemptSQLProbe(t, persistence).SetParticipationLeaseExpired,
		MakeObjectCleanupDue: func(t *testing.T, ctx context.Context, id model.AttemptWorkspaceObjectID) {
			t.Helper()
			_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_attempt_workspace_objects
			SET updated_at=GREATEST(updated_at,statement_timestamp()),reclaim_after=statement_timestamp()-INTERVAL '1 microsecond'
			WHERE id=? AND state='reclaimable'`, id.String())
			if err != nil {
				t.Fatal(err)
			}
		}, MakeCleanupClaimStale: func(t *testing.T, ctx context.Context, id model.AttemptWorkspaceObjectID) {
			t.Helper()
			_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "age Attempt Workspace cleanup claim fixture", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects DISABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects SET
				created_at=statement_timestamp()-INTERVAL '7 minutes',updated_at=statement_timestamp()-INTERVAL '6 minutes',
				reclaim_after=statement_timestamp()-INTERVAL '7 minutes',claimed_at=statement_timestamp()-INTERVAL '6 minutes'
				WHERE id=? AND state='claimed'`, id.String()); execErr != nil {
					return struct{}{}, execErr
				}
				if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_workspace_objects ENABLE TRIGGER exam_attempt_workspace_objects_immutable`); execErr != nil {
					return struct{}{}, execErr
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}, FillWorkspaceEntryQuota: func(t *testing.T, ctx context.Context, workspaceID model.ExamAttemptWorkspaceID) {
			t.Helper()
			_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "fill Attempt Workspace entry quota fixture", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				var workspace struct {
					RevisionID  string    `db:"admission_revision_id"`
					Count       int       `db:"entry_count"`
					Maximum     int       `db:"maximum_entries"`
					DatabaseNow time.Time `db:"database_now"`
				}
				if getErr := tx.Get(ctx, &workspace, `SELECT w.admission_revision_id,r.exam_workspace_max_entries AS maximum_entries,
				(SELECT count(*) FROM exam_attempt_workspace_entries e WHERE e.workspace_id=w.id) AS entry_count,
				statement_timestamp() AS database_now FROM exam_attempt_workspaces w
				JOIN exam_revisions r ON r.id=w.admission_revision_id WHERE w.id=? FOR UPDATE OF w`, workspaceID.String()); getErr != nil {
					return struct{}{}, getErr
				}
				for index := workspace.Count; index < workspace.Maximum; index++ {
					if _, execErr := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_entries
					(id,workspace_id,admission_revision_id,kind,path,created_at,updated_at)
					VALUES (?,?,?,'directory',?,?,?)`, model.NewAttemptWorkspaceEntryID().String(), workspaceID.String(),
						workspace.RevisionID, fmt.Sprintf("zz-quota-%03d", index), workspace.DatabaseNow, workspace.DatabaseNow); execErr != nil {
						return struct{}{}, execErr
					}
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}, MakeJournalGap: func(t *testing.T, ctx context.Context, workspaceID model.ExamAttemptWorkspaceID) {
			t.Helper()
			_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "create Attempt Workspace journal gap fixture", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
				var entry struct {
					ID   string `db:"id"`
					Kind string `db:"kind"`
				}
				if getErr := tx.Get(ctx, &entry, `SELECT id,kind FROM exam_attempt_workspace_entries WHERE workspace_id=? ORDER BY id LIMIT 1`, workspaceID.String()); getErr != nil {
					return struct{}{}, getErr
				}
				if _, execErr := tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_journal WHERE workspace_id=?`, workspaceID.String()); execErr != nil {
					return struct{}{}, execErr
				}
				var cursor int64
				if getErr := tx.Get(ctx, &cursor, `UPDATE exam_attempt_workspaces SET cursor=cursor+?,updated_at=statement_timestamp()
				WHERE id=? RETURNING cursor`, model.AttemptWorkspaceJournalRetention+1, workspaceID.String()); getErr != nil {
					return struct{}{}, getErr
				}
				if _, execErr := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_journal
				(workspace_id,cursor,entry_id,entry_kind,operation,old_path,new_path,mutation_key_digest,changed_at)
				VALUES (?,?,?,?,'move_entry','gap-old','gap-new',?,statement_timestamp())`, workspaceID.String(), cursor,
					entry.ID, entry.Kind, bytes.Repeat([]byte{1}, 32)); execErr != nil {
					return struct{}{}, execErr
				}
				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}}
}

func examAttemptSQLProbe(t *testing.T, persistence *SQLStore) storetest.ExamAttemptSQLProbe {
	t.Helper()
	return storetest.ExamAttemptSQLProbe{SetParticipationLeaseExpired: func(t *testing.T, ctx context.Context, id model.AttemptParticipationID) {
		t.Helper()
		_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "expire Attempt Participation fixture", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
			if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_participations DISABLE TRIGGER exam_attempt_participations_guard`); execErr != nil {
				return struct{}{}, execErr
			}
			if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_participations
				SET renewal_sequence=GREATEST(renewal_sequence,1),updated_at=GREATEST(updated_at,statement_timestamp()),
					lease_expires_at=started_at+INTERVAL '1 microsecond'
				WHERE id=?`, id.String()); execErr != nil {
				return struct{}{}, execErr
			}
			if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_participations ENABLE TRIGGER exam_attempt_participations_guard`); execErr != nil {
				return struct{}{}, execErr
			}
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, FenceRenewalPastDeadline: func(t *testing.T, ctx context.Context, id model.AttemptParticipationID, contender func() error) error {
		t.Helper()
		var completed chan error
		_, err := runSQLTransaction(ctx, persistence.GetMaster().Begin, "fence renewal past lease deadline", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
			if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_participations DISABLE TRIGGER exam_attempt_participations_guard`); execErr != nil {
				return struct{}{}, execErr
			}
			if _, execErr := tx.Exec(ctx, `UPDATE exam_attempt_participations
				SET renewal_sequence=GREATEST(renewal_sequence,1),updated_at=GREATEST(updated_at,statement_timestamp()),
					lease_expires_at=statement_timestamp()+INTERVAL '100 milliseconds' WHERE id=?`, id.String()); execErr != nil {
				return struct{}{}, execErr
			}
			if _, execErr := tx.Exec(ctx, `ALTER TABLE exam_attempt_participations ENABLE TRIGGER exam_attempt_participations_guard`); execErr != nil {
				return struct{}{}, execErr
			}
			var locked string
			if lockErr := tx.Get(ctx, &locked, `SELECT id FROM exam_attempt_participations WHERE id=? FOR UPDATE`, id.String()); lockErr != nil {
				return struct{}{}, lockErr
			}
			completed = make(chan error, 1)
			go func() { completed <- contender() }()
			time.Sleep(200 * time.Millisecond)
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return <-completed
	}, UnresolvedFocusLossMissing: func(t *testing.T, ctx context.Context, attemptID model.ExamAttemptID, generation int64) int64 {
		t.Helper()
		var count int64
		if err := persistence.GetMaster().Get(ctx, &count, `SELECT unresolved_missing_count
			FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? AND generation=?`, attemptID.String(), generation); err != nil {
			t.Fatal(err)
		}
		return count
	}, AgeFocusLossPending: func(t *testing.T, ctx context.Context, attemptID model.ExamAttemptID, generation, sequence int64, age time.Duration) {
		t.Helper()
		result, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_attempt_focus_loss_pending
			SET received_at=statement_timestamp()-(? * INTERVAL '1 microsecond')
			WHERE exam_attempt_id=? AND generation=? AND sequence=?`, age.Microseconds(), attemptID.String(), generation, sequence)
		if err != nil {
			t.Fatal(err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("age Focus Loss pending affected=%d", affected)
		}
	}, FocusLossPersistence: func(t *testing.T, ctx context.Context, attemptID model.ExamAttemptID, generation int64) storetest.FocusLossPersistenceProbe {
		t.Helper()
		var row struct {
			Flags           int   `db:"flags"`
			Evidence        int   `db:"evidence"`
			Pending         int   `db:"pending"`
			OverflowCount   int64 `db:"overflow_count"`
			DiagnosticCount int64 `db:"diagnostic_count"`
		}
		if err := persistence.GetMaster().Get(ctx, &row, `SELECT
			(SELECT count(*) FROM integrity_flags WHERE exam_attempt_id=? AND generation=? AND policy_kind='focus_loss') AS flags,
			(SELECT count(*) FROM integrity_evidence WHERE exam_attempt_id=? AND generation=? AND policy_kind='focus_loss') AS evidence,
			(SELECT count(*) FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=?) AS pending,
			COALESCE((SELECT overflow_count FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? AND generation=?),0) AS overflow_count,
			COALESCE((SELECT diagnostic_count FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? AND generation=?),0) AS diagnostic_count`,
			attemptID.String(), generation, attemptID.String(), generation, attemptID.String(), generation,
			attemptID.String(), generation, attemptID.String(), generation); err != nil {
			t.Fatal(err)
		}
		return storetest.FocusLossPersistenceProbe{Flags: row.Flags, Evidence: row.Evidence, Pending: row.Pending,
			OverflowCount: row.OverflowCount, DiagnosticCount: row.DiagnosticCount}
	}, AssertFocusLossSchema: func(t *testing.T, ctx context.Context) {
		t.Helper()
		var columns []string
		if err := persistence.GetMaster().Select(ctx, &columns, `SELECT column_name FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='exam_attempt_suspensions'
			AND column_name IN ('suspension_attempt_revision','expiry_attempt_revision') ORDER BY column_name`); err != nil {
			t.Fatal(err)
		}
		if len(columns) != 1 || columns[0] != "suspension_attempt_revision" {
			t.Fatalf("Attempt Suspension revision columns = %#v", columns)
		}
		var sourceColumns int
		if err := persistence.GetMaster().Get(ctx, &sourceColumns, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='integrity_evidence' AND column_name='source'`); err != nil {
			t.Fatal(err)
		}
		if sourceColumns != 1 {
			t.Fatalf("Integrity Evidence source column count = %d", sourceColumns)
		}
		var constraints []string
		if err := persistence.GetMaster().Select(ctx, &constraints, `SELECT conname FROM pg_constraint
			WHERE conname IN (
			'exam_attempt_focus_loss_evaluations_connection_fkey',
			'exam_attempt_focus_loss_evaluations_participation_fkey',
			'exam_attempt_focus_loss_evaluations_flag_fkey',
			'exam_attempt_focus_loss_evaluations_suspension_fkey',
			'exam_attempt_focus_loss_pending_evaluation_fkey',
			'exam_attempt_focus_loss_pending_participation_fkey',
			'exam_attempt_focus_loss_evaluations_exam_attempt_id_canonical_check',
			'exam_attempt_focus_loss_evaluations_participation_id_canonical_check',
			'exam_attempt_focus_loss_evaluations_last_signal_id_canonical_check',
			'exam_attempt_focus_loss_evaluations_last_connection_id_canonical_check',
			'exam_attempt_focus_loss_evaluations_integrity_flag_id_canonical_check',
			'exam_attempt_focus_loss_evaluations_last_suspension_id_canonical_check',
			'exam_attempt_focus_loss_pending_exam_attempt_id_canonical_check',
			'exam_attempt_focus_loss_pending_participation_id_canonical_check',
			'exam_attempt_focus_loss_pending_signal_id_canonical_check',
			'exam_attempt_focus_loss_pending_evidence_id_canonical_check',
			'integrity_evidence_focus_loss_signal_id_canonical_check') ORDER BY conname`); err != nil {
			t.Fatal(err)
		}
		if len(constraints) != 17 {
			t.Fatalf("Focus Loss named schema constraints = %#v", constraints)
		}
		var connectionDefinition, suspensionDefinition string
		if err := persistence.GetMaster().Get(ctx, &connectionDefinition, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conname='exam_attempt_focus_loss_evaluations_connection_fkey'`); err != nil {
			t.Fatal(err)
		}
		if err := persistence.GetMaster().Get(ctx, &suspensionDefinition, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conname='exam_attempt_focus_loss_evaluations_suspension_fkey'`); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(connectionDefinition, "(last_connection_id, exam_attempt_id, participation_id)") ||
			!strings.Contains(suspensionDefinition, "(last_suspension_id, exam_attempt_id, participation_id, generation)") {
			t.Fatalf("Focus Loss exact-owner FKs = %q, %q", connectionDefinition, suspensionDefinition)
		}
		for _, name := range []string{
			"integrity_evidence_participation_fkey",
			"exam_attempt_focus_loss_evaluations_participation_fkey",
			"exam_attempt_focus_loss_pending_participation_fkey",
			"exam_attempt_suspensions_participation_fkey",
		} {
			var definition string
			if err := persistence.GetMaster().Get(ctx, &definition, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=?`, name); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(definition, "(participation_id, exam_attempt_id, generation)") ||
				!strings.Contains(definition, "(id, exam_attempt_id, generation)") {
				t.Fatalf("%s does not enforce exact Participation generation ownership: %q", name, definition)
			}
		}
		var generationOwnerKeys int
		if err := persistence.GetMaster().Get(ctx, &generationOwnerKeys, `SELECT count(*) FROM pg_constraint
			WHERE conrelid='exam_attempt_participations'::regclass AND contype='u'
			AND pg_get_constraintdef(oid)='UNIQUE (id, exam_attempt_id, generation)'`); err != nil {
			t.Fatal(err)
		}
		if generationOwnerKeys != 1 {
			t.Fatalf("Participation exact-generation owner keys = %d", generationOwnerKeys)
		}
	}}
}

func TestExamCorrectionStore(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamCorrectionStore(t, persistence, examCorrectionSQLProbe(t, persistence))
}

func examCorrectionSQLProbe(t *testing.T, persistence *SQLStore) storetest.ExamCorrectionSQLProbe {
	t.Helper()
	filesystem := memoryvfs.New()
	content, err := filecontent.New(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	return storetest.ExamCorrectionSQLProbe{OpenSitting: func(t *testing.T, ctx context.Context, sittingID model.ExamSittingID) {
		t.Helper()
		result, execErr := persistence.GetMaster().Exec(ctx, `UPDATE exam_sittings SET state='open',
			scheduled_start_at=statement_timestamp()-INTERVAL '1 minute',scheduled_end_at=statement_timestamp()+INTERVAL '1 hour',
			opened_at=GREATEST(created_at,statement_timestamp()),updated_at=GREATEST(created_at,statement_timestamp()),revision=revision+1
			WHERE id=? AND state='scheduled'`, sittingID.String())
		if execErr != nil {
			t.Fatal(execErr)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("open correction Sitting affected=%d", affected)
		}
	}, StageBytes: func(t *testing.T, ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, body string) {
		t.Helper()
		if _, stageErr := content.StoreExamResourceRendition(ctx, revisionID, renditionID, model.ExamResourceMediaText,
			strings.NewReader(body), int64(len(body)), model.NowUTC()); stageErr != nil {
			t.Fatal(stageErr)
		}
	}, VerifyBytes: func(t *testing.T, ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, want string) {
		t.Helper()
		opened, openErr := content.OpenExamResource(ctx, revisionID, renditionID)
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer opened.Close()
		got, readErr := io.ReadAll(opened)
		if readErr != nil || string(got) != want {
			t.Fatalf("retained correction bytes=%q error=%v want=%q", got, readErr, want)
		}
	}, FileAvailability: func(t *testing.T, ctx context.Context, revisionID model.FileRevisionID) model.FileAvailability {
		t.Helper()
		var availability string
		if getErr := persistence.GetMaster().Get(ctx, &availability, `SELECT availability FROM file_revisions WHERE id=?`, revisionID.String()); getErr != nil {
			t.Fatal(getErr)
		}
		return model.FileAvailability(availability)
	}, Corrections: func(t *testing.T, ctx context.Context, sittingID model.ExamSittingID) []storetest.ExamCorrectionProvenanceProbe {
		t.Helper()
		var rows []struct {
			PreviousRevisionID   string `db:"previous_revision_id"`
			CorrectionRevisionID string `db:"correction_revision_id"`
			PrivateReason        string `db:"private_reason"`
			SittingRevision      int64  `db:"sitting_revision"`
		}
		if selectErr := persistence.GetMaster().Select(ctx, &rows, `SELECT previous_revision_id,correction_revision_id,private_reason,sitting_revision FROM exam_sitting_live_corrections WHERE exam_sitting_id=? ORDER BY sitting_revision`, sittingID.String()); selectErr != nil {
			t.Fatal(selectErr)
		}
		items := make([]storetest.ExamCorrectionProvenanceProbe, len(rows))
		for index, row := range rows {
			previousID, parseErr := model.ParseExamRevisionID(row.PreviousRevisionID)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			correctionID, parseErr := model.ParseExamRevisionID(row.CorrectionRevisionID)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			items[index] = storetest.ExamCorrectionProvenanceProbe{PreviousRevisionID: previousID, CorrectionRevisionID: correctionID,
				PrivateReason: row.PrivateReason, SittingRevision: row.SittingRevision}
		}
		return items
	}, AssertAppendOnly: func(t *testing.T, ctx context.Context, sittingID model.ExamSittingID) {
		t.Helper()
		if _, updateErr := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_live_corrections SET private_reason='tampered' WHERE exam_sitting_id=?`, sittingID.String()); updateErr == nil {
			t.Fatal("live correction provenance UPDATE unexpectedly succeeded")
		}
		if _, deleteErr := persistence.GetMaster().Exec(ctx, `DELETE FROM exam_sitting_live_corrections WHERE exam_sitting_id=?`, sittingID.String()); deleteErr == nil {
			t.Fatal("live correction provenance DELETE unexpectedly succeeded")
		}
	}, ExpireStage: func(t *testing.T, ctx context.Context, stageID model.ExamCorrectionResourceStageID) {
		t.Helper()
		_, transactionErr := runSQLTransaction(ctx, persistence.GetMaster().Begin, "age correction stage", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
			if _, execErr := tx.Exec(ctx, `UPDATE exam_correction_resource_stages SET created_at=statement_timestamp()-INTERVAL '26 hours',ready_at=statement_timestamp()-INTERVAL '25 hours' WHERE id=? AND state='ready'`, stageID.String()); execErr != nil {
				return struct{}{}, execErr
			}
			result, execErr := tx.Exec(ctx, `UPDATE upload_leases SET created_at=statement_timestamp()-INTERVAL '26 hours',updated_at=statement_timestamp()-INTERVAL '25 hours',expires_at=statement_timestamp()-INTERVAL '25 hours' WHERE id=(SELECT upload_lease_id FROM exam_correction_resource_stages WHERE id=?)`, stageID.String())
			if execErr != nil {
				return struct{}{}, execErr
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return struct{}{}, fmt.Errorf("expire correction stage affected=%d", affected)
			}
			return struct{}{}, nil
		})
		if transactionErr != nil {
			t.Fatal(transactionErr)
		}
	}, ExpireStageOutcome: func(t *testing.T, ctx context.Context, stageID model.ExamCorrectionResourceStageID) {
		t.Helper()
		result, execErr := persistence.GetMaster().Exec(ctx, `UPDATE command_outcomes SET created_at=statement_timestamp()-INTERVAL '26 hours',expires_at=statement_timestamp()-INTERVAL '1 hour' WHERE operation=? AND outcome->>'stage_id'=?`, store.ExamCorrectionResourceStageOperation, stageID.String())
		if execErr != nil {
			t.Fatal(execErr)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("expire correction stage outcome affected=%d", affected)
		}
	}, ReleaseStageCleanupProtection: func(t *testing.T, ctx context.Context, stageID model.ExamCorrectionResourceStageID) {
		t.Helper()
		result, execErr := persistence.GetMaster().Exec(ctx, `UPDATE exam_correction_resource_stages SET cleanup_protected_until=statement_timestamp()-INTERVAL '1 second' WHERE id=?`, stageID.String())
		if execErr != nil {
			t.Fatal(execErr)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("release correction stage cleanup protection affected=%d", affected)
		}
	}, RemoveBytes: func(t *testing.T, ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) {
		t.Helper()
		if removeErr := content.RemoveExamResource(ctx, revisionID, renditionID); removeErr != nil {
			t.Fatal(removeErr)
		}
	}, AssertPurged: func(t *testing.T, ctx context.Context, stage *store.ExamCorrectionResourceStage) {
		t.Helper()
		for _, query := range []struct {
			name string
			sql  string
			arg  string
		}{
			{"stage", `SELECT COUNT(*) FROM exam_correction_resource_stages WHERE id=?`, stage.ID.String()},
			{"revision", `SELECT COUNT(*) FROM file_revisions WHERE id=?`, stage.FileRevisionID.String()},
			{"lease", `SELECT COUNT(*) FROM upload_leases WHERE id=?`, stage.UploadLeaseID.String()},
			{"rendition", `SELECT COUNT(*) FROM file_renditions WHERE id=?`, stage.RenditionID.String()},
			{"identity", `SELECT COUNT(*) FROM exam_resource_identities WHERE id=?`, stage.ResourceID.String()},
			{"entry", `SELECT COUNT(*) FROM file_entries WHERE id=?`, stage.FileEntryID.String()},
		} {
			var count int
			if getErr := persistence.GetMaster().Get(ctx, &count, query.sql, query.arg); getErr != nil {
				t.Fatal(getErr)
			}
			if count != 0 {
				t.Fatalf("purged correction %s count=%d", query.name, count)
			}
		}
		var outcomeCount int
		if getErr := persistence.GetMaster().Get(ctx, &outcomeCount, `SELECT COUNT(*) FROM command_outcomes WHERE operation=? AND outcome->>'stage_id'=?`, store.ExamCorrectionResourceStageOperation, stage.ID.String()); getErr != nil {
			t.Fatal(getErr)
		}
		if outcomeCount != 0 {
			t.Fatalf("purged correction command outcome count=%d", outcomeCount)
		}
	}}
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
		if _, advanceErr := persistence.GetMaster().Exec(ctx, `UPDATE exam_starter_workspace_objects SET reclaim_after=created_at WHERE id=?`, fixture.OldWorkspaceObjectID.String()); advanceErr != nil {
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
	if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO exam_resource_identities (id,exam_id,file_entry_id) VALUES (?,?,?)`, resourceID.String(), examID.String(), entryID.String()); err != nil {
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
		_, err = tx.ExecContext(ctx, `INSERT INTO exam_revisions (id,exam_id,number,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,execution_profile_document,execution_profile_canonical,execution_profile_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,base_revision_id,publication_kind,sealed)
			SELECT $1,exam_id,number+$2,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,execution_profile_document,execution_profile_canonical,execution_profile_digest,starter_workspace_digest,content_digest,$3,0,0,published_by_user_id,published_at,NULL,publication_kind,$4 FROM exam_revisions WHERE id=$5`, revisionID.String(), test.number, test.count, test.sealed, fixture.RevisionID.String())
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO exam_revisions (id,exam_id,number,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,execution_profile_document,execution_profile_canonical,execution_profile_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,base_revision_id,publication_kind,sealed)
		SELECT $1,exam_id,number+1000,snapshot_schema_version,source_draft_revision,title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,execution_profile_document,execution_profile_canonical,execution_profile_digest,starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,published_by_user_id,published_at,NULL,publication_kind,FALSE FROM exam_revisions WHERE id=$2`, unsealedRevisionID.String(), fixture.RevisionID.String()); err != nil {
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
	StoreTest(t, func(t *testing.T, persistence store.Store) {
		storetest.TestUserStore(t, persistence, userRegistrationSQLProbe(persistence.(*SQLStore)))
	})
}

func userRegistrationSQLProbe(persistence *SQLStore) storetest.UserStoreSQLProbe {
	return storetest.UserStoreSQLProbe{SetPublicRegistration: func(t *testing.T, enabled, localLogin bool) {
		t.Helper()
		if _, err := persistence.GetMaster().Exec(context.Background(), `
			UPDATE access_policies
			   SET local_login_enabled=?, public_registration_enabled=?, invitation_local_credential_enabled=?, revision=revision+1,
			       updated_at=clock_timestamp()
			 WHERE singleton=1`, localLogin, enabled, localLogin); err != nil {
			t.Fatal(err)
		}
	}, DatabaseNow: func(t *testing.T) time.Time {
		t.Helper()
		var now time.Time
		if err := persistence.GetMaster().Get(context.Background(), &now, `SELECT clock_timestamp()`); err != nil {
			t.Fatal(err)
		}
		return now.UTC().Truncate(time.Millisecond)
	}}
}

func TestUserSettingsStore(t *testing.T) {
	StoreTest(t, storetest.TestUserSettingsStore)
}

func TestUserSettingsRequiredRowAndCascade(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	missing := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "settings-missing-row",
		Email:    "settings-missing-row@example.test",
	})
	if _, err := persistence.GetMaster().Exec(
		ctx,
		"DELETE FROM user_settings_documents WHERE user_id = $1",
		missing.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.UserSettings().Get(ctx, missing.ID); err == nil || store.IsNotFound(err) ||
		strings.Contains(err.Error(), missing.ID.String()) {
		t.Fatalf("missing required settings row error = %v", err)
	}

	cascaded := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "settings-cascade",
		Email:    "settings-cascade@example.test",
	})
	if _, err := persistence.GetMaster().Exec(ctx, "DELETE FROM users WHERE id = $1", cascaded.ID.String()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := persistence.GetMaster().Get(
		ctx,
		&count,
		"SELECT count(*) FROM user_settings_documents WHERE user_id = $1",
		cascaded.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("hard-deleted User retained %d settings rows", count)
	}
}

func TestFileStore(t *testing.T) {
	StoreTest(t, storetest.TestFileStore)
}

func TestJobStore(t *testing.T) {
	StoreTest(t, storetest.TestJobStore)
}

func TestExternalIdentityStore(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, map[string]model.ProviderAdmissionMode{
		"campus-cas": model.ProviderAdmissionAutoProvision,
	}, storetest.TestExternalIdentityStore)
}

func TestExternalIdentityAdmissionModes(t *testing.T) {
	for _, mode := range []model.ProviderAdmissionMode{
		model.ProviderAdmissionLinkedOnly,
		model.ProviderAdmissionInvitationRequired,
		model.ProviderAdmissionAutoProvision,
	} {
		t.Run(string(mode), func(t *testing.T) {
			persistence := openTestStore(t)
			resetPristineTestStore(t, persistence)
			seedTestAuthenticationPolicy(t, persistence, map[string]model.ProviderAdmissionMode{"campus-cas": mode})
			storetest.TestExternalIdentityAdmissionMode(t, persistence, mode, storetest.ExternalIdentityAdmissionSQLProbe{
				BackdateState: func(t *testing.T, id model.ExternalLoginStateID, expiresAt time.Time) {
					t.Helper()
					if _, err := persistence.GetMaster().Exec(context.Background(), `UPDATE external_login_states
						SET created_at=?,updated_at=?,consumed_at=?,expires_at=? WHERE id=?`, expiresAt.Add(-2*time.Minute),
						expiresAt.Add(-time.Minute), expiresAt.Add(-time.Minute), expiresAt, id.String()); err != nil {
						t.Fatal(err)
					}
				},
			})
		})
	}
}

func TestExternalLoginStateStore(t *testing.T) {
	StoreTest(t, func(t *testing.T, persistence store.Store) {
		storetest.TestExternalLoginStateStore(t, persistence, externalLoginStateSQLProbe(persistence.(*SQLStore)))
	})
}

func externalLoginStateSQLProbe(sqlStore *SQLStore) storetest.ExternalLoginStateSQLProbe {
	return storetest.ExternalLoginStateSQLProbe{Backdate: func(t *testing.T, id model.ExternalLoginStateID, createdAt, expiresAt time.Time) {
		t.Helper()
		if _, err := sqlStore.GetMaster().Exec(context.Background(), `UPDATE external_login_states SET created_at=?, updated_at=?, expires_at=? WHERE id=?`, createdAt, createdAt, expiresAt, id.String()); err != nil {
			t.Fatal(err)
		}
	}}
}

func TestPasswordCredentialStore(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, map[string]model.ProviderAdmissionMode{
		"campus-cas": model.ProviderAdmissionLinkedOnly,
	}, storetest.TestPasswordCredentialStore)
}

func TestUserTokenStore(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, nil, storetest.TestUserTokenStore)
}

func TestInvitationStore(t *testing.T) {
	sqlStore := openTestStore(t)
	resetPristineTestStore(t, sqlStore)
	seedTestAuthenticationPolicy(t, sqlStore, nil)
	storetest.TestInvitationStore(t, sqlStore, invitationAuthoritySQLProbe(t, sqlStore))
	storetest.TestInvitationHierarchyFences(t, sqlStore, invitationHierarchySQLProbe(t, sqlStore))
	storetest.TestInvitationTerminalizationSuppressesCredentialDelivery(t, sqlStore)
	ctx := context.Background()
	var target struct {
		InvitationID string `db:"invitation_id"`
		DeliveryID   string `db:"delivery_id"`
	}
	if err := sqlStore.GetMaster().Get(ctx, &target, `SELECT i.id invitation_id,d.id delivery_id FROM invitations i JOIN mail_deliveries d ON d.target_invitation_id=i.id
		WHERE i.state='accepted' AND d.state IN ('accepted','suppressed','canceled')
		  AND NOT EXISTS (SELECT 1 FROM browser_authentication_transactions bat WHERE bat.invitation_id=i.id)
		ORDER BY i.id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlStore.GetMaster().Exec(ctx, `UPDATE invitations SET created_at=created_at-interval '100 days',
		updated_at=updated_at-interval '100 days',expires_at=expires_at-interval '100 days',accepted_at=accepted_at-interval '100 days'
		WHERE id=?`, target.InvitationID); err != nil {
		t.Fatal(err)
	}
	maintained, err := sqlStore.Invitation().Maintain(ctx, 500)
	if err != nil || maintained.Purged != 1 {
		t.Fatalf("Maintain() = %#v, %v", maintained, err)
	}
	if _, err = sqlStore.Invitation().Get(ctx, model.InvitationID(target.InvitationID)); !store.IsNotFound(err) {
		t.Fatalf("purged Invitation error = %v", err)
	}
	delivery, err := sqlStore.Mail().GetDelivery(ctx, model.MailDeliveryID(target.DeliveryID))
	if err != nil || delivery.TargetInvitationID != model.InvitationID(target.InvitationID) || delivery.TargetUserID.IsValid() {
		t.Fatalf("preserved historical delivery = %#v, %v", delivery, err)
	}
}

func TestPersonalAccessTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestPersonalAccessTokenStore)
}

func TestMFAStore(t *testing.T) {
	StoreTest(t, storetest.TestMFAStore)
}

func TestSessionStores(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, nil, storetest.TestSessionStores)
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

func TestAuditStoreAcceptsGranularAcademicResources(t *testing.T) {
	persistence := openTestStore(t)
	for _, test := range []struct {
		name     string
		action   model.Action
		resource model.Resource
	}{
		{
			name: "programme owner for programme level creation", action: model.ActionProgrammeLevelManage,
			resource: model.Resource{Type: model.ResourceProgramme, ID: model.NewProgrammeID().String()},
		},
		{
			name: "programme level", action: model.ActionProgrammeLevelManage,
			resource: model.Resource{Type: model.ResourceProgrammeLevel, ID: model.NewProgrammeLevelID().String()},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := &model.AuditEvent{
				Action: string(test.action), Resource: test.resource,
				ScopeType: model.RoleScopeAcademicUnit, ScopeID: model.NewAcademicUnitID().String(),
				Status: model.AuditStatusSuccess, NodeID: "node-test",
			}
			saved, err := persistence.Audit().Save(context.Background(), event)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Resource != test.resource {
				t.Fatalf("saved resource = %#v, want %#v", saved.Resource, test.resource)
			}
		})
	}
}

func TestInstallationStore(t *testing.T) {
	PristineStoreTest(t, storetest.TestInstallationStore)
}

func TestDesktopCompatibilityPolicyStore(t *testing.T) {
	PristineStoreTest(t, storetest.TestDesktopCompatibilityPolicyStore)
}

func TestSessionRevocationAttemptFence(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, nil, storetest.TestSessionRevocationAttemptFence)
}

func TestSessionExpiryAttemptFence(t *testing.T) {
	StoreTestWithAuthenticationPolicy(t, nil, storetest.TestSessionExpiryAttemptFence)
}

func TestAdministratorRecoveryStore(t *testing.T) {
	PristineStoreTest(t, func(t *testing.T, persistence store.Store) {
		sqlPersistence := persistence.(*SQLStore)
		storetest.TestAdministratorRecoveryStore(t, persistence, storetest.AdministratorRecoverySQLProbe{
			HoldAuthenticationPathFence: func(t *testing.T, ctx context.Context) (int, func()) {
				t.Helper()
				return holdSystemAdministratorAuthenticationPathFence(t, ctx, sqlPersistence)
			},
			HoldServingNodeLeaseFence: func(t *testing.T, ctx context.Context) (int, func()) {
				t.Helper()
				return holdServingNodeLeaseFence(t, ctx, sqlPersistence)
			},
			WaitForBlockedTransactions: func(t *testing.T, ctx context.Context, blockerPID, want int) {
				t.Helper()
				waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, sqlPersistence, blockerPID, want)
			},
		})
	})
}

func TestCommandOutcomeStore(t *testing.T) {
	StoreTest(t, storetest.TestCommandOutcomeStore)
}
