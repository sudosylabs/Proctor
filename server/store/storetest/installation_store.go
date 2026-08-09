// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInstallationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	if _, err := ss.Installation().Get(ctx); !store.IsNotFound(err) {
		t.Fatalf("Get(pristine) error = %v", err)
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
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := ss.Installation().Bootstrap(
				ctx,
				testInstallationBootstrap(index),
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
	if winner.State.Validate() != nil ||
		winner.Institution.ID != winner.State.InstitutionID ||
		winner.Administrator.ID != winner.State.AdministratorUserID ||
		winner.Role.Name != model.SystemAdministratorRoleName ||
		!winner.Role.BuiltIn ||
		winner.RoleBinding.ScopeType != model.RoleScopeInstitution ||
		winner.RoleBinding.ScopeID != winner.Institution.ID.String() ||
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
	return &store.InstallationBootstrap{
		Institution: institution, Administrator: creation.User,
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng",
		Role: &model.Role{
			ID: model.RoleID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
			Permissions: model.AllActions(), BuiltIn: true,
		},
		RoleBinding: &model.RoleBinding{
			ID: model.RoleBindingID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1), ArchivedAt: model.OptionalTimeFromMillis(1),
			EndsAt: model.OptionalTimeFromMillis(2),
		},
		AuditEvent: &model.AuditEvent{
			ID: model.AuditEventID(model.NewId()), CreatedAt: model.TimeFromMillis(1),
			UpdatedAt: model.TimeFromMillis(1),
			Action:    "installation.bootstrap", NodeID: "store-test",
			Parameters: parameters,
		},
		DefaultProfilePictureJob: creation.DefaultProfilePictureJob,
	}
}
