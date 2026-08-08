// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type installationStoreFake struct {
	events *[]string
	state  *model.InstallationState
	getErr error
	input  *store.InstallationBootstrap
	result *model.InstallationBootstrapResult
	err    error
}

func (s *installationStoreFake) Get(context.Context) (*model.InstallationState, error) {
	*s.events = append(*s.events, "get-status")
	return s.state, s.getErr
}

func (s *installationStoreFake) Bootstrap(_ context.Context, input *store.InstallationBootstrap) (*model.InstallationBootstrapResult, error) {
	*s.events = append(*s.events, "bootstrap")
	s.input = input
	return s.result, s.err
}

type passwordHasherFake struct {
	events *[]string
	hash   string
	err    error
}

func (h *passwordHasherFake) Hash(string) (string, error) {
	*h.events = append(*h.events, "hash-password")
	return h.hash, h.err
}

type bootstrapRateLimiterFake struct {
	events *[]string
	err    error
}

func (r *bootstrapRateLimiterFake) Allow(context.Context, string) error {
	*r.events = append(*r.events, "rate-limit")
	return r.err
}

func TestBootstrapStatusUninitializedOnNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, getErr: store.NewErrNotFound("installation", "")},
		&passwordHasherFake{events: &events},
		&bootstrapRateLimiterFake{events: &events},
		"node-a",
		time.Now,
	)
	status, err := service.GetStatus(context.Background())
	if err != nil || status.Initialized {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestBootstrapCommitsAtomicAggregate(t *testing.T) {
	t.Parallel()
	events := []string{}
	result := &model.InstallationBootstrapResult{
		State: &model.InstallationState{InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}
	persistence := &installationStoreFake{
		events: &events, getErr: store.NewErrNotFound("installation", ""), result: result,
	}
	service := newBootstrapService(
		persistence,
		&passwordHasherFake{events: &events, hash: "encoded"},
		&bootstrapRateLimiterFake{events: &events},
		"node-a",
		func() time.Time { return time.UnixMilli(500) },
	)
	got, err := service.Bootstrap(context.Background(), NewInvocation(model.Principal{}, model.RequestMetadata{RequestID: "req"}), BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com",
		Password: "password-value", Source: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State.InstitutionID != result.State.InstitutionID || persistence.input.PasswordHash != "encoded" {
		t.Fatalf("result/input = %#v / %#v", got, persistence.input)
	}
	if persistence.input.AuditEvent.NodeID != "node-a" || persistence.input.Role.BuiltIn != true {
		t.Fatalf("bootstrap input = %#v", persistence.input)
	}
	want := []string{"get-status", "rate-limit", "hash-password", "bootstrap"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapAlreadyInitialized(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{events: &events, state: &model.InstallationState{
			InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID(),
		}},
		&passwordHasherFake{events: &events},
		&bootstrapRateLimiterFake{events: &events},
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get-status"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestBootstrapConflictMapsToAlreadyInitialized(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newBootstrapService(
		&installationStoreFake{
			events: &events, getErr: store.NewErrNotFound("installation", ""),
			err: store.NewErrConflict("installation", "already", errors.New("race")),
		},
		&passwordHasherFake{events: &events, hash: "encoded"},
		&bootstrapRateLimiterFake{events: &events},
		"node-a",
		time.Now,
	)
	_, err := service.Bootstrap(context.Background(), Invocation{}, BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge",
		AdministratorUsername: "admin", AdministratorEmail: "admin@example.com", Password: "x",
	})
	if !Is(err, "installation.already_initialized") {
		t.Fatalf("error = %v", err)
	}
}
