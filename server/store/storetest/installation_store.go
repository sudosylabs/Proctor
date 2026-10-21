// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInstallationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, err := ss.Installation().Get(ctx)
	require.True(t, store.IsNotFound(err), "Get(pristine) error")
	pristine, err := ss.Installation().ReconcileSystemAdministratorRole(
		ctx,
		testSystemAdministratorRoleReconciliation(0),
	)
	require.NoError(t, err)
	require.NotNil(t, pristine)
	require.False(t, pristine.Changed)
	require.Nil(t, pristine.Role)
	require.Empty(t, pristine.AddedPermissions)
	events, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "role.system_admin.reconcile", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "pristine reconciliation audit events")
	require.Empty(t, events, "pristine reconciliation audit events")
	mismatched := testInstallationBootstrap(97)
	mismatched.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
	_, err = ss.Installation().Bootstrap(ctx, mismatched)
	require.Error(t, err, "Bootstrap() accepted a default-picture Job targeting another User")
	_, err = ss.Installation().Get(ctx)
	require.True(t, store.IsNotFound(err), "bootstrap state survived mismatched Job rollback")
	_, err = ss.User().GetByEmail(ctx, mismatched.Administrator.Email)
	require.True(t, store.IsNotFound(err), "bootstrap administrator survived mismatched Job rollback")
	_, err = ss.Job().Get(ctx, mismatched.DefaultProfilePictureJob.ID)
	require.True(t, store.IsNotFound(err), "mismatched bootstrap Job was persisted")
	events, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "installation.bootstrap", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "bootstrap audit survived mismatched Job rollback: events")
	require.Empty(t, events, "bootstrap audit survived mismatched Job rollback: events")
	permanent := testInstallationBootstrap(98)
	permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
	_, err = ss.Installation().Bootstrap(ctx, permanent)
	require.Error(t, err, "Bootstrap() accepted a permanent-dedupe default-picture intent")
	blocker := mustJob(t, "bootstrap-job-id-blocker", model.NowUTC())
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: blocker})
	require.NoError(t, err)
	rolledBack := testInstallationBootstrap(98)
	rolledBack.DefaultProfilePictureJob.ID = blocker.ID
	_, err = ss.Installation().Bootstrap(ctx, rolledBack)
	require.Error(t, err, "Bootstrap() succeeded when its default-picture Job insert failed")
	_, err = ss.Installation().Get(ctx)
	require.True(t, store.IsNotFound(err), "bootstrap state survived Job rollback")
	_, err = ss.User().GetByEmail(ctx, rolledBack.Administrator.Email)
	require.True(t, store.IsNotFound(err), "bootstrap administrator survived Job rollback")

	const attempts = 4
	type outcome struct {
		result *model.InstallationBootstrapResult
		err    error
	}
	outcomes := make(chan outcome, attempts)
	var wait sync.WaitGroup
	inputs := make([]*store.InstallationBootstrap, attempts)
	for index := 0; index < attempts; index++ {
		inputs[index] = testInstallationBootstrap(index)
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := ss.Installation().Bootstrap(
				ctx,
				inputs[index],
			)
			outcomes <- outcome{result: result, err: err}
		}(index)
	}
	wait.Wait()
	close(outcomes)

	var winner *model.InstallationBootstrapResult
	conflicts := 0
	for current := range outcomes {
		switch {
		case current.err == nil:
			require.Nil(t, winner, "more than one concurrent bootstrap succeeded")
			winner = current.result
		case store.IsConflict(current.err):
			conflicts++
		default:
			require.FailNow(t, fmt.Sprintf("Bootstrap() error = %v", current.err))
		}
	}
	require.NotNil(t, winner, "winner/conflicts")
	require.Equal(t, attempts-1, conflicts, "winner/conflicts")
	var winnerInput *store.InstallationBootstrap
	for _, input := range inputs {
		if input.Administrator.Email == winner.Administrator.Email {
			winnerInput = input
			break
		}
	}
	replayed, err := ss.Installation().Bootstrap(ctx, winnerInput)
	require.NoError(t, err)
	require.Equal(t, winner.State.InstitutionID, replayed.State.InstitutionID, "Bootstrap(exact replay) = %#v, want identities from %#v", replayed, winner)
	require.Equal(t, winner.State.AdministratorUserID, replayed.State.AdministratorUserID, "Bootstrap(exact replay) = %#v, want identities from %#v", replayed, winner)
	require.Equal(t, winner.AccessPolicy.ID, replayed.AccessPolicy.ID, "Bootstrap(exact replay) = %#v, want identities from %#v", replayed, winner)
	require.Equal(t, winner.DesktopCompatibilityPolicy.InstitutionID, replayed.DesktopCompatibilityPolicy.InstitutionID, "Bootstrap(exact replay) = %#v, want identities from %#v", replayed, winner)
	err = ss.User().UpdateLastLogin(ctx, winner.Administrator.ID, winner.Administrator.CreatedAt.Add(time.Minute).UnixMilli())
	require.NoError(t, err)
	replayed, err = ss.Installation().Bootstrap(ctx, winnerInput)
	require.NoError(t, err)
	require.False(t, replayed.Administrator.LastLoginAt.Valid)
	require.NoError(t, winner.State.Validate())
	require.Equal(t, winner.State.InstitutionID, winner.Institution.ID)
	require.Equal(t, winner.State.AdministratorUserID, winner.Administrator.ID)
	require.Equal(t, model.SystemAdministratorRoleName, winner.Role.Name)
	require.True(t, winner.Role.BuiltIn)
	require.Equal(t, model.RoleScopeInstitution, winner.RoleBinding.ScopeType)
	require.Equal(t, winner.Institution.ID.String(), winner.RoleBinding.ScopeID)
	require.NotNil(t, winner.AccessPolicy)
	require.NoError(t, winner.AccessPolicy.Validate())
	require.Equal(t, int64(1), winner.AccessPolicy.Revision)
	require.True(t, winner.AccessPolicy.LocalLoginEnabled)
	require.False(t, winner.AccessPolicy.PublicRegistrationEnabled)
	require.True(t, winner.AccessPolicy.InvitationAdmissionEnabled)
	require.True(t, winner.AccessPolicy.InvitationLocalCredentialEnabled)
	require.True(t, winner.AccessPolicy.DesktopAuthorizationEnabled)
	require.Empty(t, winner.AccessPolicy.ProviderAdmissions)
	require.NotNil(t, winner.DesktopCompatibilityPolicy)
	require.NoError(t, winner.DesktopCompatibilityPolicy.Validate())
	require.Equal(t, winner.Institution.ID, winner.DesktopCompatibilityPolicy.InstitutionID)
	require.Equal(t, int64(1), winner.DesktopCompatibilityPolicy.Revision)
	require.Empty(t, winner.DesktopCompatibilityPolicy.MinimumDesktopRelease)
	require.Empty(t, winner.DesktopCompatibilityPolicy.RevokedDesktopBuildIDs)
	require.Empty(t, winner.DesktopCompatibilityPolicy.AdministratorMessage)
	require.False(t, winner.Institution.IsArchived())
	require.False(t, winner.Administrator.ArchivedAt.Valid)
	require.False(t, winner.Administrator.EmailVerified)
	require.False(t, winner.Administrator.DisabledAt.Valid)
	require.Equal(t, int64(0), winner.Role.ArchivedAt.Millis())
	require.Equal(t, int64(0), winner.RoleBinding.ArchivedAt.Millis())
	require.Equal(t, int64(0), winner.RoleBinding.EndsAt.Millis())
	state, err := ss.Installation().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, *winner.State, *state, "Get() = %#v, want %#v", state, winner.State)
	desktopPolicy, err := ss.DesktopCompatibilityPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, winner.Institution.ID, desktopPolicy.InstitutionID)
	require.Equal(t, int64(1), desktopPolicy.Revision)
	credential, err := ss.PasswordCredential().GetByUser(ctx, winner.Administrator.ID)
	require.NoError(t, err)
	require.NotEmpty(t, credential.PasswordHash, "bootstrap password hash was not persisted")
	sessions, err := ss.Session().ListByUser(ctx, winner.Administrator.ID)
	require.NoError(t, err)
	require.Empty(t, sessions)
	settings, err := ss.UserSettings().Get(ctx, winner.Administrator.ID)
	require.NoError(t, err)
	require.Equal(t, model.UserSettingsInitialSource, settings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
	require.True(t, settings.CreatedAt.Equal(winner.Administrator.CreatedAt))
	probe := testUserCreation(winner.Administrator, nil).DefaultProfilePictureJob
	queued, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: probe})
	require.NoError(t, err)
	require.False(t, inserted, "bootstrap default-picture job inserted=%t job=%#v", inserted, queued)
	require.Equal(t, winner.Administrator.ID.String(), queued.DedupeKey, "bootstrap default-picture job inserted=%t job=%#v", inserted, queued)
	events, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "installation.bootstrap", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, model.AuditStatusSuccess, events[0].Status)
	require.Equal(t, winner.Administrator.ID, events[0].ActorID)
	customRole, err := SaveRole(ctx, ss, &model.Role{
		Name: "reconciliation-control", DisplayName: "Reconciliation Control",
		Permissions: []string{string(model.ActionClassView)},
	})
	require.NoError(t, err)
	customRoleBefore := customRole.Clone()
	bindingBefore, err := ss.RoleBinding().Get(ctx, winner.RoleBinding.ID)
	require.NoError(t, err)

	type reconciliationOutcome struct {
		result *store.SystemAdministratorRoleReconciliationResult
		err    error
	}
	reconciliations := make(chan reconciliationOutcome, attempts)
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := ss.Installation().ReconcileSystemAdministratorRole(
				ctx,
				testSystemAdministratorRoleReconciliation(index+1),
			)
			reconciliations <- reconciliationOutcome{result: result, err: err}
		}(index)
	}
	wait.Wait()
	close(reconciliations)

	reconciled := 0
	missing := model.AllActions()[0]
	for current := range reconciliations {
		require.NoError(t, current.err)
		require.NotNil(t, current.result)
		require.NotNil(t, current.result.Role)
		if current.result.Changed {
			reconciled++
			require.True(t, slices.Equal(current.result.AddedPermissions, []string{missing}), "added permissions = %#v, want %q", current.result.AddedPermissions, missing)
		}
	}
	require.Equal(t, 1, reconciled, "changed reconciliation outcomes")

	protected, err := ss.Role().GetByName(ctx, model.SystemAdministratorRoleName)
	require.NoError(t, err)
	for _, permission := range model.AllActions() {
		require.Contains(t, protected.Permissions, permission, "reconciled role omitted required permission")
	}
	require.Contains(t, protected.Permissions, "future.unknown")
	customRole, err = ss.Role().Get(ctx, customRole.ID)
	require.NoError(t, err)
	require.Equal(t, customRoleBefore.ID, customRole.ID, "reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	require.Equal(t, customRoleBefore.Name, customRole.Name, "reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	require.Equal(t, customRoleBefore.DisplayName, customRole.DisplayName, "reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	require.True(t, customRole.UpdatedAt.Equal(customRoleBefore.UpdatedAt), "reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	require.True(t, slices.Equal(customRole.Permissions, customRoleBefore.Permissions), "reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	bindingAfter, err := ss.RoleBinding().Get(ctx, winner.RoleBinding.ID)
	require.NoError(t, err)
	require.Equal(t, *bindingBefore, *bindingAfter, "reconciliation changed a Role Binding: before=%#v after=%#v", bindingBefore, bindingAfter)
	reconciliationEvents, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "role.system_admin.reconcile", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, reconciliationEvents, 1)
	require.Equal(t, model.AuditStatusSuccess, reconciliationEvents[0].Status)
	require.True(t, reconciliationEvents[0].ActorID.IsZero())
	require.Equal(t, model.ResourceInstitution, reconciliationEvents[0].Resource.Type)
	require.Equal(t, winner.Institution.ID.String(), reconciliationEvents[0].Resource.ID)
	require.Equal(t, model.RoleScopeInstitution, reconciliationEvents[0].ScopeType)
	require.Equal(t, winner.Institution.ID.String(), reconciliationEvents[0].ScopeID)
	var auditResult struct {
		RoleID           string   `json:"role_id"`
		AddedPermissions []string `json:"added_permissions"`
	}
	require.NoError(t, json.Unmarshal(reconciliationEvents[0].Result, &auditResult))
	require.Equal(t, protected.ID.String(), auditResult.RoleID)
	require.True(t, slices.Equal(auditResult.AddedPermissions, []string{missing}))

	noOp, err := ss.Installation().ReconcileSystemAdministratorRole(ctx, testSystemAdministratorRoleReconciliation(10))
	require.NoError(t, err)
	require.NotNil(t, noOp)
	require.NotNil(t, noOp.Role)
	require.False(t, noOp.Changed)
	require.Empty(t, noOp.AddedPermissions)
	reconciliationEvents, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "role.system_admin.reconcile", Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	require.Len(t, reconciliationEvents, 1)
	invalid := testSystemAdministratorRoleReconciliation(11)
	invalid.RequiredPermissions = []string{"future.unknown"}
	_, err = ss.Installation().ReconcileSystemAdministratorRole(ctx, invalid)
	require.Error(t, err, "ReconcileSystemAdministratorRole() accepted an unknown required permission")
	_, err = ss.Installation().Bootstrap(ctx, testInstallationBootstrap(99))
	require.True(t, store.IsConflict(err), "Bootstrap(initialized) error")
	_, err = ss.Installation().Bootstrap(ctx, nil)
	require.Error(t, err, "Bootstrap(nil) succeeded")
}

func testInstallationBootstrap(index int) *store.InstallationBootstrap {
	institution := &model.Institution{
		Name: "northbridge", DisplayName: "Northbridge University",
	}
	user := &model.User{
		Username:      fmt.Sprintf("administrator-%d", index),
		Email:         fmt.Sprintf("administrator-%d@example.test", index),
		DisplayName:   "Administrator",
		EmailVerified: true, DisabledAt: model.OptionalTimeFromMillis(1),
	}
	creation := testUserCreation(user, nil)
	parameters, appErr := model.EncodeAuditData(map[string]any{"attempt": index})
	if appErr != nil {
		panic(appErr)
	}
	required := model.AllActions()
	permissions := append([]string{"future.unknown"}, required[1:]...)
	return &store.InstallationBootstrap{
		BootstrapSecretDigest: sha256.Sum256([]byte(fmt.Sprintf("bootstrap-secret-%d", index))),
		CommandFingerprint:    sha256.Sum256([]byte(fmt.Sprintf("bootstrap-command-%d", index))),
		Institution:           institution, Administrator: creation.User, AdministratorSettings: creation.Settings,
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng",
		Role: &model.Role{
			ID: model.RoleID(model.NewID()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
			Permissions: permissions, BuiltIn: true,
		},
		RoleBinding: &model.RoleBinding{
			ID: model.RoleBindingID(model.NewID()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			EndsAt: model.OptionalTimeFromMillis(2),
		},
		AccessPolicy: model.NewInitialAccessPolicy(model.NewAccessPolicyID(), creation.User.CreatedAt),
		AuditEvent: &model.AuditEvent{
			ID: model.AuditEventID(model.NewID()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1),
			Action:    "installation.bootstrap", NodeID: "store-test",
			Parameters: parameters,
		},
		DefaultProfilePictureJob: creation.DefaultProfilePictureJob,
	}
}

func testSystemAdministratorRoleReconciliation(index int) *store.SystemAdministratorRoleReconciliation {
	return &store.SystemAdministratorRoleReconciliation{
		RequiredPermissions: model.AllActions(),
		ReconciledAt:        int64(index + 100),
		AuditEvent: &model.AuditEvent{
			Action: "role.system_admin.reconcile", Status: model.AuditStatusSuccess,
			NodeID: fmt.Sprintf("store-test-%d", index), ClientType: "system",
		},
	}
}
