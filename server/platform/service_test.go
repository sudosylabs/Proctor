// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store"
)

type testStore struct{}

func (testStore) Institution() store.InstitutionStore             { return nil }
func (testStore) AcademicUnit() store.AcademicUnitStore           { return nil }
func (testStore) Ping(context.Context) error                      { return nil }
func (testStore) GetDBSchemaVersion(context.Context) (int, error) { return 0, nil }
func (testStore) GetLocalSchemaVersion() (int, error)             { return 0, nil }
func (testStore) ValidateSchema(context.Context) error            { return nil }
func (testStore) Close() error                                    { return nil }

func TestServiceReconfiguresLoggerFromSharedConfiguration(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first.log")
	secondPath := filepath.Join(t.TempDir(), "second.log")
	store, err := config.NewStore(
		context.Background(),
		config.NewMemoryStore(nil),
		config.StoreOptions{LookupEnv: func(string) (string, bool) { return "", false }},
	)
	if err != nil {
		t.Fatal(err)
	}
	initial := store.Get()
	initial.Log.Targets = []config.LogTarget{{
		Name: "file", Type: "file", Level: "info", Format: "json", File: firstPath,
	}}
	if _, _, err := store.Set(context.Background(), initial); err != nil {
		t.Fatal(err)
	}

	service, err := New(ServiceConfig{ConfigStore: store, Store: testStore{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	service.Log().Info("first target")
	if err := service.Log().Flush(); err != nil {
		t.Fatal(err)
	}

	updated := store.Get()
	updated.Log.Targets[0].File = secondPath
	if _, _, err := store.Set(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	service.Log().Info("second target")
	if err := service.Log().Flush(); err != nil {
		t.Fatal(err)
	}

	firstData, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstData), "first target") || strings.Contains(string(firstData), "second target") {
		t.Fatalf("first target = %q", firstData)
	}
	if !strings.Contains(string(secondData), "second target") {
		t.Fatalf("second target = %q", secondData)
	}
}
