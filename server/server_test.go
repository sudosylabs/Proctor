// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type facadeRuntime struct {
	ready     bool
	startErr  error
	closeErr  error
	started   int
	closed    int
	startWith context.Context
}

func (r *facadeRuntime) Start(ctx context.Context) error {
	r.started++
	r.startWith = ctx
	return r.startErr
}

func (r *facadeRuntime) Close() error {
	r.closed++
	return r.closeErr
}

func (r *facadeRuntime) Ready() bool {
	return r.ready
}

func TestFacadeDelegatesLifecycleAndReadiness(t *testing.T) {
	t.Parallel()

	startErr := errors.New("start failure")
	closeErr := errors.New("close failure")
	runtime := &facadeRuntime{ready: true, startErr: startErr, closeErr: closeErr}
	facade := &Server{runtime: runtime}
	ctx := context.Background()

	if !facade.Ready() {
		t.Fatal("Ready() = false, want true")
	}
	if err := facade.Start(ctx); !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want %v", err, startErr)
	}
	if runtime.started != 1 || runtime.startWith != ctx {
		t.Fatalf("Start() delegation = (%d, %v), want (1, supplied context)", runtime.started, runtime.startWith)
	}
	if err := facade.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want %v", err, closeErr)
	}
	if runtime.closed != 1 {
		t.Fatalf("Close() delegation count = %d, want 1", runtime.closed)
	}
}

func TestNewConstructsUsableFacade(t *testing.T) {
	t.Parallel()

	constructed := &facadeRuntime{ready: true}
	option := func(settings *options) error {
		settings.runtimeFactory = func(context.Context, string) (runtime, error) {
			return constructed, nil
		}
		return nil
	}
	facade, err := New(context.Background(), option)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !facade.Ready() {
		t.Fatal("New().Ready() = false, want true")
	}
	if err := facade.Close(); err != nil {
		t.Fatalf("New().Close() error = %v", err)
	}
	if constructed.closed != 1 {
		t.Fatalf("New().Close() delegation count = %d, want 1", constructed.closed)
	}
}

func TestNewRejectsEmptyConfigurationPath(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), WithConfigPath(""))
	if err == nil || !strings.Contains(err.Error(), "configuration path is empty") {
		t.Fatalf("New() error = %v, want empty configuration path", err)
	}
}
