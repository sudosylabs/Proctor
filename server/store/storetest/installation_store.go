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
	if !winner.State.IsValid() ||
		winner.Institution.Id != winner.State.InstitutionId ||
		winner.Administrator.Id != winner.State.AdministratorUserId ||
		winner.Role.Name != model.SystemAdministratorRoleName ||
		!winner.Role.BuiltIn ||
		winner.RoleBinding.ScopeType != model.RoleScopeInstitution ||
		winner.RoleBinding.ScopeId != winner.Institution.Id ||
		winner.Institution.DeleteAt != 0 ||
		winner.Administrator.DeleteAt != 0 ||
		winner.Administrator.EmailVerified ||
		winner.Administrator.DisabledAt != 0 ||
		winner.Role.DeleteAt != 0 ||
		winner.RoleBinding.DeleteAt != 0 ||
		winner.RoleBinding.EndAt != 0 {
		t.Fatalf("Bootstrap() = %#v", winner)
	}
	state, err := ss.Installation().Get(ctx)
	requireNoError(t, err)
	if *state != *winner.State {
		t.Fatalf("Get() = %#v, want %#v", state, winner.State)
	}
	credential, err := ss.PasswordCredential().GetByUser(ctx, winner.Administrator.Id)
	requireNoError(t, err)
	if credential.PasswordHash == "" {
		t.Fatal("bootstrap password hash was not persisted")
	}
	events, err := ss.Audit().List(ctx, store.AuditListOptions{
		Action: "installation.bootstrap", Limit: 10,
	})
	requireNoError(t, err)
	if len(events) != 1 ||
		events[0].Status != model.AuditStatusSuccess ||
		events[0].ActorId != winner.Administrator.Id {
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
		Id: model.NewId(), CreateAt: 1, UpdateAt: 1, DeleteAt: 1,
		Name: "northbridge", DisplayName: "Northbridge University",
	}
	user := &model.User{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 1, DeleteAt: 1,
		Username:      fmt.Sprintf("administrator-%d", index),
		Email:         fmt.Sprintf("administrator-%d@example.test", index),
		DisplayName:   "Administrator",
		EmailVerified: true, DisabledAt: 1,
	}
	parameters, appErr := model.EncodeAuditData(map[string]any{"attempt": index})
	if appErr != nil {
		panic(appErr)
	}
	return &store.InstallationBootstrap{
		Institution: institution, Administrator: user,
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng",
		Role: &model.Role{
			Id: model.NewId(), CreateAt: 1, UpdateAt: 1, DeleteAt: 1,
			Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
			Permissions: model.AllActions(), BuiltIn: true,
		},
		RoleBinding: &model.RoleBinding{
			Id: model.NewId(), CreateAt: 1, UpdateAt: 1, DeleteAt: 1, EndAt: 2,
		},
		AuditEvent: &model.AuditEvent{
			Id: model.NewId(), CreateAt: 1, UpdateAt: 1,
			Action: "installation.bootstrap", NodeId: "store-test",
			Parameters: parameters,
		},
	}
}
