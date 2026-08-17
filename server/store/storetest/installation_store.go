// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInstallationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	if _, err := ss.Installation().Get(ctx); !store.IsNotFound(err) {
		t.Fatalf("Get(pristine) error = %v", err)
	}
	pristine, err := ss.Installation().ReconcileSystemAdministratorRole(
		ctx,
		testSystemAdministratorRoleReconciliation(0),
	)
	requireNoError(t, err)
	if pristine == nil || pristine.Changed || pristine.Role != nil || len(pristine.AddedPermissions) != 0 {
		t.Fatalf("ReconcileSystemAdministratorRole(pristine) = %#v", pristine)
	}
	if events, err := ss.Audit().List(ctx, store.AuditListOptions{Action: "role.system_admin.reconcile", Limit: 10}); err != nil || len(events) != 0 {
		t.Fatalf("pristine reconciliation audit events=%#v error=%v", events, err)
	}
	mismatched := testInstallationBootstrap(97)
	mismatched.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
	if _, err := ss.Installation().Bootstrap(ctx, mismatched); err == nil {
		t.Fatal("Bootstrap() accepted a default-picture Job targeting another User")
	}
	if _, err := ss.Installation().Get(ctx); !store.IsNotFound(err) {
		t.Fatalf("bootstrap state survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.User().GetByEmail(ctx, mismatched.Administrator.Email); !store.IsNotFound(err) {
		t.Fatalf("bootstrap administrator survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.Job().Get(ctx, mismatched.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
		t.Fatalf("mismatched bootstrap Job was persisted: %v", err)
	}
	if events, err := ss.Audit().List(ctx, store.AuditListOptions{Action: "installation.bootstrap", Limit: 10}); err != nil || len(events) != 0 {
		t.Fatalf("bootstrap audit survived mismatched Job rollback: events=%#v error=%v", events, err)
	}
	permanent := testInstallationBootstrap(98)
	permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
	if _, err := ss.Installation().Bootstrap(ctx, permanent); err == nil {
		t.Fatal("Bootstrap() accepted a permanent-dedupe default-picture intent")
	}
	blocker := mustJob(t, "bootstrap-job-id-blocker", model.NowUTC())
	if _, _, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: blocker}); err != nil {
		t.Fatal(err)
	}
	rolledBack := testInstallationBootstrap(98)
	rolledBack.DefaultProfilePictureJob.ID = blocker.ID
	if _, err := ss.Installation().Bootstrap(ctx, rolledBack); err == nil {
		t.Fatal("Bootstrap() succeeded when its default-picture Job insert failed")
	}
	if _, err := ss.Installation().Get(ctx); !store.IsNotFound(err) {
		t.Fatalf("bootstrap state survived Job rollback: %v", err)
	}
	if _, err := ss.User().GetByEmail(ctx, rolledBack.Administrator.Email); !store.IsNotFound(err) {
		t.Fatalf("bootstrap administrator survived Job rollback: %v", err)
	}

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
			if winner != nil {
				t.Fatal("more than one concurrent bootstrap succeeded")
			}
			winner = current.result
		case store.IsConflict(current.err):
			conflicts++
		default:
			t.Fatalf("Bootstrap() error = %v", current.err)
		}
	}
	if winner == nil || conflicts != attempts-1 {
		t.Fatalf("winner/conflicts = %#v/%d", winner, conflicts)
	}
	var winnerInput *store.InstallationBootstrap
	for _, input := range inputs {
		if input.Administrator.Email == winner.Administrator.Email {
			winnerInput = input
			break
		}
	}
	replayed, err := ss.Installation().Bootstrap(ctx, winnerInput)
	requireNoError(t, err)
	if replayed.State.InstitutionID != winner.State.InstitutionID ||
		replayed.State.AdministratorUserID != winner.State.AdministratorUserID ||
		replayed.AccessPolicy.ID != winner.AccessPolicy.ID {
		t.Fatalf("Bootstrap(exact replay) = %#v, want identities from %#v", replayed, winner)
	}
	if err := ss.User().UpdateLastLogin(ctx, winner.Administrator.ID.String(), winner.Administrator.CreatedAt.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	replayed, err = ss.Installation().Bootstrap(ctx, winnerInput)
	requireNoError(t, err)
	if replayed.Administrator.LastLoginAt.Valid {
		t.Fatalf("exact replay returned mutable current administrator state: %#v", replayed.Administrator)
	}
	if winner.State.Validate() != nil ||
		winner.Institution.ID != winner.State.InstitutionID ||
		winner.Administrator.ID != winner.State.AdministratorUserID ||
		winner.Role.Name != model.SystemAdministratorRoleName ||
		!winner.Role.BuiltIn ||
		winner.RoleBinding.ScopeType != model.RoleScopeInstitution ||
		winner.RoleBinding.ScopeID != winner.Institution.ID.String() ||
		winner.AccessPolicy == nil || winner.AccessPolicy.Validate() != nil ||
		winner.AccessPolicy.Revision != 1 || !winner.AccessPolicy.LocalLoginEnabled ||
		winner.AccessPolicy.PublicRegistrationEnabled ||
		!winner.AccessPolicy.InvitationAdmissionEnabled ||
		!winner.AccessPolicy.InvitationLocalCredentialEnabled ||
		!winner.AccessPolicy.DesktopAuthorizationEnabled ||
		len(winner.AccessPolicy.ProviderAdmissions) != 0 ||
		winner.Institution.IsArchived() ||
		winner.Administrator.ArchivedAt.Valid ||
		winner.Administrator.EmailVerified ||
		winner.Administrator.DisabledAt.Valid ||
		winner.Role.ArchivedAt.Millis() != 0 ||
		winner.RoleBinding.ArchivedAt.Millis() != 0 ||
		winner.RoleBinding.EndsAt.Millis() != 0 {
		t.Fatalf("Bootstrap() = %#v", winner)
	}
	state, err := ss.Installation().Get(ctx)
	requireNoError(t, err)
	if *state != *winner.State {
		t.Fatalf("Get() = %#v, want %#v", state, winner.State)
	}
	credential, err := ss.PasswordCredential().GetByUser(ctx, winner.Administrator.ID.String())
	requireNoError(t, err)
	if credential.PasswordHash == "" {
		t.Fatal("bootstrap password hash was not persisted")
	}
	sessions, err := ss.Session().ListByUser(ctx, winner.Administrator.ID.String())
	requireNoError(t, err)
	if len(sessions) != 0 {
		t.Fatalf("bootstrap created Sessions: %#v", sessions)
	}
	settings, err := ss.UserSettings().Get(ctx, winner.Administrator.ID)
	requireNoError(t, err)
	if settings.Source != model.UserSettingsInitialSource ||
		settings.FormatVersion != model.UserSettingsFormatVersion1 ||
		!settings.CreatedAt.Equal(winner.Administrator.CreatedAt) {
		t.Fatalf("bootstrap administrator settings = %#v", settings)
	}
	probe := testUserCreation(winner.Administrator, nil).DefaultProfilePictureJob
	queued, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: probe})
	requireNoError(t, err)
	if inserted || queued.DedupeKey != winner.Administrator.ID.String() {
		t.Fatalf("bootstrap default-picture job inserted=%t job=%#v", inserted, queued)
	}
	events, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "installation.bootstrap", Limit: 10,
	})
	requireNoError(t, err)
	if len(events) != 1 ||
		events[0].Status != model.AuditStatusSuccess ||
		events[0].ActorID != winner.Administrator.ID {
		t.Fatalf("bootstrap audit events = %#v", events)
	}
	customRole, err := ss.Role().Save(ctx, &model.Role{
		Name: "reconciliation-control", DisplayName: "Reconciliation Control",
		Permissions: []string{string(model.ActionClassView)},
	})
	requireNoError(t, err)
	customRoleBefore := customRole.Clone()
	bindingBefore, err := ss.RoleBinding().Get(ctx, winner.RoleBinding.ID.String())
	requireNoError(t, err)

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
		requireNoError(t, current.err)
		if current.result == nil || current.result.Role == nil {
			t.Fatalf("ReconcileSystemAdministratorRole() = %#v", current.result)
		}
		if current.result.Changed {
			reconciled++
			if !slices.Equal(current.result.AddedPermissions, []string{missing}) {
				t.Fatalf("added permissions = %#v, want %q", current.result.AddedPermissions, missing)
			}
		}
	}
	if reconciled != 1 {
		t.Fatalf("changed reconciliation outcomes = %d, want 1", reconciled)
	}

	protected, err := ss.Role().GetByName(ctx, model.SystemAdministratorRoleName)
	requireNoError(t, err)
	for _, permission := range model.AllActions() {
		if !slices.Contains(protected.Permissions, permission) {
			t.Fatalf("reconciled role omitted required permission %q: %#v", permission, protected.Permissions)
		}
	}
	if !slices.Contains(protected.Permissions, "future.unknown") {
		t.Fatalf("reconciliation removed an unknown downgrade permission: %#v", protected.Permissions)
	}
	customRole, err = ss.Role().Get(ctx, customRole.ID.String())
	requireNoError(t, err)
	if customRole.ID != customRoleBefore.ID || customRole.Name != customRoleBefore.Name ||
		customRole.DisplayName != customRoleBefore.DisplayName ||
		!customRole.UpdatedAt.Equal(customRoleBefore.UpdatedAt) ||
		!slices.Equal(customRole.Permissions, customRoleBefore.Permissions) {
		t.Fatalf("reconciliation changed a custom Role: before=%#v after=%#v", customRoleBefore, customRole)
	}
	bindingAfter, err := ss.RoleBinding().Get(ctx, winner.RoleBinding.ID.String())
	requireNoError(t, err)
	if *bindingAfter != *bindingBefore {
		t.Fatalf("reconciliation changed a Role Binding: before=%#v after=%#v", bindingBefore, bindingAfter)
	}
	reconciliationEvents, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "role.system_admin.reconcile", Limit: 10,
	})
	requireNoError(t, err)
	if len(reconciliationEvents) != 1 ||
		reconciliationEvents[0].Status != model.AuditStatusSuccess ||
		!reconciliationEvents[0].ActorID.IsZero() ||
		reconciliationEvents[0].Resource.Type != model.ResourceInstitution ||
		reconciliationEvents[0].Resource.ID != winner.Institution.ID.String() ||
		reconciliationEvents[0].ScopeType != model.RoleScopeInstitution ||
		reconciliationEvents[0].ScopeID != winner.Institution.ID.String() {
		t.Fatalf("reconciliation audit events = %#v", reconciliationEvents)
	}
	var auditResult struct {
		RoleID           string   `json:"role_id"`
		AddedPermissions []string `json:"added_permissions"`
	}
	if err := json.Unmarshal(reconciliationEvents[0].Result, &auditResult); err != nil {
		t.Fatal(err)
	}
	if auditResult.RoleID != protected.ID.String() || !slices.Equal(auditResult.AddedPermissions, []string{missing}) {
		t.Fatalf("reconciliation audit result = %#v", auditResult)
	}

	noOp, err := ss.Installation().ReconcileSystemAdministratorRole(ctx, testSystemAdministratorRoleReconciliation(10))
	requireNoError(t, err)
	if noOp == nil || noOp.Role == nil || noOp.Changed || len(noOp.AddedPermissions) != 0 {
		t.Fatalf("ReconcileSystemAdministratorRole(no-op) = %#v", noOp)
	}
	reconciliationEvents, err = ss.Audit().List(ctx, store.AuditListOptions{
		Action: "role.system_admin.reconcile", Limit: 10,
	})
	requireNoError(t, err)
	if len(reconciliationEvents) != 1 {
		t.Fatalf("no-op reconciliation wrote an audit event: %#v", reconciliationEvents)
	}
	invalid := testSystemAdministratorRoleReconciliation(11)
	invalid.RequiredPermissions = []string{"future.unknown"}
	if _, err := ss.Installation().ReconcileSystemAdministratorRole(ctx, invalid); err == nil {
		t.Fatal("ReconcileSystemAdministratorRole() accepted an unknown required permission")
	}
	if _, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(99)); !store.IsConflict(err) {
		t.Fatalf("Bootstrap(initialized) error = %v", err)
	}
	if _, err := ss.Installation().Bootstrap(ctx, nil); err == nil {
		t.Fatal("Bootstrap(nil) succeeded")
	}
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
			ID: model.RoleID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
			Permissions: permissions, BuiltIn: true,
		},
		RoleBinding: &model.RoleBinding{
			ID: model.RoleBindingID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			EndsAt: model.OptionalTimeFromMillis(2),
		},
		AccessPolicy: model.NewInitialAccessPolicy(model.NewAccessPolicyID(), creation.User.CreatedAt),
		AuditEvent: &model.AuditEvent{
			ID: model.AuditEventID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
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
