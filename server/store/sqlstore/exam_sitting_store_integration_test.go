//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestExamSittingStoreAdapter(t *testing.T) {
	StoreTest(t, storetest.TestExamSittingStore)
}

func TestExamSittingMailReconciliationAfterObsoleteSuppression(t *testing.T) {
	StoreTest(t, storetest.TestExamSittingMailReconciliationAfterObsoleteSuppression)
}

func TestExamSittingClassTransferMailReconciliation(t *testing.T) {
	StoreTest(t, storetest.TestExamSittingClassTransferMailReconciliation)
}

func TestExamSittingDisabledMailReconciliationConverges(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingDisabledMailReconciliationConverges(t, persistence, storetest.ExamSittingDisabledMailSQLProbe{
		AgeTerminalFanout: func(t *testing.T, ctx context.Context, occurrenceID model.MailOccurrenceID) {
			t.Helper()
			_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_mail_fanouts SET
				created_at=statement_timestamp()-INTERVAL '102 days',deadline=statement_timestamp()-INTERVAL '101 days',
				completed_at=statement_timestamp()-INTERVAL '100 days' WHERE occurrence_id=?`, occurrenceID.String())
			require.NoError(t, err)
		},
	})
}

func TestExamSittingDisabledMailEligibilityChronologySerializesBothCommitOrders(t *testing.T) {
	for _, test := range []struct {
		name        string
		markerFirst bool
	}{
		{name: "marker before eligibility", markerFirst: true},
		{name: "eligibility before marker", markerFirst: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			persistence := openTestStore(t)
			resetTestStore(t, persistence)
			fixture := storetest.PrepareExamSittingDisabledEligibilityRaceFixture(t, persistence,
				"sitting-disabled-eligibility-"+fmt.Sprint(test.markerFirst))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			controller, err := persistence.GetMaster().DB().Conn(ctx)
			require.NoError(t, err)
			defer controller.Close()
			pauseKey := int64(8154700260915)
			if !test.markerFirst {
				pauseKey++
			}
			_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey)
			require.NoError(t, err)
			locked := true
			defer func() {
				if locked {
					_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
				}
			}()
			table, condition, blockedQuery := "exam_sittings", "NEW.mail_disabled_suppressed_revision IS NOT NULL", "UPDATE exam_sittings SET mail_reconciliation_actor_user_id"
			if !test.markerFirst {
				table, condition, blockedQuery = "users", "NEW.email_verified AND NOT OLD.email_verified", "UPDATE users SET email_verified"
			}
			functionName := fmt.Sprintf("proctor_test_sitting_eligibility_%t", test.markerFirst)
			triggerName := functionName + "_trigger"
			statement := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF %s THEN PERFORM pg_advisory_xact_lock(%d); END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION %s()`,
				functionName, condition, pauseKey, triggerName, table, functionName)
			_, err = persistence.GetMaster().Exec(ctx, statement)
			require.NoError(t, err)
			defer func() {
				_, _ = persistence.GetMaster().Exec(context.Background(), fmt.Sprintf(
					`DROP TRIGGER IF EXISTS %s ON %s; DROP FUNCTION IF EXISTS %s()`, triggerName, table, functionName))
			}()

			scheduleResult := make(chan error, 1)
			verifyResult := make(chan error, 1)
			startSchedule := func() {
				go func() {
					_, scheduleErr := persistence.ExamSitting().Schedule(ctx, fixture.Schedule, fixture.Command)
					scheduleResult <- scheduleErr
				}()
			}
			startVerification := func() {
				go func() {
					_, verifyErr := persistence.UserToken().VerifyEmailPrivileged(ctx, fixture.Verification)
					verifyResult <- verifyErr
				}()
			}
			if test.markerFirst {
				startSchedule()
				markerPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID(t, ctx, controller), blockedQuery)
				startVerification()
				_ = waitForBlockedMailQuery(t, ctx, persistence, markerPID, "UPDATE mail_audience_states")
			} else {
				startVerification()
				eligibilityPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID(t, ctx, controller), blockedQuery)
				startSchedule()
				_ = waitForBlockedMailQuery(t, ctx, persistence, eligibilityPID, "mail_audience_states")
			}
			_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey)
			require.NoError(t, err)
			locked = false
			err = <-scheduleResult
			require.NoError(t, err)
			err = <-verifyResult
			require.NoError(t, err)
			due, err := persistence.ExamSitting().ListMailReconciliationDue(ctx,
				store.ExamSittingMailReconciliationOptions{Limit: model.SittingMailExpansionPageSize})
			require.NoError(t, err)
			found := false
			for _, candidate := range due {
				found = found || candidate.Sitting.ID == fixture.SittingID
			}
			require.Equal(t, test.markerFirst, found, "reconciliation due=%v want=%v candidates=%#v", found, test.markerFirst, due)
		})
	}
}

func TestExamSittingReconciliationAndInvitationAcceptanceShareUserClassLockOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.PrepareExamSittingInvitationLockOrderFixture(t, persistence)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	controllerBackendPID := controllerPID(t, ctx, controller)
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, classLifecycleLock)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, classLifecycleLock)
		}
	}()
	reconcileResult := make(chan error, 1)
	go func() {
		_, reconcileErr := persistence.ExamSitting().ReconcileMail(ctx, fixture.Reconciliation)
		reconcileResult <- reconcileErr
	}()
	reconcilePID := waitForBlockedMailQuery(t, ctx, persistence, controllerBackendPID, "pg_advisory_xact_lock")
	acceptResult := make(chan error, 1)
	go func() {
		_, acceptErr := persistence.Invitation().AcceptStudentClass(ctx, fixture.Acceptance)
		acceptResult <- acceptErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, reconcilePID, "FROM users WHERE email")
	_, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, classLifecycleLock)
	require.NoError(t, err)
	locked = false
	err = <-reconcileResult
	require.NoError(t, err)
	err = <-acceptResult
	require.NoError(t, err)
}

// TestDisabledSittingReconciliationAndInvitationAcceptanceUseUserSingletonClassOrder
// holds the mail-eligibility singleton while reconciliation and Invitation
// acceptance target the same User. Reconciliation must wait before acquiring
// the shared Class/hierarchy fence, and acceptance must wait on its User lock.
func TestDisabledSittingReconciliationAndInvitationAcceptanceUseUserSingletonClassOrder(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.PrepareExamSittingInvitationLockOrderFixture(t, persistence)
	canceledJob, err := fixture.Reconciliation.Mail.ExpansionJob.RequestCancellation(
		fixture.Reconciliation.Mail.ExpansionJob.CreatedAt,
	)
	require.NoError(t, err)
	fixture.Reconciliation.Mail.Bundle = nil
	fixture.Reconciliation.Mail.ExpansionJob = canceledJob

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	controllerBackendPID := controllerPID(t, ctx, controller)
	_, err = controller.ExecContext(ctx, `BEGIN`)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	_, err = controller.ExecContext(ctx, `SELECT singleton FROM mail_audience_states WHERE singleton=1 FOR UPDATE`)
	require.NoError(t, err)

	reconcileResult := make(chan error, 1)
	go func() {
		_, reconcileErr := persistence.ExamSitting().ReconcileMail(ctx, fixture.Reconciliation)
		reconcileResult <- reconcileErr
	}()
	reconcilePID := waitForBlockedMailQuery(t, ctx, persistence, controllerBackendPID, "mail_audience_states")

	probe, err := persistence.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer probe.Close()
	_, err = probe.ExecContext(ctx, `BEGIN`)
	require.NoError(t, err)
	var classAvailable bool
	err = probe.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, classLifecycleLock).Scan(&classAvailable)
	require.NoError(t, err)
	_, err = probe.ExecContext(ctx, `ROLLBACK`)
	require.NoError(t, err)
	require.True(t, classAvailable, "disabled reconciliation acquired Class/hierarchy before mail eligibility singleton")

	acceptResult := make(chan error, 1)
	go func() {
		_, acceptErr := persistence.Invitation().AcceptStudentClass(ctx, fixture.Acceptance)
		acceptResult <- acceptErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, reconcilePID, "FROM users")
	_, err = controller.ExecContext(ctx, `ROLLBACK`)
	require.NoError(t, err)
	locked = false
	err = <-reconcileResult
	require.NoError(t, err)
	err = <-acceptResult
	require.NoError(t, err)
}

func TestInvitationAcceptanceAndAuthenticationMethodMutationUseGlobalUserOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	primary := openTestStore(t)
	resetTestStore(t, primary)
	secondary := openTestStore(t)
	fixture := storetest.PrepareExamSittingInvitationLockOrderFixture(t, primary)
	userID := fixture.Reconciliation.ActorUserID
	audit, err := primary.Audit().Save(ctx, &model.AuditEvent{
		ActorID: userID, Action: string(model.ActionExternalIdentityManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(), Status: model.AuditStatusAttempt,
		NodeID: "invitation-authentication-method-order",
	})
	require.NoError(t, err)

	controller, err := primary.GetMaster().DB().Conn(ctx)
	require.NoError(t, err)
	defer controller.Close()
	controllerBackendPID := controllerPID(t, ctx, controller)
	_, err = controller.ExecContext(ctx, `BEGIN`)
	require.NoError(t, err)
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	_, err = controller.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID.String())
	require.NoError(t, err)

	type acceptanceOutcome struct {
		result *store.StudentClassInvitationAcceptanceResult
		err    error
	}
	acceptResult := make(chan acceptanceOutcome, 1)
	go func() {
		result, acceptErr := primary.Invitation().AcceptStudentClass(ctx, fixture.Acceptance)
		acceptResult <- acceptanceOutcome{result: result, err: acceptErr}
	}()
	acceptPID := waitForBlockedMailQuery(t, ctx, primary, controllerBackendPID, "FROM users")

	type enrollmentOutcome struct {
		result *store.AuthenticationMethodMutationResult
		err    error
	}
	enrollResult := make(chan enrollmentOutcome, 1)
	go func() {
		result, enrollErr := secondary.PasswordCredential().Enroll(ctx, &store.PasswordCredentialEnrollment{
			Credential: &model.PasswordCredential{UserID: userID, PasswordHash: "$argon2id$concurrent-enrollment"},
			Capabilities: store.AccessDeploymentCapabilities{
				Providers: map[string]store.AccessProviderCapability{},
			},
			AuditEventID: audit.ID, AuditAt: model.GetMillis(),
		})
		enrollResult <- enrollmentOutcome{result: result, err: enrollErr}
	}()
	waitForSecondLockWaiter(t, ctx, primary, controllerBackendPID, acceptPID)
	_, err = controller.ExecContext(ctx, `ROLLBACK`)
	require.NoError(t, err)
	locked = false

	accepted := <-acceptResult
	require.NoError(t, accepted.err, "AcceptStudentClass()")
	require.NotNil(t, accepted.result, "AcceptStudentClass()")
	require.NotNil(t, accepted.result.Invitation, "AcceptStudentClass()")
	require.Equal(t, model.InvitationAccepted, accepted.result.Invitation.State, "AcceptStudentClass()")
	enrolled := <-enrollResult
	require.Nil(t, enrolled.result, "Enroll() after Invitation acceptance")
	require.True(t, store.IsConflict(enrolled.err), "Enroll() after Invitation acceptance")
	terminal, err := primary.Audit().Get(ctx, audit.ID)
	require.NoError(t, err, "authentication-method audit = %#v", terminal)
	require.Equal(t, model.AuditStatusAttempt, terminal.Status, "authentication-method audit = %#v", terminal)
}

func controllerPID(t *testing.T, ctx context.Context, connection interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) int {
	t.Helper()
	var pid int
	require.NoError(t, connection.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	return pid
}

func TestExamSittingMailExpansionMaintenance(t *testing.T) {
	StoreTest(t, storetest.TestExamSittingMailExpansionMaintenance)
}

func TestExamSittingMailRetentionCleanup(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingMailRetentionCleanup(t, persistence, storetest.ExamSittingMailRetentionSQLProbe{
		AgeDeliveries: func(t *testing.T, ctx context.Context, accepted, suppressed, failed model.MailDeliveryID) {
			t.Helper()
			_, err := persistence.GetMaster().Exec(ctx, `UPDATE mail_deliveries SET
				created_at=statement_timestamp()-INTERVAL '102 days',message_date=statement_timestamp()-INTERVAL '102 days',
				deadline=statement_timestamp()-INTERVAL '101 days',updated_at=statement_timestamp()-INTERVAL '100 days',
				accepted_at=statement_timestamp()-INTERVAL '100 days' WHERE id=?`, accepted.String())
			require.NoError(t, err)
			_, err = persistence.GetMaster().Exec(ctx, `UPDATE mail_deliveries SET
				created_at=statement_timestamp()-INTERVAL '102 days',message_date=statement_timestamp()-INTERVAL '102 days',
				deadline=statement_timestamp()-INTERVAL '101 days',updated_at=statement_timestamp()-INTERVAL '100 days'
				WHERE id=?`, suppressed.String())
			require.NoError(t, err)
			_, err = persistence.GetMaster().Exec(ctx, `UPDATE mail_deliveries SET
				created_at=statement_timestamp()-INTERVAL '183 days',message_date=statement_timestamp()-INTERVAL '183 days',
				deadline=statement_timestamp()-INTERVAL '182 days',updated_at=statement_timestamp()-INTERVAL '181 days',
				failed_at=statement_timestamp()-INTERVAL '181 days' WHERE id=?`, failed.String())
			require.NoError(t, err)
		},
		AssertRetired: func(t *testing.T, ctx context.Context, sittingID model.ExamSittingID,
			occurrenceID model.MailOccurrenceID, userIDs []model.UserID,
		) {
			t.Helper()
			var fanouts, occurrences int
			require.NoError(t, persistence.GetMaster().Get(ctx, &fanouts,
				`SELECT COUNT(*) FROM exam_sitting_mail_fanouts WHERE occurrence_id=?`, occurrenceID.String()))
			require.NoError(t, persistence.GetMaster().Get(ctx, &occurrences,
				`SELECT COUNT(*) FROM mail_occurrences WHERE id=?`, occurrenceID.String()))
			require.Equal(t, 0, fanouts, "retained fan-outs")
			require.Equal(t, 0, occurrences, "retained fan-outs")
			var rows []struct {
				UserID               string `db:"user_id"`
				DesiredDeliveryID    string `db:"desired_delivery_id"`
				CommunicatedTemplate string `db:"communicated_template_key"`
			}
			require.NoError(t, persistence.GetMaster().Select(ctx, &rows, `SELECT user_id,
				COALESCE(desired_delivery_id,'') desired_delivery_id,COALESCE(communicated_template_key,'') communicated_template_key
				FROM exam_sitting_mail_recipients WHERE exam_sitting_id=? ORDER BY user_id`, sittingID.String()))
			require.Len(t, rows, len(userIDs))
			communicated := 0
			for _, row := range rows {
				require.Empty(t, row.DesiredDeliveryID)
				if row.CommunicatedTemplate == string(model.MailTemplateExamSittingScheduled) {
					communicated++
				}
			}
			require.Equal(t, 1, communicated, "communicated projection count=%d rows=%#v", communicated, rows)
		},
	})
}

func TestExamSittingMailExpansionMaintenanceUsesPostgreSQLTimeAndRecoversOrphan(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.PrepareExamSittingMailRecoveryFixture(t, persistence)
	ctx := context.Background()
	var referencesBefore int64
	require.NoError(t, persistence.GetMaster().Get(ctx, &referencesBefore,
		`SELECT active_references FROM mail_payload_keys WHERE key_id='11111111111111111111111111111111'`))
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_mail_fanouts
		SET created_at=statement_timestamp()-INTERVAL '2 days',deadline=statement_timestamp()-INTERVAL '1 second'
		WHERE occurrence_id=?`, fixture.ExpiredOccurrence.String())
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `DELETE FROM jobs WHERE id=?`, fixture.OrphanJob.String())
	require.NoError(t, err)
	var before time.Time
	require.NoError(t, persistence.GetMaster().Get(ctx, &before, `SELECT statement_timestamp()`))
	result, err := persistence.ExamSitting().MaintainMailExpansions(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 2, result.FanoutsTerminalized)
	require.Equal(t, 0, result.DeliveriesSuppressed)
	require.False(t, result.More)
	var after time.Time
	err = persistence.GetMaster().Get(ctx, &after, `SELECT statement_timestamp()`)
	require.NoError(t, err)
	var rows []struct {
		OccurrenceID   string    `db:"occurrence_id"`
		TerminalReason string    `db:"terminal_reason"`
		CompletedAt    time.Time `db:"completed_at"`
		BundleID       string    `db:"bundle_id"`
	}
	err = persistence.GetMaster().Select(ctx, &rows, `SELECT occurrence_id,terminal_reason,completed_at,
		COALESCE(bundle_id,'') bundle_id FROM exam_sitting_mail_fanouts
		WHERE occurrence_id IN (?,?) ORDER BY occurrence_id`, fixture.ExpiredOccurrence.String(), fixture.OrphanOccurrence.String())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	reasons := map[string]string{}
	for _, row := range rows {
		reasons[row.OccurrenceID] = row.TerminalReason
		require.Empty(t, row.BundleID, "terminal fan-out row=%#v PostgreSQL window=[%s,%s]", row, before, after)
		require.False(t, row.CompletedAt.Before(before), "terminal fan-out row=%#v PostgreSQL window=[%s,%s]", row, before, after)
		require.False(t, row.CompletedAt.After(after), "terminal fan-out row=%#v PostgreSQL window=[%s,%s]", row, before, after)
	}
	require.Equal(t, "expired", reasons[fixture.ExpiredOccurrence.String()])
	require.Equal(t, "orphaned", reasons[fixture.OrphanOccurrence.String()])
	expiredJob, err := persistence.Job().Get(ctx, fixture.ExpiredJob)
	require.NoError(t, err, "expired expansion Job=%#v err", expiredJob)
	require.Equal(t, model.JobStatusCanceled, expiredJob.Status, "expired expansion Job=%#v err", expiredJob)
	var referencesAfter int64
	err = persistence.GetMaster().Get(ctx, &referencesAfter,
		`SELECT COALESCE(SUM(active_references),0) FROM mail_payload_keys
		WHERE key_id='11111111111111111111111111111111'`)
	require.NoError(t, err)
	require.Equal(t, referencesBefore-2, referencesAfter, "bundle key references before=%d after", referencesBefore)
}

func TestExamSittingStoreSQLGuards(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingStoreSQLGuards(t, persistence, storetest.ExamSittingSQLProbe{
		ArchiveProgrammeLevel: func(t *testing.T, ctx context.Context, id model.ProgrammeLevelID) {
			t.Helper()
			at := model.NowUTC().Add(time.Millisecond)
			result, err := persistence.GetMaster().Exec(ctx, `UPDATE programme_levels
				SET archived_at=?,updated_at=?,revision=revision+1 WHERE id=? AND archived_at IS NULL`, at, at, id.String())
			require.NoError(t, err)
			affected, err := result.RowsAffected()
			require.NoError(t, err, "archive Programme Level affected")
			require.Equal(t, int64(1), affected, "archive Programme Level affected")
		},
	})
}

func TestExamSittingLifecycleStore(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingLifecycleStore(t, persistence, storetest.ExamSittingLifecycleSQLProbe{
		SetSchedule: func(t *testing.T, ctx context.Context, id model.ExamSittingID, startAt, endAt time.Time) {
			t.Helper()
			// The shared fixture intentionally starts with a future Academic
			// Period so scheduling is valid. Widen that same Period when the
			// lifecycle probe advances PostgreSQL time across a boundary; this
			// keeps opening revalidation about lifecycle state, not fixture dates.
			_, err := persistence.GetMaster().Exec(ctx, `UPDATE academic_periods ap
				SET start_at=LEAST(ap.start_at,?),end_at=GREATEST(ap.end_at,?)
				FROM classes c JOIN exam_sittings s ON s.class_id=c.id
				WHERE s.id=? AND ap.id=c.academic_period_id`, startAt.Add(-time.Hour), endAt.Add(time.Hour), id.String())
			require.NoError(t, err)
			result, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sittings SET scheduled_start_at=?,scheduled_end_at=? WHERE id=?`,
				startAt, endAt, id.String())
			require.NoError(t, err)
			affected, err := result.RowsAffected()
			require.NoError(t, err, "set Sitting schedule affected")
			require.Equal(t, int64(1), affected, "set Sitting schedule affected")
		},
		PrivateActions: func(t *testing.T, ctx context.Context, id model.ExamSittingID) []storetest.ExamSittingPrivateActionProbe {
			t.Helper()
			var rows []struct {
				ActionCode    string `db:"action_code"`
				PrivateReason string `db:"private_reason"`
				Revision      int64  `db:"sitting_revision"`
			}
			require.NoError(t, persistence.GetMaster().Select(ctx, &rows, `SELECT action_code,private_reason,sitting_revision
				FROM exam_sitting_private_actions WHERE exam_sitting_id=? ORDER BY sitting_revision`, id.String()))
			result := make([]storetest.ExamSittingPrivateActionProbe, len(rows))
			for index, row := range rows {
				result[index] = storetest.ExamSittingPrivateActionProbe{ActionCode: row.ActionCode,
					PrivateReason: row.PrivateReason, Revision: row.Revision}
			}
			return result
		},
		AssertAppendOnly: func(t *testing.T, ctx context.Context, id model.ExamSittingID) {
			t.Helper()
			_, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_private_actions SET private_reason='changed'
				WHERE exam_sitting_id=?`, id.String())
			require.Error(t, err, "private action UPDATE unexpectedly succeeded")
			_, err = persistence.GetMaster().Exec(ctx, `DELETE FROM exam_sitting_private_actions WHERE exam_sitting_id=?`, id.String())
			require.Error(t, err, "private action DELETE unexpectedly succeeded")
		},
	})
	testExamSittingLifecycleDueBoundedPlan(t, persistence)
}
