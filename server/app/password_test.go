// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
)

func testPasswordPolicy() PasswordPolicy {
	settings := config.Default().Authentication.Password
	return PasswordPolicy{
		MinimumLength:               settings.MinimumLength,
		MaximumLength:               settings.MaximumLength,
		MaximumConcurrentOperations: 2,
		ArgonMemoryKiB:              19 * 1024,
		ArgonIterations:             1,
		ArgonParallelism:            1,
		ArgonSaltBytes:              settings.ArgonSaltBytes,
		ArgonKeyBytes:               settings.ArgonKeyBytes,
	}
}

func TestPasswordHasherRoundTripAndRehash(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("Hash() = %q", encoded)
	}
	if err := hasher.Verify(context.Background(), encoded, "correct horse battery staple"); err != nil {
		t.Fatalf("Verify(correct) error = %v", err)
	}
	if err := hasher.Verify(context.Background(), encoded, "wrong password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Verify(wrong) error = %v", err)
	}
	if hasher.NeedsRehash(encoded) {
		t.Fatal("fresh hash needs rehash")
	}

	stronger := settings
	stronger.ArgonIterations = 2
	strongerHasher, err := newPasswordHasher(stronger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strongerHasher.NeedsRehash(encoded) {
		t.Fatal("changed parameters did not require rehash")
	}
}

func TestPasswordHasherRejectsUnsafeInputsAndParameters(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hasher.Hash(context.Background(), "short"); err == nil {
		t.Fatal("Hash(short) succeeded")
	}
	oversized := strings.Repeat("a", settings.MaximumLength+1)
	if _, err := hasher.Hash(context.Background(), oversized); err == nil {
		t.Fatal("Hash(oversized) succeeded")
	}
	hostile := "$argon2id$v=19$m=4294967295,t=20,p=64$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5"
	if err := hasher.Verify(context.Background(), hostile, "password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Verify(hostile) error = %v", err)
	}
}

func TestPasswordHasherRequiresPositiveWorkCapacity(t *testing.T) {
	for _, maximum := range []int{0, -1} {
		policy := testPasswordPolicy()
		policy.MaximumConcurrentOperations = maximum
		if _, err := newPasswordHasher(policy, nil); err == nil {
			t.Fatalf("capacity %d was accepted", maximum)
		}
	}
}

func TestPasswordHasherReleasesCapacityAfterMismatch(t *testing.T) {
	policy := testPasswordPolicy()
	policy.MaximumConcurrentOperations = 1
	hasher, err := newPasswordHasher(policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := hasher.Verify(context.Background(), hasher.dummyHash, "incorrect password"); !errors.Is(err, ErrPasswordMismatch) {
			t.Fatalf("mismatch did not release capacity: %v", err)
		}
	}
	if err := hasher.VerifyDummy(context.Background(), "incorrect password"); err != nil {
		t.Fatalf("dummy verification did not suppress mismatch: %v", err)
	}
}

func TestPasswordHasherSharesCapacityAndRetainsCancelledWorkUntilReturn(t *testing.T) {
	for _, operation := range []string{"hash", "verify", "verify mismatch", "dummy"} {
		t.Run(operation, func(t *testing.T) {
			entered, proceed := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(proceed) })
			t.Cleanup(release)
			announce := sync.OnceFunc(func() { close(entered) })
			recorder := &passwordWorkRecorderFake{onStart: func() { announce(); <-proceed }}
			policy := testPasswordPolicy()
			policy.MaximumConcurrentOperations = 1
			hasher, err := newPasswordHasher(policy, recorder)
			if err != nil {
				t.Fatal(err)
			}
			if recorder.started.Load() != 0 {
				t.Fatal("startup timing hash was counted as runtime work")
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "hash":
					_, err := hasher.Hash(ctx, "correct horse battery staple")
					done <- err
				case "verify":
					done <- hasher.Verify(ctx, hasher.dummyHash, "proctor-dummy-password-never-used")
				case "verify mismatch":
					done <- hasher.Verify(ctx, hasher.dummyHash, "incorrect password")
				case "dummy":
					done <- hasher.VerifyDummy(ctx, "incorrect password")
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("password work did not start")
			}
			checkBusy := func() {
				t.Helper()
				_, hashErr := hasher.Hash(context.Background(), "correct horse battery staple")
				verifyErr := hasher.Verify(context.Background(), hasher.dummyHash, "incorrect password")
				dummyErr := hasher.VerifyDummy(context.Background(), "incorrect password")
				for _, err := range []error{hashErr, verifyErr, dummyErr} {
					if !Is(err, "service.busy") {
						t.Fatalf("saturated password operation = %v, want service.busy", err)
					}
				}
			}
			checkBusy()
			cancel()
			select {
			case err := <-done:
				t.Fatalf("cancelled password work returned before synchronous work finished: %v", err)
			default:
			}
			checkBusy()
			_, hashErr := hasher.Hash(ctx, "short")
			verifyErr := hasher.Verify(ctx, "malformed", "incorrect password")
			dummyErr := hasher.VerifyDummy(ctx, "incorrect password")
			for _, err := range []error{hashErr, verifyErr, dummyErr} {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled request = %v, want context cancellation", err)
				}
			}
			if _, err := hasher.Hash(context.Background(), "short"); !Is(err, "authentication.password.invalid") {
				t.Fatalf("invalid password consumed capacity: %v", err)
			}
			if err := hasher.Verify(context.Background(), "malformed", "incorrect password"); !errors.Is(err, ErrPasswordMismatch) {
				t.Fatalf("malformed stored hash consumed capacity: %v", err)
			}
			release()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("completed cancelled operation = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled password work did not finish")
			}
			if err := hasher.VerifyDummy(context.Background(), "incorrect password"); err != nil {
				t.Fatalf("capacity was not released: %v", err)
			}
			if recorder.started.Load() != 2 || recorder.finished.Load() != 2 || recorder.rejected.Load() != 6 || recorder.duration.Load() <= 0 {
				t.Fatalf("work metrics = started %d, finished %d, rejected %d, duration %d", recorder.started.Load(), recorder.finished.Load(), recorder.rejected.Load(), recorder.duration.Load())
			}
		})
	}
}

type passwordWorkRecorderFake struct {
	started, finished, rejected atomic.Int64
	duration                    atomic.Int64
	onStart                     func()
}

func (r *passwordWorkRecorderFake) Started() {
	r.started.Add(1)
	if r.onStart != nil {
		r.onStart()
	}
}

func (r *passwordWorkRecorderFake) Finished(elapsed time.Duration) {
	r.finished.Add(1)
	r.duration.Add(int64(elapsed))
}

func (r *passwordWorkRecorderFake) Rejected() { r.rejected.Add(1) }

func saturatePasswordWork(t *testing.T, hasher *passwordHasher) func() {
	t.Helper()
	var releases []func()
	for range cap(hasher.work) {
		release, err := hasher.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	release := sync.OnceFunc(func() {
		for _, release := range releases {
			release()
		}
	})
	t.Cleanup(release)
	return release
}

type passwordHashFunc func(context.Context, string) (string, error)

func (hash passwordHashFunc) Hash(ctx context.Context, password string) (string, error) {
	return hash(ctx, password)
}

func testPasswordHashFailures(t *testing.T, unavailableCode string, invoke func(*testing.T, context.Context, passwordHash) error) {
	t.Helper()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "busy", err: NewError("service.busy"), code: "service.busy"},
		{name: "cancelled", err: context.Canceled, code: unavailableCode},
		{name: "operational failure", err: errors.New("password work unavailable"), code: unavailableCode},
		{name: "invalid password", err: NewError("authentication.password.invalid"), code: "authentication.password.invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			hasher := passwordHashFunc(func(got context.Context, _ string) (string, error) {
				calls++
				if got != ctx {
					t.Fatal("password work did not receive the request context")
				}
				return "", test.err
			})
			err := invoke(t, ctx, hasher)
			if !Is(err, test.code) || calls != 1 || !errors.Is(err, test.err) {
				t.Fatalf("password failure = %v, calls %d; want %s wrapping %v after one call", err, calls, test.code, test.err)
			}
		})
	}
}
