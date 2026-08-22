//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func invitationAuthoritySQLProbe(t *testing.T, primary *SQLStore) storetest.InvitationSQLProbe {
	t.Helper()
	secondary := openTestStore(t)
	return storetest.InvitationSQLProbe{
		PayloadKeyReferences: func(t *testing.T, ctx context.Context, keyID string) int64 {
			var references int64
			if err := primary.GetMaster().Get(ctx, &references,
				`SELECT COALESCE((SELECT active_references FROM mail_payload_keys WHERE key_id=?),0)`, keyID); err != nil {
				t.Fatal(err)
			}
			return references
		},
		ArchiveTeacherUnitBeforeAccept: func(t *testing.T, ctx context.Context, unit *model.AcademicUnit, operation func() error) error {
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "teacher_invitation_unit_archive", "academic_units", "UPDATE academic_units", 8154700260827, func() error {
				_, err := secondary.AcademicUnit().Archive(ctx, unit.ID.String(), model.GetMillis())
				return err
			}, operation)
		},
		ArchiveTeacherUnitBeforeMail: func(t *testing.T, ctx context.Context, unit *model.AcademicUnit, operation func() error) error {
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "teacher_invitation_mail_unit_archive", "academic_units", "UPDATE academic_units", 8154700260828, func() error {
				_, err := secondary.AcademicUnit().Archive(ctx, unit.ID.String(), model.GetMillis())
				return err
			}, operation)
		},
		MutateTeacherRoleBeforeMail: func(t *testing.T, ctx context.Context, role *model.Role, operation func() error) error {
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "teacher_invitation_mail_role_update", "roles", "UPDATE roles", 8154700260829, func() error {
				candidate := role.Clone()
				candidate.Permissions = []string{string(model.ActionProgrammeManage)}
				_, err := secondary.Role().Update(ctx, candidate)
				return err
			}, operation)
		},
		DisableInviterBeforeIssue: func(t *testing.T, ctx context.Context, inviter *model.User, operation func() error) error {
			audit := saveInvitationAuthorityAudit(t, ctx, secondary, inviter.ID, string(model.ActionUserManage), model.ResourceUser, inviter.ID.String(), model.RoleScopeInstitution, invitationTestInstitutionID(t, ctx, secondary).String())
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "invitation_disable", "users", "UPDATE users", 8154700260822, func() error {
				_, err := secondary.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
					ID: inviter.ID.String(), ExpectedRevision: inviter.Revision, Disabled: true, ChangedAt: model.GetMillis(),
					RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
					Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
				}))
				return err
			}, operation)
		},
		EndBindingBeforeAccept: func(t *testing.T, ctx context.Context, binding *model.RoleBinding, operation func() error) error {
			audit := saveInvitationAuthorityAudit(t, ctx, secondary, binding.UserID, string(model.ActionRoleBindingManage), model.ResourceClass, binding.ScopeID, binding.ScopeType, binding.ScopeID)
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "invitation_binding_end", "role_bindings", "UPDATE role_bindings", 8154700260823, func() error {
				endedAt := model.GetMillis() - 1_000
				_, err := secondary.RoleBinding().EndWithAudit(ctx, &store.RoleBindingEnd{
					ID: binding.ID.String(), EndAt: endedAt, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
					Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
				})
				return err
			}, operation)
		},
		ArchiveRoleBeforeAccept: func(t *testing.T, ctx context.Context, role *model.Role, operation func() error) error {
			institutionID := invitationTestInstitutionID(t, ctx, secondary)
			audit := saveInvitationAuthorityAudit(t, ctx, secondary, invitationRoleActorID(t, ctx, secondary, role.ID), string(model.ActionRoleManage), model.ResourceInstitution, institutionID.String(), model.RoleScopeInstitution, institutionID.String())
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "invitation_role_archive", "roles", "UPDATE roles", 8154700260824, func() error {
				_, err := secondary.Role().ArchiveWithAudit(ctx, &store.RoleArchive{
					ID: role.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
				})
				return err
			}, operation)
		},
	}
}

func invitationHierarchySQLProbe(t *testing.T, primary *SQLStore) storetest.InvitationHierarchySQLProbe {
	t.Helper()
	secondary := openTestStore(t)
	return storetest.InvitationHierarchySQLProbe{
		MoveProgrammeBeforeIssue: func(t *testing.T, ctx context.Context, programme *model.Programme, targetUnitID model.AcademicUnitID, operation func() error) error {
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "invitation_programme_move", "programmes", "UPDATE programmes", 8154700260825, func() error {
				candidate := *programme
				candidate.AcademicUnitID = targetUnitID
				_, err := secondary.Programme().Update(ctx, &candidate)
				return err
			}, operation)
		},
		MoveLevelBeforeAccept: func(t *testing.T, ctx context.Context, level *model.ProgrammeLevel, targetProgrammeID model.ProgrammeID, operation func() error) error {
			return runInvitationAuthorityMutationFirst(t, ctx, primary, "invitation_level_move", "programme_levels", "UPDATE programme_levels", 8154700260826, func() error {
				candidate := *level
				candidate.ProgrammeID = targetProgrammeID
				_, err := secondary.ProgrammeLevel().Update(ctx, &candidate)
				return err
			}, operation)
		},
	}
}

func invitationRoleActorID(t *testing.T, ctx context.Context, persistence *SQLStore, roleID model.RoleID) model.UserID {
	t.Helper()
	var id string
	if err := persistence.GetMaster().Get(ctx, &id, `SELECT user_id FROM role_bindings
		WHERE role_id=? AND archived_at IS NULL ORDER BY id LIMIT 1`, roleID.String()); err != nil {
		t.Fatal(err)
	}
	return model.UserID(id)
}

func runInvitationAuthorityMutationFirst(
	t *testing.T,
	ctx context.Context,
	persistence *SQLStore,
	name string,
	table string,
	mutationQuery string,
	pauseKey int64,
	mutation func() error,
	operation func() error,
) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pauseKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
		}
	}()
	functionName := "proctor_test_pause_" + name
	triggerName := functionName + "_trigger"
	statement := fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END $$;
		CREATE TRIGGER %s BEFORE UPDATE ON %s
		FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, pauseKey, triggerName, table, functionName)
	if _, err = persistence.GetMaster().Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = persistence.GetMaster().Exec(context.Background(), fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON %s; DROP FUNCTION IF EXISTS %s()`, triggerName, table, functionName))
	}()

	mutationResult := make(chan error, 1)
	go func() { mutationResult <- mutation() }()
	mutationPID := waitForBlockedMailQuery(t, ctx, persistence, controllerPID, mutationQuery)
	operationResult := make(chan error, 1)
	go func() { operationResult <- operation() }()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case operationErr := <-operationResult:
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, pauseKey)
			locked = false
			if mutationErr := <-mutationResult; mutationErr != nil {
				t.Fatalf("authority mutation: %v", mutationErr)
			}
			return operationErr
		case <-ticker.C:
			var blocked bool
			if err = persistence.GetMaster().Get(ctx, &blocked, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, mutationPID); err != nil {
				t.Fatal(err)
			}
			if !blocked {
				continue
			}
			if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pauseKey); err != nil {
				t.Fatal(err)
			}
			locked = false
			if mutationErr := <-mutationResult; mutationErr != nil {
				t.Fatalf("authority mutation: %v", mutationErr)
			}
			return <-operationResult
		case <-ctx.Done():
			t.Fatalf("wait for Invitation authority serialization: %v", ctx.Err())
		}
	}
}

func invitationTestInstitutionID(t *testing.T, ctx context.Context, persistence *SQLStore) model.InstitutionID {
	t.Helper()
	var id string
	if err := persistence.GetMaster().Get(ctx, &id, `SELECT id FROM institutions ORDER BY id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	return model.InstitutionID(id)
}

func saveInvitationAuthorityAudit(
	t *testing.T,
	ctx context.Context,
	persistence *SQLStore,
	actorID model.UserID,
	action string,
	resourceType model.ResourceType,
	resourceID string,
	scopeType model.RoleScopeType,
	scopeID string,
) *model.AuditEvent {
	t.Helper()
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: action, Resource: model.Resource{Type: resourceType, ID: resourceID},
		ScopeType: scopeType, ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "invitation-authority-fence-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return audit
}
